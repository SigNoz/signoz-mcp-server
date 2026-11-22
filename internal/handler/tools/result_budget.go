package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
)

// A result size budget bounds the text a client shows its model: the UTF-8
// bytes of every text block, the JSON payload and each note. The raw row tools
// and the list tools (list_budget.go) fit whole rows or items into it and page
// the rest with offset.
const (
	maxResultBytesParam = "maxResultBytes"
	// minResultBytes keeps a per-call budget above the envelope and notes alone.
	minResultBytes = 1000
	// maxResultBytesCeiling caps a per-call budget unless the server default is higher.
	maxResultBytesCeiling = 500000
	// shortenSlack leaves a string alone unless cutting it saves more than the
	// "…(+N chars)" suffix costs.
	shortenSlack = 16
	// neverShortenRunes is the longest string that is never shortened, so
	// timestamps and IDs stay whole however small the budget.
	neverShortenRunes = 48
	// paginationKey is the server-owned top-level object a paged row result gains.
	paginationKey = "pagination"
	// webURLKey values are deep links; a shortened link would be broken.
	webURLKey = "webUrl"
)

// Values of mcp.tool.result.budget.event.
const (
	budgetEventRowsDropped  = "rows_dropped"
	budgetEventRowShortened = "row_shortened"
	budgetEventOverBudget   = "over_budget"
	budgetEventFailOpen     = "fail_open"
)

// resultBudget is the budget one call applies.
type resultBudget struct {
	bytes     int
	requested int64
	clamped   bool
}

func (h *Handler) defaultResultBytes() int {
	if h.config != nil && h.config.MaxResultBytes > 0 {
		return h.config.MaxResultBytes
	}
	return config.DefaultMaxResultBytes
}

// parseResultBudget reads maxResultBytes, clamping it to [minResultBytes, the
// ceiling]. Without it the call uses the server default.
func (h *Handler) parseResultBudget(args map[string]any) (resultBudget, *mcp.CallToolResult) {
	serverDefault := h.defaultResultBytes()
	value, present, ok := looseInt(args[maxResultBytesParam])
	if !ok {
		return resultBudget{}, validationError(maxResultBytesParam, "must be an integer number of bytes, such as 64000")
	}
	if !present {
		return resultBudget{bytes: serverDefault}, nil
	}
	ceiling := max(maxResultBytesCeiling, serverDefault)
	budget := resultBudget{requested: value}
	switch {
	case value < minResultBytes:
		budget.bytes, budget.clamped = minResultBytes, true
	case value > int64(ceiling):
		budget.bytes, budget.clamped = ceiling, true
	default:
		budget.bytes = int(value)
	}
	return budget, nil
}

// clampNote says how a caller's maxResultBytes was clamped, or "" when it was not.
func (b resultBudget) clampNote() string {
	if !b.clamped {
		return ""
	}
	bound := "below the minimum"
	if b.requested > int64(b.bytes) {
		bound = "above the maximum"
	}
	return fmt.Sprintf("note: maxResultBytes %d is %s of %d, so this result used %d.", b.requested, bound, b.bytes, b.bytes)
}

// resultFit lets the validation decorator finish a budgeted result once it
// knows the notices it will append, so the delivered result, notices
// included, fits the budget.
type resultFit struct {
	mu     sync.Mutex
	finish func(extra []string) *mcp.CallToolResult
}

type resultFitKey struct{}

func withResultFit(ctx context.Context) (context.Context, *resultFit) {
	fit := &resultFit{}
	return context.WithValue(ctx, resultFitKey{}, fit), fit
}

func resultFitFrom(ctx context.Context) *resultFit {
	fit, _ := ctx.Value(resultFitKey{}).(*resultFit)
	return fit
}

func (f *resultFit) register(finish func(extra []string) *mcp.CallToolResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finish = finish
}

// complete returns the registered result rebuilt with the extra notes, or nil
// when no handler registered one.
func (f *resultFit) complete(extra []string) *mcp.CallToolResult {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	finish := f.finish
	f.mu.Unlock()
	if finish == nil {
		return nil
	}
	return finish(extra)
}

// deliverFitted registers fit with the validation decorator, which appends its
// notices after the handler returns and so must rebuild the result with them
// counted. record runs once per call, for the result delivered.
func deliverFitted(ctx context.Context, fit func(extra []string) (*mcp.CallToolResult, pageOutcome), record func(pageOutcome)) *mcp.CallToolResult {
	preview, outcome := fit(nil)
	finisher := resultFitFrom(ctx)
	if finisher == nil {
		record(outcome)
		return preview
	}
	finisher.register(func(extra []string) *mcp.CallToolResult {
		if len(extra) == 0 {
			record(outcome)
			return preview
		}
		result, final := fit(extra)
		record(final)
		return result
	})
	return preview
}

// rawSearchPage is one raw search response with what its result needs to
// page rows by size.
type rawSearchPage struct {
	toolName  string
	limit     int
	offset    int
	budget    resultBudget
	leadNotes []string
	emptyNote string
	// ignoresSelectFields is true for rows selectFields cannot narrow, so the
	// notes do not suggest it.
	ignoresSelectFields bool
	rows                *rowPage
}

// pageOutcome is what fitting did, for telemetry.
type pageOutcome struct {
	dropped    int
	shortened  int
	textBytes  int
	overBudget bool
}

// fit returns the result whose text blocks, extra notes included, fit the
// budget: every row when they fit, else the longest prefix of whole rows that
// does, else the first row with its long strings shortened.
func (p *rawSearchPage) fit(extra []string) (*mcp.CallToolResult, pageOutcome) {
	total := len(p.rows.rows)
	// Rows that alone outweigh the budget cannot fit, so a large body is not
	// rendered whole just to be measured.
	if total == 0 || p.rows.rowBytes <= p.budget.bytes {
		result, size := p.build(total, nil, false, extra)
		if size <= p.budget.bytes || total == 0 {
			return result, pageOutcome{textBytes: size, overBudget: size > p.budget.bytes}
		}
	}

	// Estimate the rows that fit from their encoded lengths, then confirm,
	// starting one row above the estimate because it can run a byte high.
	_, used := p.build(0, nil, true, extra)
	keep := 0
	for _, row := range p.rows.rows {
		used += len(row) + 1
		if used > p.budget.bytes {
			break
		}
		keep++
	}
	for keep = min(keep+1, total-1); keep > 0; keep-- {
		if result, size := p.build(keep, nil, true, extra); size <= p.budget.bytes {
			return result, pageOutcome{dropped: total - keep, textBytes: size}
		}
	}

	// One row always comes back: a page with no rows and hasMore would loop.
	truncated := total > 1
	first := p.rows.rows[0]
	sizeAt := func(keepRunes int) (*mcp.CallToolResult, int, bool) {
		row, changed := shortenStrings(first, keepRunes, "")
		if !changed {
			row = nil
		}
		result, size := p.build(1, row, truncated, extra)
		return result, size, changed
	}
	low, high := 0, longestString(first, "")
	result, size, changed := sizeAt(low)
	if size <= p.budget.bytes {
		for low < high {
			mid := low + (high-low+1)/2
			if _, midSize, _ := sizeAt(mid); midSize <= p.budget.bytes {
				low = mid
			} else {
				high = mid - 1
			}
		}
		result, size, changed = sizeAt(low)
	}
	outcome := pageOutcome{dropped: total - 1, textBytes: size, overBudget: size > p.budget.bytes}
	if changed {
		outcome.shortened = 1
	}
	return result, outcome
}

// build renders the result keeping the first keep rows, with first replacing
// row 0 when it is not nil, and returns it with its text bytes.
func (p *rawSearchPage) build(keep int, first json.RawMessage, truncated bool, extra []string) (*mcp.CallToolResult, int) {
	shortened := 0
	if first != nil {
		shortened = 1
	}
	meta := pagination{Returned: keep, Limit: p.limit, Offset: p.offset, NextOffset: -1, Truncated: truncated}
	switch {
	case truncated:
		meta.HasMore = true
	case p.limit > 0 && keep >= p.limit:
		meta.HasMore = true
	}
	if meta.HasMore {
		meta.NextOffset = p.offset + keep
	}
	if truncated || shortened > 0 || p.budget.clamped {
		meta.MaxResultBytes = p.budget.bytes
	}
	meta.ShortenedRows = shortened

	notes := append([]string(nil), p.leadNotes...)
	notes = append(notes, budgetCompletenessNote(meta, p.budget.bytes, !p.ignoresSelectFields))
	if shortened > 0 {
		notes = append(notes, shortenedRowNote(p.budget.bytes, !p.ignoresSelectFields))
	}
	if keep == 0 && !truncated && p.offset == 0 {
		notes = append(notes, p.emptyNote)
	}
	notes = append(notes, extra...)

	result := resultWithNotes(p.rows.render(keep, first, meta.encode()), notes...)
	return result, textBytes(result)
}

// pagination is the server-owned page metadata of a raw row result.
type pagination struct {
	Returned   int  `json:"returned"`
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	HasMore    bool `json:"hasMore"`
	NextOffset int  `json:"nextOffset"`
	// Truncated is true when the size budget, not the row limit, ended the page.
	Truncated      bool `json:"truncated"`
	MaxResultBytes int  `json:"maxResultBytes,omitempty"`
	ShortenedRows  int  `json:"shortenedRows,omitempty"`
}

func (p pagination) encode() json.RawMessage {
	encoded, _ := json.Marshal(p) // a struct of ints and bools always encodes
	return encoded
}

// budgetCompletenessNote is the completeness note of a paged row result. It
// keeps completenessNote's wording unless the budget ended the page.
// selectFields says whether naming fewer fields can shrink these rows.
func budgetCompletenessNote(meta pagination, budgetBytes int, selectFields bool) string {
	if !meta.Truncated {
		return completenessNote(meta.Returned, meta.Limit, meta.Offset, true)
	}
	note := fmt.Sprintf(
		"note: returned %d rows (limit %d); the result size budget of %d bytes ended this page, so more results exist (hasMore=true). Fetch the next page with offset=%d, and pass the start and end from the window note so new rows do not shift the page.",
		meta.Returned, meta.Limit, budgetBytes, meta.NextOffset)
	if selectFields {
		note += " To fit more rows in a page, name only the fields you need in selectFields."
	}
	return note + " Raise maxResultBytes only if your client can show a larger result."
}

func shortenedRowNote(budgetBytes int, selectFields bool) string {
	note := fmt.Sprintf(
		"note: the first row alone was larger than the result size budget of %d bytes, so its long string values were shortened. Each shortened value ends in …(+N chars), N being the characters removed. To see them in full, ",
		budgetBytes)
	if selectFields {
		note += "name only the fields you need in selectFields, or "
	}
	return note + "raise maxResultBytes if your client can show a larger result."
}

// textBytes sums the UTF-8 bytes of a result's text blocks.
func textBytes(result *mcp.CallToolResult) int {
	total := 0
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			total += len(text.Text)
		}
	}
	return total
}

// recordBudget puts what fitting did on the tool span and, when the budget
// changed or could not bound the result, on mcp.tool.result.budget.events.
// rows counts the rows or list items the page had before fitting.
func (h *Handler) recordBudget(ctx context.Context, toolName string, budgetBytes, rows int, outcome pageOutcome) {
	trace.SpanFromContext(ctx).SetAttributes(
		otelpkg.MCPToolResultBudgetBytesKey.Int(budgetBytes),
		otelpkg.MCPToolResultBudgetRowsDroppedKey.Int(outcome.dropped),
		otelpkg.MCPToolResultBudgetShortenedRowsKey.Int(outcome.shortened),
	)
	if outcome.dropped > 0 {
		h.countBudgetEvent(ctx, toolName, budgetEventRowsDropped)
	}
	if outcome.shortened > 0 {
		h.countBudgetEvent(ctx, toolName, budgetEventRowShortened)
	}
	if outcome.overBudget {
		h.countBudgetEvent(ctx, toolName, budgetEventOverBudget)
		h.warnLogger().WarnContext(ctx, "tool result is over its size budget after fitting rows",
			slog.String("gen_ai.tool.name", toolName),
			slog.Int("budget_bytes", budgetBytes),
			slog.Int("text_bytes", outcome.textBytes),
			slog.Int("rows", rows))
	}
}

func (h *Handler) warnLogger() *slog.Logger {
	if h.logger == nil {
		return slog.Default()
	}
	return h.logger
}

func (h *Handler) countBudgetEvent(ctx context.Context, toolName, event string) {
	if h.meters == nil || h.meters.ToolResultBudgetEvents == nil {
		return
	}
	attrs := []attribute.KeyValue{
		otelpkg.GenAIToolNameKey.String(toolName),
		otelpkg.MCPToolResultBudgetEventKey.String(event),
	}
	attrs = otelpkg.AppendClientSource(ctx, attrs)
	h.meters.ToolResultBudgetEvents.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// rowPage is a query_range body split at the rows of its one result,
// data.data.results[0].rows[], so a page can keep a prefix of them and
// re-encode the objects around them from the bytes SigNoz sent. Rows stay
// json.RawMessage, so they and their large integers keep their exact bytes.
// The values the walk passed through are dropped, so a large body is held
// once as payload and once as rows.
type rowPage struct {
	payload  []byte
	top      []jsonMember
	data     []jsonMember
	inner    []jsonMember
	result   []jsonMember // members of the one result; nil when there is none
	dataAt   int
	innerAt  int
	resultAt int
	rowsAt   int
	rows     []json.RawMessage
	rowBytes int // encoded bytes of every row, without separators
}

type jsonMember struct {
	key   string
	value json.RawMessage
}

var errPaginationTaken = fmt.Errorf("payload already has a top-level %q field", paginationKey)

// parseRowPage splits payload at its rows. It fails on any shape it cannot
// walk, when the payload already owns a top-level pagination field, and on
// more than one result: one offset cannot continue several row lists.
func parseRowPage(payload []byte) (*rowPage, error) {
	page := &rowPage{payload: payload}
	var ok bool
	if page.top, ok = decodeObject(payload); !ok {
		return nil, errors.New("payload is not a JSON object")
	}
	if memberIndex(page.top, paginationKey) >= 0 {
		return nil, errPaginationTaken
	}
	if page.dataAt = memberIndex(page.top, "data"); page.dataAt < 0 {
		return nil, errors.New("payload has no data object")
	}
	if page.data, ok = decodeObject(page.top[page.dataAt].value); !ok {
		return nil, errors.New("data is not an object")
	}
	page.top[page.dataAt].value = nil
	if page.innerAt = memberIndex(page.data, "data"); page.innerAt < 0 {
		return nil, errors.New("data has no data object")
	}
	if page.inner, ok = decodeObject(page.data[page.innerAt].value); !ok {
		return nil, errors.New("data.data is not an object")
	}
	page.data[page.innerAt].value = nil
	if page.resultAt = memberIndex(page.inner, "results"); page.resultAt < 0 {
		return nil, errors.New("data.data has no results")
	}
	results, ok := decodeArrayOrNull(page.inner[page.resultAt].value)
	if !ok {
		return nil, errors.New("results is not an array")
	}
	page.inner[page.resultAt].value = nil
	if len(results) == 0 {
		return page, nil
	}
	if len(results) > 1 {
		return nil, errors.New("payload has more than one result")
	}
	if page.result, ok = decodeObject(results[0]); !ok {
		return nil, errors.New("the result is not an object")
	}
	if page.rowsAt = memberIndex(page.result, "rows"); page.rowsAt < 0 {
		return nil, errors.New("the result has no rows")
	}
	if page.rows, ok = decodeArrayOrNull(page.result[page.rowsAt].value); !ok {
		return nil, errors.New("the result's rows is not an array")
	}
	page.result[page.rowsAt].value = nil
	for _, row := range page.rows {
		page.rowBytes += len(row)
	}
	return page, nil
}

// render encodes the body with its first keep rows, row 0 replaced by first
// when it is not nil, and meta as a top-level pagination field. A body that
// keeps every row is SigNoz's bytes with pagination added. One that lost rows
// also loses its nextCursor, which points past the rows dropped.
func (p *rowPage) render(keep int, first json.RawMessage, meta json.RawMessage) []byte {
	if keep >= len(p.rows) && first == nil {
		end := bytes.LastIndexByte(p.payload, '}')
		out := make([]byte, 0, len(p.payload)+len(paginationKey)+len(meta)+4)
		out = append(out, p.payload[:end]...)
		out = append(out, `,"`+paginationKey+`":`...)
		out = append(out, meta...)
		return append(out, p.payload[end:]...)
	}
	rows := append([]json.RawMessage(nil), p.rows[:keep]...)
	if first != nil && keep > 0 {
		rows[0] = first
	}
	result := make([]jsonMember, 0, len(p.result))
	for i, member := range p.result {
		switch {
		case i == p.rowsAt:
			member.value = encodeArray(rows)
		case member.key == "nextCursor" && keep < len(p.rows):
			continue
		}
		result = append(result, member)
	}
	inner := append([]jsonMember(nil), p.inner...)
	inner[p.resultAt].value = encodeArray([]json.RawMessage{encodeObject(result)})
	data := append([]jsonMember(nil), p.data...)
	data[p.innerAt].value = encodeObject(inner)
	top := append([]jsonMember(nil), p.top...)
	top[p.dataAt].value = encodeObject(data)
	top = append(top, jsonMember{key: paginationKey, value: meta})
	return encodeObject(top)
}

// decodeObject splits a JSON object into its members in order, keeping each
// value's bytes.
func decodeObject(raw []byte) ([]jsonMember, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	members := []jsonMember{}
	for dec.More() {
		token, err := dec.Token()
		key, isKey := token.(string)
		if err != nil || !isKey {
			return nil, false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		members = append(members, jsonMember{key: key, value: value})
	}
	if token, err := dec.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return members, true
}

func memberIndex(members []jsonMember, key string) int {
	for i, member := range members {
		if member.key == key {
			return i
		}
	}
	return -1
}

func encodeObject(members []jsonMember) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, member := range members {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(encodeString(member.key))
		out.WriteByte(':')
		out.Write(member.value)
	}
	out.WriteByte('}')
	return out.Bytes()
}

func encodeArray(values []json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(value)
	}
	out.WriteByte(']')
	return out.Bytes()
}

// encodeString encodes s as a JSON string without HTML escaping, as SigNoz
// text reaches the client.
func encodeString(s string) []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a Go string always encodes
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// shortenStrings cuts every string value in raw longer than keepRunes runes
// (by more than shortenSlack) to its first keepRunes runes plus "…(+N chars)".
// Keys, numbers, webUrl links, and strings of at most neverShortenRunes runes
// keep their bytes. It reports whether anything changed.
func shortenStrings(raw json.RawMessage, keepRunes int, key string) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}
	switch trimmed[0] {
	case '{':
		members, ok := decodeObject(trimmed)
		if !ok {
			return raw, false
		}
		changed := false
		for i, member := range members {
			if value, shortened := shortenStrings(member.value, keepRunes, member.key); shortened {
				members[i].value, changed = value, true
			}
		}
		if !changed {
			return raw, false
		}
		return encodeObject(members), true
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return raw, false
		}
		changed := false
		for i, value := range values {
			if shortened, ok := shortenStrings(value, keepRunes, ""); ok {
				values[i], changed = shortened, true
			}
		}
		if !changed {
			return raw, false
		}
		return encodeArray(values), true
	case '"':
		if key == webURLKey {
			return raw, false
		}
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return raw, false
		}
		runes := utf8.RuneCountInString(s)
		if runes <= max(keepRunes+shortenSlack, neverShortenRunes) {
			return raw, false
		}
		cut := 0
		for range keepRunes {
			_, size := utf8.DecodeRuneInString(s[cut:])
			cut += size
		}
		return encodeString(s[:cut] + fmt.Sprintf("…(+%d chars)", runes-keepRunes)), true
	default:
		return raw, false
	}
}

// longestString returns the rune count of the longest string value in raw
// that shortenStrings may cut.
func longestString(raw json.RawMessage, key string) int {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	longest := 0
	switch trimmed[0] {
	case '{':
		members, _ := decodeObject(trimmed)
		for _, member := range members {
			longest = max(longest, longestString(member.value, member.key))
		}
	case '[':
		var values []json.RawMessage
		_ = json.Unmarshal(trimmed, &values)
		for _, value := range values {
			longest = max(longest, longestString(value, ""))
		}
	case '"':
		var s string
		if key != webURLKey && json.Unmarshal(trimmed, &s) == nil {
			longest = utf8.RuneCountInString(s)
		}
	}
	return longest
}
