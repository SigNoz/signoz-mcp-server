package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"unicode/utf8"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// Views of signoz_get_trace_details.
const (
	traceDetailsViewSpans  = "spans"
	traceDetailsViewThread = "thread"
)

// threadPreviewChars caps each message string in a thread so one long prompt
// cannot crowd the rest of the conversation out of the result.
const threadPreviewChars = 1000

// threadRawMessageKeys are the raw message attributes SigNoz also returns
// decoded as formattedInput and formattedOutput.
var threadRawMessageKeys = []string{"gen_ai.input.messages", "gen_ai.output.messages"}

type traceThreadOutput struct {
	TraceID    string           `json:"traceId"`
	View       string           `json:"view"`
	Spans      []map[string]any `json:"spans"`
	NextCursor string           `json:"nextCursor,omitempty"`
	PrevCursor string           `json:"prevCursor,omitempty"`
	Truncated  bool             `json:"truncated"`
	NextAction string           `json:"nextAction,omitempty"`
}

func (h *Handler) handleGetTraceThread(ctx context.Context, args map[string]any, traceID string) *mcp.CallToolResult {
	page := signozclient.TraceThreadPage{}
	if value, present, ok := looseInt(args["limit"]); present {
		if !ok {
			return validationErrorf("limit", "must be an integer, got %v", args["limit"])
		}
		limit := int(value)
		page.Limit = &limit
	}
	page.After, _ = args["after"].(string)
	page.Before, _ = args["before"].(string)
	page.SpanID, _ = args["spanId"].(string)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_trace_details", slog.String("traceId", traceID), slog.String("view", traceDetailsViewThread))
	client, base, clientErr := h.getResourceClient(ctx)
	if clientErr != nil {
		return clientErr
	}
	body, err := client.GetTraceThread(ctx, traceID, page)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get trace thread", err, slog.String("traceId", traceID))
		return upstreamError(err)
	}

	var resp struct {
		Data *struct {
			Spans      []map[string]any `json:"spans"`
			NextCursor string           `json:"nextCursor"`
			PrevCursor string           `json:"prevCursor"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // keep nanosecond durations and timestamps exact
	if err := decoder.Decode(&resp); err != nil || resp.Data == nil || resp.Data.Spans == nil {
		h.logger.WarnContext(ctx, "Unexpected response shape from the trace thread endpoint; upstream contract may have changed",
			slog.String("traceId", traceID), slog.String("response", logpkg.TruncBody(body)))
		return upstreamResponseError("SigNoz returned the trace thread without data.spans; retry, and report it if it persists")
	}

	out := traceThreadOutput{
		TraceID:    traceID,
		View:       traceDetailsViewThread,
		Spans:      resp.Data.Spans,
		NextCursor: resp.Data.NextCursor,
		PrevCursor: resp.Data.PrevCursor,
	}
	for _, span := range out.Spans {
		if previewThreadSpan(span) {
			span["truncated"] = true
			out.Truncated = true
		}
	}
	out.NextAction = threadNextAction(out)

	payload, err := json.Marshal(out)
	if err != nil {
		return InternalErrorResult("failed to marshal response: " + err.Error())
	}
	return structuredResult(h.enrichTraceWebURL(ctx, base, payload, traceID))
}

// previewThreadSpan drops the raw message attributes and cuts long strings in
// the decoded messages and the remaining attributes. It reports whether any
// value was cut.
func previewThreadSpan(span map[string]any) bool {
	cut := false
	if attributes, ok := span["attributes"].(map[string]any); ok {
		for _, key := range threadRawMessageKeys {
			delete(attributes, key)
		}
		cut = previewStrings(attributes) || cut
	}
	for _, key := range []string{"formattedInput", "formattedOutput"} {
		if value, ok := span[key]; ok {
			span[key], cut = previewValue(value, cut)
		}
	}
	return cut
}

func previewStrings(values map[string]any) bool {
	cut := false
	for key, value := range values {
		values[key], cut = previewValue(value, cut)
	}
	return cut
}

func previewValue(value any, cut bool) (any, bool) {
	switch v := value.(type) {
	case string:
		if utf8.RuneCountInString(v) > threadPreviewChars {
			return string([]rune(v)[:threadPreviewChars]), true
		}
	case map[string]any:
		return v, previewStrings(v) || cut
	case []any:
		for i, item := range v {
			v[i], cut = previewValue(item, cut)
		}
	}
	return value, cut
}

func threadNextAction(out traceThreadOutput) string {
	var action string
	if out.NextCursor != "" {
		action = "For the next spans, call again with after set to nextCursor."
	}
	if out.Truncated {
		if action != "" {
			action += " "
		}
		action += "Spans marked truncated have values cut to 1000 characters and their raw gen_ai.input.messages and gen_ai.output.messages omitted; read a full message with signoz_search_traces, filtering on span_id and selecting those fields."
	}
	return action
}
