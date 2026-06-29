//go:build e2e

// Live E2E coverage for the trace field snake_case migration. These tests are
// read-only and use existing trace data from the target SigNoz instance.
package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestE2ETraceFields_SnakeCaseMigration(t *testing.T) {
	h, ctx := e2eHandlerC(t)
	const traceWindow = "24h"

	search := callOK(t, h.handleSearchTraces, ctx, "signoz_search_traces", map[string]any{
		"timeRange": traceWindow,
		"limit":     "5",
	})
	rows := traceFieldRows(t, firstText(search))
	if len(rows) == 0 {
		t.Skip("no traces found on staging; cannot verify trace field migration live")
	}

	row, traceID, ok := firstTraceFieldRowWithID(rows)
	if !ok {
		t.Fatalf("search_traces returned rows but none carried canonical trace_id; body prefix: %s", truncForLog(firstText(search)))
	}
	for _, key := range []string{"trace_id", "span_id", "duration_nano", "has_error", "service.name", "webUrl"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("search_traces row missing canonical field %q; row keys: %v", key, traceFieldKeys(row))
		}
	}
	for _, deprecated := range []string{"traceID", "spanID", "durationNano", "hasError"} {
		if _, ok := row[deprecated]; ok {
			t.Fatalf("search_traces row still contains deprecated field %q; row keys: %v", deprecated, traceFieldKeys(row))
		}
	}
	if webURL := rawJSONString(row["webUrl"]); !strings.Contains(webURL, "/trace/") {
		t.Fatalf("search_traces row webUrl = %q, want trace deep link", webURL)
	}
	t.Logf("search_traces canonical row fields round-tripped: trace_id/span_id/duration_nano/has_error/service.name/webUrl")

	callOK(t, h.handleSearchTraces, ctx, "signoz_search_traces", map[string]any{
		"timeRange":   traceWindow,
		"limit":       "1",
		"error":       false,
		"minDuration": "0",
		"maxDuration": "86400000000000",
	})
	t.Logf("search_traces canonical shortcut filters accepted: has_error=false and duration_nano bounds")

	callOK(t, h.handleSearchTraces, ctx, "signoz_search_traces", map[string]any{
		"timeRange": traceWindow,
		"limit":     "1",
		"filter":    "durationNano >= 0",
	})
	t.Logf("search_traces legacy free-form durationNano filter still passes through")

	callOK(t, h.handleAggregateTraces, ctx, "signoz_aggregate_traces", map[string]any{
		"timeRange":   traceWindow,
		"aggregation": "p99",
		"aggregateOn": "duration_nano",
		"requestType": "scalar",
	})
	t.Logf("aggregate_traces canonical duration_nano aggregation accepted")

	grouped := callOK(t, h.handleAggregateTraces, ctx, "signoz_aggregate_traces", map[string]any{
		"timeRange":   traceWindow,
		"aggregation": "count",
		"groupBy":     "service.name",
		"limit":       "5",
		"requestType": "scalar",
	})
	columns, groupedRows := traceAggregateColumnsAndRowCount(t, firstText(grouped))
	if groupedRows == 0 {
		t.Fatalf("aggregate_traces groupBy=service.name returned no aggregate rows despite recent trace rows")
	}
	if !traceContainsString(columns, "service.name") {
		t.Fatalf("aggregate_traces groupBy columns missing service.name; columns: %v; body prefix: %s", columns, truncForLog(firstText(grouped)))
	}
	t.Logf("aggregate_traces groupBy=service.name accepted and returned service.name groups")

	details := callOK(t, h.handleGetTraceDetails, ctx, "signoz_get_trace_details", map[string]any{
		"traceId":      traceID,
		"timeRange":    traceWindow,
		"includeSpans": true,
	})
	if body := firstText(details); !strings.Contains(body, `"webUrl"`) || !strings.Contains(body, "/trace/") {
		t.Fatalf("get_trace_details response missing trace webUrl; body prefix: %s", truncForLog(body))
	}
	t.Logf("get_trace_details found trace via canonical trace_id-backed lookup and returned webUrl")
}

type traceFieldRow map[string]json.RawMessage

func traceFieldRows(t *testing.T, body string) []traceFieldRow {
	t.Helper()
	var env struct {
		Data struct {
			Data struct {
				Results []struct {
					Rows []struct {
						Data traceFieldRow `json:"data"`
					} `json:"rows"`
				} `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal query_range body: %v; body prefix: %s", err, truncForLog(body))
	}
	var rows []traceFieldRow
	for _, result := range env.Data.Data.Results {
		for _, row := range result.Rows {
			if row.Data != nil {
				rows = append(rows, row.Data)
			}
		}
	}
	return rows
}

func firstTraceFieldRowWithID(rows []traceFieldRow) (traceFieldRow, string, bool) {
	for _, row := range rows {
		if id := rawJSONString(row["trace_id"]); id != "" {
			return row, id, true
		}
	}
	return nil, "", false
}

func rawJSONString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func traceFieldKeys(row traceFieldRow) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	return keys
}

func traceAggregateColumnsAndRowCount(t *testing.T, body string) ([]string, int) {
	t.Helper()
	var env struct {
		Data struct {
			Data struct {
				Results []struct {
					Columns []json.RawMessage `json:"columns"`
					Data    json.RawMessage   `json:"data"`
				} `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal aggregate query_range body: %v; body prefix: %s", err, truncForLog(body))
	}

	var columns []string
	rowCount := 0
	for _, result := range env.Data.Data.Results {
		for _, rawColumn := range result.Columns {
			if name := aggregateColumnName(rawColumn); name != "" {
				columns = append(columns, name)
			}
		}
		if len(result.Data) == 0 || strings.TrimSpace(string(result.Data)) == "null" {
			continue
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(result.Data, &rows); err != nil {
			t.Fatalf("unmarshal aggregate data rows: %v; body prefix: %s", err, truncForLog(body))
		}
		rowCount += len(rows)
	}
	return columns, rowCount
}

func aggregateColumnName(raw json.RawMessage) string {
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Name != "" {
		return obj.Name
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func traceContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
