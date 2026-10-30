package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// countQueryRangeRows sums the number of rows across all results in a QB v5
// query_range passthrough body. The expected nesting is
// data.data.results[].rows[] (a render.Success envelope wrapping a
// QueryRangeResponse), the same shape util.InjectRowsWebURL walks. It fails
// open: it returns (0, false) on any shape it cannot walk so a completeness
// note is simply omitted rather than wrong.
func countQueryRangeRows(payload []byte) (int, bool) {
	// Walk with json.RawMessage at each level so we can distinguish a MISSING /
	// null / non-array leaf from a genuinely-empty array. A missing or null
	// "results"/"rows" key means we could not locate the rows — we must NOT
	// claim count=0 (which would assert a misleading hasMore=false); fail open
	// to (0, false) so the caller emits the generic "more may exist" note.
	var envelope struct {
		Data struct {
			Data struct {
				Results json.RawMessage `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, false
	}
	// A present-but-null "results" is a normal empty response (matching the
	// InjectRowsWebURL contract), so count it as zero. Only a MISSING or
	// non-array, non-null "results" is uncountable drift.
	results, ok := decodeArrayOrNull(envelope.Data.Data.Results)
	if !ok {
		return 0, false
	}
	total := 0
	for _, rawResult := range results {
		var result struct {
			// Use RawMessage (not []json.RawMessage) so a missing "rows" key on
			// any single result is detectable rather than silently 0.
			Rows json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(rawResult, &result); err != nil {
			return 0, false
		}
		// A present "rows":null is a normal empty result per the v5 /
		// InjectRowsWebURL contract — count it as zero rows. Only a MISSING
		// "rows" key (always emitted by the contract) or a non-array, non-null
		// value is drift → fail open.
		if len(result.Rows) == 0 {
			return 0, false // "rows" key absent — contract drift
		}
		rows, ok := decodeArrayOrNull(result.Rows)
		if !ok {
			return 0, false // "rows" present but not an array or null — drift
		}
		total += len(rows)
	}
	return total, true
}

// countDataArrayRows counts the elements of a JSON array nested at data.<key>
// in a passthrough body (e.g. data.metrics for list_metrics, data.samples for
// top_metrics). A present-but-null leaf counts as zero rows (a normal empty
// collection). It fails open — returns (0, false) — only when the key is ABSENT
// or its value is neither an array nor null, so a misleading hasMore=false is
// never asserted on an uncountable shape.
func countDataArrayRows(payload []byte, key string) (int, bool) {
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return 0, false
	}
	raw, present := resp.Data[key]
	if !present || len(raw) == 0 {
		return 0, false // key absent
	}
	arr, ok := decodeArrayOrNull(raw)
	if !ok {
		return 0, false // present but neither array nor null
	}
	return len(arr), true
}

// decodeArrayOrNull decodes raw as a JSON array OR a literal null, returning the
// elements (nil for null) and true. A present null is a NORMAL empty collection
// (matching the InjectRowsWebURL contract), so it counts as zero rows rather
// than as uncountable drift. It returns (nil, false) only when raw is
// absent/empty or is a non-array, non-null value. Callers handle the
// absent-key case separately (len(raw)==0) when "missing" must be distinguished
// from "present null".
func decodeArrayOrNull(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false // key absent
	}
	if string(bytesTrimSpace(raw)) == "null" {
		return nil, true // literal null == normal empty collection
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false // not an array
	}
	if arr == nil {
		// Defensive: json.Unmarshal into a slice leaves it nil for "null",
		// which we already handled above; any other nil here is treated as
		// empty so a genuinely-empty array is never mistaken for drift.
		return nil, true
	}
	return arr, true
}

// bytesTrimSpace trims ASCII whitespace from a json.RawMessage without
// allocating a new string, so the "null" literal check tolerates surrounding
// whitespace.
func bytesTrimSpace(b json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(b)))
}

// completenessNote builds a pagination/completeness advisory for raw-passthrough
// tools that accept limit/offset but expose no hasMore signal of their own. When
// the returned row count is known it infers hasMore from returnedRows == limit
// and reports the nextOffset; otherwise it falls back to a generic "limit
// applied; more may exist" note so the LLM is never left assuming completeness.
func completenessNote(returnedRows, limit, offset int, rowsKnown bool) string {
	if !rowsKnown {
		return fmt.Sprintf(
			"note: limit %d applied; this tool cannot count returned rows, so more results may exist. Paginate with \"offset\" (or narrow the query) to be sure.",
			limit)
	}
	hasMore := limit > 0 && returnedRows >= limit
	nextOffset := offset + returnedRows
	if hasMore {
		return fmt.Sprintf(
			"note: returned %d rows (limit %d) — more results likely exist (hasMore=true). Fetch the next page with offset=%d.",
			returnedRows, limit, nextOffset)
	}
	return fmt.Sprintf(
		"note: returned %d rows (limit %d) — all matching results returned (hasMore=false).",
		returnedRows, limit)
}

// limitOnlyCompletenessNote is the completeness advisory for list tools that
// expose a `limit` but NO offset pagination (e.g. signoz_list_metrics). It must
// not tell callers to "fetch the next page with offset" — there is no offset
// param, so the caller would loop on the same page. Instead it advises
// narrowing the result set (searchText / time range / source). narrowHint names
// the concrete params for the tool.
func limitOnlyCompletenessNote(returnedRows, limit int, rowsKnown bool, narrowHint string) string {
	if !rowsKnown {
		return fmt.Sprintf(
			"note: limit %d applied; this tool cannot count returned rows, so more results may exist. Narrow the result set (%s) to be sure.",
			limit, narrowHint)
	}
	if limit > 0 && returnedRows >= limit {
		return fmt.Sprintf(
			"note: returned %d rows (limit %d) — more results likely exist (hasMore=true). This tool has no offset paging; narrow the result set (%s) to surface the rest.",
			returnedRows, limit, narrowHint)
	}
	return fmt.Sprintf(
		"note: returned %d rows (limit %d) — all matching results returned (hasMore=false).",
		returnedRows, limit)
}

// isTrivialBody reports whether a payload is effectively empty ("", {}, [], null)
// so the drift WARN doesn't fire on a legitimately empty response.
func isTrivialBody(payload []byte) bool {
	switch string(bytes.TrimSpace(payload)) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

// warnRowCountUnknown warns when the row counter couldn't locate the rows array
// on a non-trivial body (likely a renamed "rows" field). Fails open.
func warnRowCountUnknown(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, rowsKnown bool) {
	if rowsKnown || isTrivialBody(payload) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"row count unknown on non-empty body",
		slog.String("tool", toolName),
	)
}
