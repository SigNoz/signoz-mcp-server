package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const (
	// traceResultLimit is counted on the text as a client counts it: JSON-escaped.
	traceResultLimit = 50_000
	// Notes appear twice in a result: in its structured content and as text blocks.
	traceNoteRoom           = 4_000
	traceValueCut           = 500
	traceThreadMessageCut   = 1_000
	traceOverviewBesideSpan = 5
	traceThreadView         = "thread"
	traceScopeAll           = "all"
)

var (
	traceScopeRoutes = map[string]string{traceScopeAll: "", "descendants": "descendants", "children": "children", "ancestors": "ancestors"}
	// traceShortenFirst lists the overview's views, as SigNoz keys them, in the
	// order they give up spans when the overview is too long. A view not named
	// here gives up spans before any of them; errorOrigins is last.
	traceShortenFirst = []string{"links", "aiUsage", "timeSpent", "unmarkedFailures", "errorOrigins"}
	traceparentHeader = regexp.MustCompile(`^[0-9a-f]{2}-([0-9a-f]{32})-[0-9a-f]{16}-[0-9a-f]{2}$`)
	camelBoundary     = regexp.MustCompile(`([a-z])([A-Z])`)
)

type traceDetailsCall struct {
	traceID         string
	spanID          string
	scope           string
	views           []string
	search          string
	selectFields    []string
	pageSize        int
	cursor          string
	includeOverview bool
}

func (h *Handler) handleGetTraceDetails(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	call, errResult := parseTraceDetailsCall(args)
	if errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_trace_details", slog.String("traceId", call.traceID), slog.String("spanId", call.spanID))
	client, base, clientErr := h.getResourceClient(ctx)
	if clientErr != nil {
		return clientErr, nil
	}
	webURL, _ := util.ResourceWebURL(base, "trace", call.traceID)

	switch {
	case slices.Contains(call.views, traceThreadView):
		return h.traceThread(ctx, client, call, webURL), nil
	case call.spanID != "" && call.scope == "" && len(call.views) == 0 && call.search == "":
		return h.traceSingleSpan(ctx, client, call, webURL), nil
	case call.scope != "" || call.search != "" || call.spanID != "":
		return h.traceSpanList(ctx, client, call, webURL), nil
	default:
		return h.traceOverview(ctx, client, call, webURL), nil
	}
}

// parseTraceDetailsCall checks only what this tool decides: which inputs pick
// which result. SigNoz validates view names and page sizes.
func parseTraceDetailsCall(args map[string]any) (*traceDetailsCall, *mcp.CallToolResult) {
	rawTraceID, errResult := requireStringArg(args, "traceId")
	if errResult != nil {
		return nil, errResult
	}
	if _, sent := args["includeSpans"]; sent {
		return nil, validationError("includeSpans", `is no longer supported: traceId alone returns the trace overview, and scope "all" lists its spans`)
	}
	call := &traceDetailsCall{traceID: cleanTraceID(rawTraceID)}

	for key, target := range map[string]*string{"spanId": &call.spanID, "scope": &call.scope, "search": &call.search, "cursor": &call.cursor} {
		value, errResult := optionalStringArg(args, key)
		if errResult != nil {
			return nil, errResult
		}
		*target = strings.TrimSpace(value)
	}
	if _, known := traceScopeRoutes[call.scope]; call.scope != "" && !known {
		return nil, validationErrorf("scope", "must be one of all, descendants, children, or ancestors; got %q", call.scope)
	}
	if call.scope == traceScopeAll && call.spanID != "" {
		return nil, validationError("scope", `"all" lists the whole trace; for the spans below spanId use "descendants"`)
	}
	if call.scope != "" && call.scope != traceScopeAll && call.spanID == "" {
		return nil, validationErrorf("scope", "%q needs a spanId", call.scope)
	}

	var err error
	if call.views, err = parseStringList("view", args["view"], "view", "error_origins", "time_spent"); err != nil {
		return nil, errorWithCode(CodeValidationFailed, validationErrorPrefix+" "+err.Error())
	}
	if call.selectFields, err = parseStringList("selectFields", args["selectFields"], "field", "http.route", "db.statement"); err != nil {
		return nil, errorWithCode(CodeValidationFailed, validationErrorPrefix+" "+err.Error())
	}
	if call.includeOverview, _, err = parseBoolArg(args, "includeOverview"); err != nil {
		return nil, errorWithCode(CodeValidationFailed, validationErrorPrefix+" "+err.Error())
	}
	pageSize, sent, ok := looseInt(args["pageSize"])
	if !ok || (sent && pageSize < 1) {
		return nil, validationError("pageSize", "must be a positive integer")
	}
	call.pageSize = int(pageSize)

	thread := slices.Contains(call.views, traceThreadView)
	if thread && (len(call.views) > 1 || call.spanID != "" || call.scope != "" || call.search != "" || len(call.selectFields) > 0) {
		return nil, validationError("view", `"thread" takes only traceId, pageSize, and cursor`)
	}
	listed := !thread && (call.scope != "" || call.search != "" || call.spanID != "")
	if listed && len(call.views) > 1 {
		return nil, validationErrorf("view", "takes exactly one view in a list, got %d; call once for each view, or send traceId with the views alone for the overview", len(call.views))
	}
	return call, nil
}

// cleanTraceID accepts a dashed UUID and a whole traceparent header.
func cleanTraceID(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if header := traceparentHeader.FindStringSubmatch(value); header != nil {
		return header[1]
	}
	return strings.ReplaceAll(value, "-", "")
}

func (h *Handler) traceOverview(ctx context.Context, client signozclient.Client, call *traceDetailsCall, webURL string) *mcp.CallToolResult {
	overview, errResult := h.fetchTraceOverview(ctx, client, call.traceID, call.views, call.pageSize)
	if errResult != nil {
		return errResult
	}
	var cut []string
	cutLongValues(overview, "", traceValueCut, &cut)
	notes := shortenTraceOverview(overview, overview)
	if summary, ok := overview["summary"].(map[string]any); ok && summary["ai"] != nil {
		notes = append(notes, `This trace has AI spans: view "thread" returns the conversation.`)
	}
	notes = append(notes, unusedTraceInputs(call, false)...)
	return h.traceResult(ctx, overview, webURL, append(notes, traceCutNote(cut)))
}

// fetchTraceOverview returns SigNoz's highlights object with the trace summary added.
func (h *Handler) fetchTraceOverview(ctx context.Context, client signozclient.Client, traceID string, views []string, pageSize int) (map[string]any, *mcp.CallToolResult) {
	summary, errResult := h.fetchTrace(ctx, traceID, "summary", nil, func() (json.RawMessage, error) {
		return client.GetTraceSummary(ctx, traceID)
	})
	if errResult != nil {
		return nil, errResult
	}
	overview, errResult := h.fetchTrace(ctx, traceID, "highlights", []string{"services"}, func() (json.RawMessage, error) {
		return client.GetTraceHighlights(ctx, traceID, views, pageSize)
	})
	if errResult != nil {
		return nil, errResult
	}
	overview["summary"] = summary
	return overview, nil
}

// shortenTraceOverview drops spans from the end of the overview's views until
// whole fits the size limit, and says where each view continues.
func shortenTraceOverview(overview, whole map[string]any) []string {
	var views []string
	for key, value := range overview {
		if list, ok := value.(map[string]any); ok {
			if _, isList := list["items"].([]any); isList {
				views = append(views, key)
			}
		}
	}
	slices.SortFunc(views, func(a, b string) int {
		if order := slices.Index(traceShortenFirst, a) - slices.Index(traceShortenFirst, b); order != 0 {
			return order
		}
		return strings.Compare(a, b)
	})

	var notes []string
	size := clientLen(encodeJSON(whole))
	for _, key := range views {
		list := overview[key].(map[string]any)
		items := list["items"].([]any)
		sent := len(items)
		for len(items) > 0 && size > traceResultLimit-traceNoteRoom {
			size -= clientLen(encodeJSON(items[len(items)-1])) - 1
			items = items[:len(items)-1]
			list["items"] = items
		}
		view := strings.ToLower(camelBoundary.ReplaceAllString(key, "${1}_${2}"))
		total, _, _ := looseInt(list["total"])
		if len(items) < sent {
			notes = append(notes, fmt.Sprintf("%s shows %d of %d to fit the size limit.", key, len(items), total))
		}
		if total > int64(len(items)) {
			notes = append(notes, fmt.Sprintf(`More of %s: scope "all" with view %q.`, key, view))
		}
	}
	return notes
}

func (h *Handler) traceSingleSpan(ctx context.Context, client signozclient.Client, call *traceDetailsCall, webURL string) *mcp.CallToolResult {
	span, errResult := h.fetchTrace(ctx, call.traceID, "span", nil, func() (json.RawMessage, error) {
		return client.GetTraceSpan(ctx, call.traceID, call.spanID)
	})
	if errResult != nil {
		return errResult
	}
	notes := fitTraceSpan(span)

	payload := map[string]any{"span": span}
	if call.includeOverview {
		overview, errResult := h.fetchTraceOverview(ctx, client, call.traceID, nil, traceOverviewBesideSpan)
		if errResult != nil {
			return errResult
		}
		var cut []string
		cutLongValues(overview, "traceOverview", traceValueCut, &cut)
		payload["traceOverview"] = overview
		// The span is never shortened for the overview; the overview gives way.
		notes = append(notes, shortenTraceOverview(overview, payload)...)
		notes = append(notes, traceCutNote(cut))
	}
	if len(call.selectFields) > 0 {
		notes = append(notes, "selectFields applies to a list; a single span already has every field.")
	}
	if call.cursor != "" {
		notes = append(notes, "cursor applies to a list or the thread; it was not used.")
	}
	return h.traceResult(ctx, payload, webURL, notes)
}

// fitTraceSpan keeps a span within one result. Its values stay whole unless it
// is too long; then the longest values are cut, and if that is not enough,
// events are dropped from the end.
func fitTraceSpan(span map[string]any) []string {
	limit := traceResultLimit - traceNoteRoom
	original := encodeJSON(span)
	if clientLen(original) <= limit {
		return nil
	}
	var notes, cut []string
	for valueLimit := limit / 2; valueLimit >= traceValueCut; valueLimit /= 2 {
		// Each pass starts from the span as SigNoz sent it, so a note gives each value's full length.
		fresh := decodeTraceObject(original)
		cut = nil
		cutLongValues(fresh, "", valueLimit, &cut)
		clear(span)
		maps.Copy(span, fresh)
		if clientLen(encodeJSON(span)) <= limit {
			break
		}
	}
	if len(cut) > 0 {
		notes = append(notes, fmt.Sprintf("The span is too long for one result, so its longest values were cut: %s.", strings.Join(cut, "; ")))
	}
	if events, ok := span["events"].([]any); ok {
		kept := events
		for len(kept) > 0 && clientLen(encodeJSON(span)) > limit {
			kept = kept[:len(kept)-1]
			span["events"] = kept
		}
		if len(kept) < len(events) {
			notes = append(notes, fmt.Sprintf("events shows the first %d of %d to fit the size limit.", len(kept), len(events)))
		}
	}
	return notes
}

func decodeTraceObject(data []byte) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	_ = decoder.Decode(&object)
	return object
}

func (h *Handler) traceSpanList(ctx context.Context, client signozclient.Client, call *traceDetailsCall, webURL string) *mcp.CallToolResult {
	scope := call.scope
	if scope == "" {
		scope = traceScopeAll
		if call.spanID != "" {
			scope = "descendants"
		}
	}
	view := ""
	if len(call.views) > 0 {
		view = call.views[0]
	}
	kind := traceCallKind(call.traceID, call.spanID, scope, view, call.search, call.selectFields)
	position, errResult := decodeTraceCursor(call.cursor, kind)
	if errResult != nil {
		return errResult
	}
	data, errResult := h.fetchTrace(ctx, call.traceID, "spans", []string{"items", "total"}, func() (json.RawMessage, error) {
		return client.ListTraceSpans(ctx, call.traceID, call.spanID, traceScopeRoutes[scope], view, call.search, call.selectFields, position.Offset, call.pageSize)
	})
	if errResult != nil {
		return errResult
	}

	var cut []string
	items, _ := data["items"].([]any)
	shown := make([]any, 0, len(items))
	size := 0
	for i, item := range items {
		cutLongValues(item, fmt.Sprintf("items[%d]", i), traceValueCut, &cut)
		size += clientLen(encodeJSON(item)) + 1
		if len(shown) > 0 && size > traceResultLimit-traceNoteRoom {
			break
		}
		shown = append(shown, item)
	}
	data["items"] = shown

	total, _, _ := looseInt(data["total"])
	last := position.Offset + len(shown)
	pagination := map[string]any{"hasMore": int64(last) < total}
	var notes []string
	switch {
	case len(shown) == 0:
		notes = append(notes, fmt.Sprintf("No spans on this page; %d match in all.", total))
	case int64(last) < total:
		next := encodeTraceCursor(traceCursor{Kind: kind, Offset: last})
		pagination["nextCursor"] = next
		ended := ""
		if len(shown) < len(items) {
			ended = " The page ended early at the size limit."
		}
		notes = append(notes, fmt.Sprintf("Items %d to %d of %d.%s More exist: call again with cursor %q and the same traceId, spanId, scope, view, search, and selectFields.",
			position.Offset+1, last, total, ended, next))
	default:
		notes = append(notes, fmt.Sprintf("Items %d to %d of %d. This is the last page.", position.Offset+1, last, total))
	}
	data["pagination"] = pagination
	notes = append(notes, unusedTraceInputs(call, true)...)
	return h.traceResult(ctx, data, webURL, append(notes, traceCutNote(cut)))
}

func (h *Handler) traceThread(ctx context.Context, client signozclient.Client, call *traceDetailsCall, webURL string) *mcp.CallToolResult {
	kind := traceCallKind(call.traceID, traceThreadView)
	position, errResult := decodeTraceCursor(call.cursor, kind)
	if errResult != nil {
		return errResult
	}
	fetch := func(limit int) (map[string]any, *mcp.CallToolResult) {
		return h.fetchTrace(ctx, call.traceID, "thread", []string{"spans"}, func() (json.RawMessage, error) {
			return client.GetTraceThread(ctx, call.traceID, limit, position.Thread)
		})
	}
	data, errResult := fetch(call.pageSize)
	if errResult != nil {
		return errResult
	}
	spans, _ := data["spans"].([]any)
	fit := make([]any, 0, len(spans))
	size := 0
	for _, span := range spans {
		span = trimTraceThreadSpan(span)
		size += clientLen(encodeJSON(span)) + 1
		if len(fit) > 0 && size > traceResultLimit-traceNoteRoom {
			break
		}
		fit = append(fit, span)
	}

	var notes []string
	next, _ := data["nextCursor"].(string)
	if len(fit) < len(spans) {
		// SigNoz builds the cursor, so asking again for only the spans that fit
		// is how the next page starts at the right span.
		shorter, errResult := fetch(len(fit))
		if errResult != nil {
			return errResult
		}
		next, _ = shorter["nextCursor"].(string)
		notes = append(notes, "The page ended early at the size limit.")
	}
	if len(fit) == 1 {
		if span, ok := fit[0].(map[string]any); ok {
			if dropped := fitTraceThreadSpan(span); dropped > 0 {
				notes = append(notes, fmt.Sprintf("Span %v does not fit: its %d oldest input messages were left out.", span["span_id"], dropped))
			}
		}
	}
	notes = append(notes, fmt.Sprintf("%d spans of the conversation in time order, with their messages. Attributes, resource attributes, and events are left out: open a span with spanId for those.", len(fit)))
	pagination := map[string]any{"hasMore": next != ""}
	if next != "" {
		cursor := encodeTraceCursor(traceCursor{Kind: kind, Thread: next})
		pagination["nextCursor"] = cursor
		notes = append(notes, fmt.Sprintf(`More exist: call again with view "thread" and cursor %q.`, cursor))
	} else {
		notes = append(notes, "This is the last page.")
	}
	return h.traceResult(ctx, map[string]any{"spans": fit, "pagination": pagination}, webURL, notes)
}

// trimTraceThreadSpan drops what a conversation does not need from a span and
// cuts its messages. Fields SigNoz adds later pass through.
func trimTraceThreadSpan(raw any) any {
	span, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	if resource, ok := span["resource"].(map[string]any); ok {
		span["service.name"] = resource["service.name"]
	}
	for _, key := range []string{"attributes", "resource", "events", "references"} {
		delete(span, key)
	}
	var cut []string
	return cutLongValues(span, "", traceThreadMessageCut, &cut)
}

// fitTraceThreadSpan drops the oldest input messages of a span that is too long
// on its own and returns how many it dropped.
func fitTraceThreadSpan(span map[string]any) int {
	messages, _ := span["formattedInput"].([]any)
	dropped := 0
	for len(messages) > 0 && clientLen(encodeJSON(span)) > traceResultLimit-traceNoteRoom {
		messages = messages[1:]
		span["formattedInput"] = messages
		dropped++
	}
	return dropped
}

// fetchTrace decodes the data object of a trace route. A response without the
// keys the handler reads is reported, not read as empty.
func (h *Handler) fetchTrace(ctx context.Context, traceID, route string, required []string, call func() (json.RawMessage, error)) (map[string]any, *mcp.CallToolResult) {
	body, err := call()
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get trace details", err, slog.String("traceId", traceID), slog.String("route", route))
		return nil, upstreamError(err)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&envelope)
	for _, key := range required {
		if _, ok := envelope.Data[key]; !ok {
			decodeErr = fmt.Errorf("data.%s is missing", key)
		}
	}
	if decodeErr != nil || envelope.Data == nil {
		h.logger.WarnContext(ctx, "Unexpected response shape from a trace details route; the upstream contract may have changed",
			slog.String("traceId", traceID), slog.String("route", route), slog.String("response", logpkg.TruncBody(body)))
		return nil, upstreamResponseError(fmt.Sprintf("SigNoz returned the trace %s in a shape this tool does not know; retry, and report it if it persists", route))
	}
	return envelope.Data, nil
}

func (h *Handler) traceResult(ctx context.Context, payload map[string]any, webURL string, notes []string) *mcp.CallToolResult {
	if webURL != "" {
		payload["webUrl"] = webURL
	}
	return structuredResultWithNotes(ctx, h.logger, encodeJSON(payload), notes...)
}

// unusedTraceInputs names the inputs a result did not use, so none is ignored silently.
func unusedTraceInputs(call *traceDetailsCall, list bool) []string {
	var notes []string
	if call.includeOverview {
		notes = append(notes, "includeOverview applies only to traceId with spanId alone; it was not used.")
	}
	if !list && len(call.selectFields) > 0 {
		notes = append(notes, "selectFields applies to a list; it was not used.")
	}
	if !list && call.cursor != "" {
		notes = append(notes, "cursor applies to a list or the thread; it was not used.")
	}
	return notes
}

func traceCutNote(cut []string) string {
	if len(cut) == 0 {
		return ""
	}
	shown, more := cut, ""
	if len(cut) > 8 {
		shown, more = cut[:8], fmt.Sprintf("; and %d more", len(cut)-8)
	}
	return fmt.Sprintf("Values over %d characters were cut: %s%s. Open a span with spanId for its values whole.", traceValueCut, strings.Join(shown, "; "), more)
}

// cutLongValues cuts, in place, every string longer than limit and records where.
func cutLongValues(value any, path string, limit int, cut *[]string) any {
	switch typed := value.(type) {
	case string:
		runes := []rune(typed)
		if len(runes) <= limit {
			return typed
		}
		*cut = append(*cut, fmt.Sprintf("%s (%d characters)", path, len(runes)))
		return string(runes[:limit]) + fmt.Sprintf("… [cut: %d characters]", len(runes))
	case map[string]any:
		for key, item := range typed {
			child := key
			if path != "" {
				child = path + "." + key
			}
			typed[key] = cutLongValues(item, child, limit, cut)
		}
	case []any:
		for i, item := range typed {
			typed[i] = cutLongValues(item, fmt.Sprintf("%s[%d]", path, i), limit, cut)
		}
	}
	return value
}

// traceCursor is what the opaque cursor holds: the call it belongs to, and the
// position of the next page.
type traceCursor struct {
	Kind   string `json:"k"`
	Offset int    `json:"o,omitempty"`
	Thread string `json:"t,omitempty"`
}

func traceCallKind(parts ...any) string {
	sum := sha256.Sum256(encodeJSON(parts))
	return hex.EncodeToString(sum[:6])
}

func encodeTraceCursor(cursor traceCursor) string {
	return base64.RawURLEncoding.EncodeToString(encodeJSON(cursor))
}

func decodeTraceCursor(encoded, kind string) (traceCursor, *mcp.CallToolResult) {
	if encoded == "" {
		return traceCursor{}, nil
	}
	var cursor traceCursor
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(raw, &cursor) != nil {
		return traceCursor{}, validationError("cursor", "is not valid: pass the cursor of the previous result unchanged")
	}
	if cursor.Kind != kind || cursor.Offset < 0 {
		return traceCursor{}, validationError("cursor", "belongs to a different call: send it with the same traceId, spanId, scope, view, search, and selectFields as the call that returned it")
	}
	return cursor, nil
}

func encodeJSON(value any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return []byte("null")
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
}

// clientLen is the length a client counts for a text block: the text as a JSON string.
func clientLen(text []byte) int {
	quoted, err := json.Marshal(string(text))
	if err != nil {
		return len(text)
	}
	return len(quoted)
}
