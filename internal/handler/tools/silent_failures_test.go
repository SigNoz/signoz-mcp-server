package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- N3: shared boolean parser ---

func TestParseBoolArg(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		wantVal   bool
		wantOK    bool
		wantError bool
	}{
		{name: "real bool true", args: map[string]any{"k": true}, wantVal: true, wantOK: true},
		{name: "real bool false", args: map[string]any{"k": false}, wantVal: false, wantOK: true},
		{name: "string true", args: map[string]any{"k": "true"}, wantVal: true, wantOK: true},
		{name: "string false", args: map[string]any{"k": "false"}, wantVal: false, wantOK: true},
		{name: "string TRUE case-insensitive", args: map[string]any{"k": "TRUE"}, wantVal: true, wantOK: true},
		{name: "string False case-insensitive", args: map[string]any{"k": "False"}, wantVal: false, wantOK: true},
		{name: "string padded true", args: map[string]any{"k": "  true  "}, wantVal: true, wantOK: true},
		{name: "absent", args: map[string]any{}, wantVal: false, wantOK: false},
		{name: "nil value", args: map[string]any{"k": nil}, wantVal: false, wantOK: false},
		{name: "empty string treated as absent", args: map[string]any{"k": ""}, wantVal: false, wantOK: false},
		{name: "garbage string errors", args: map[string]any{"k": "maybe"}, wantError: true},
		{name: "numeric string errors", args: map[string]any{"k": "1"}, wantError: true},
		{name: "number type errors", args: map[string]any{"k": float64(1)}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok, err := parseBoolArg(tt.args, "k")
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got val=%v ok=%v", val, ok)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tt.wantVal || ok != tt.wantOK {
				t.Fatalf("got (val=%v ok=%v), want (val=%v ok=%v)", val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

// N3: the `error` trace filter must hard-error on garbage rather than silently
// dropping the filter (which widened results).
func TestParseSearchTracesArgs_ErrorFilter(t *testing.T) {
	// real bool true -> has_error = true
	reqTrue, err := parseSearchTracesArgs(map[string]any{"error": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reqTrue.FilterExpression, "has_error = true") {
		t.Fatalf("expected has_error = true in filter, got %q", reqTrue.FilterExpression)
	}

	// string "false" -> has_error = false
	reqFalse, err := parseSearchTracesArgs(map[string]any{"error": "false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reqFalse.FilterExpression, "has_error = false") {
		t.Fatalf("expected has_error = false in filter, got %q", reqFalse.FilterExpression)
	}

	// absent -> no generated error clause
	reqAbsent, err := parseSearchTracesArgs(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(reqAbsent.FilterExpression, "has_error") {
		t.Fatalf("expected no has_error clause when absent, got %q", reqAbsent.FilterExpression)
	}

	// garbage -> hard error (previously silently dropped + widened results)
	if _, err := parseSearchTracesArgs(map[string]any{"error": "yes"}); err == nil {
		t.Fatal("expected hard error on garbage error value, got nil")
	}
}

// N3: list_alerts tri-state bools stay nil when absent but hard-error on garbage.
func TestHandleListAlerts_GarbageBoolErrors(t *testing.T) {
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{"active": "maybe"})
	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for garbage active value")
	}
	// The bool validation now goes through the coded path (handleListAlerts wraps
	// the parse error with errorWithCode(CodeValidationFailed, ...)). Pin the
	// machine-readable code so a silent regression to an uncoded error fails here.
	if code := resultCode(t, result); code != CodeValidationFailed {
		t.Fatalf("garbage active value code = %q, want %q", code, CodeValidationFailed)
	}
}

// N3: list_alerts accepts a real JSON bool for the tri-state filters.
func TestHandleListAlerts_RealBoolAccepted(t *testing.T) {
	var captured types.ListAlertsParams
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			captured = params
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{"active": false, "silenced": true})
	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if captured.Active == nil || *captured.Active != false {
		t.Errorf("expected active=false, got %v", captured.Active)
	}
	if captured.Silenced == nil || *captured.Silenced != true {
		t.Errorf("expected silenced=true, got %v", captured.Silenced)
	}
	if captured.Inhibited != nil {
		t.Errorf("expected inhibited=nil when omitted, got %v", *captured.Inhibited)
	}
}

// N3: query_metrics isMonotonic garbage value hard-errors.
func TestParseMetricsQueryArgs_IsMonotonicGarbage(t *testing.T) {
	if _, err := parseMetricsQueryArgs(map[string]any{"metricName": "m", "isMonotonic": "yes"}); err == nil {
		t.Fatal("expected error on garbage isMonotonic, got nil")
	}
	req, err := parseMetricsQueryArgs(map[string]any{"metricName": "m", "isMonotonic": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !req.IsMonotonic {
		t.Fatal("expected IsMonotonic=true")
	}
	req2, err := parseMetricsQueryArgs(map[string]any{"metricName": "m", "isMonotonic": "FALSE"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req2.IsMonotonic {
		t.Fatal("expected IsMonotonic=false from string FALSE")
	}
}

// --- N2: stepInterval numeric value accepted ---

func TestParseAggregateArgs_StepIntervalNumericAndString(t *testing.T) {
	// JSON number (previously silently dropped by the string-only assertion)
	numReq, err := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": float64(60),
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if numReq.StepInterval == nil || *numReq.StepInterval != 60 {
		t.Fatalf("expected stepInterval=60 from JSON number, got %v", numReq.StepInterval)
	}
	if numReq.StepIntervalWarning != "" {
		t.Fatalf("expected no warning for valid numeric stepInterval, got %q", numReq.StepIntervalWarning)
	}

	// numeric string still works
	strReq, err := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": "3600",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strReq.StepInterval == nil || *strReq.StepInterval != 3600 {
		t.Fatalf("expected stepInterval=3600 from string, got %v", strReq.StepInterval)
	}

	// unparseable present value -> nil interval + warning surfaced
	badReq, err := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": "abc",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if badReq.StepInterval != nil {
		t.Fatalf("expected nil stepInterval for garbage, got %v", *badReq.StepInterval)
	}
	if badReq.StepIntervalWarning == "" {
		t.Fatal("expected a stepInterval warning for an unparseable value")
	}
}

// N2 (regression): a suffixed string like "1h"/"60s" must NOT be accepted as a
// numeric prefix (1/60); it must warn + auto-select instead of applying a
// silently-wrong bucket size. Direct parseStepInterval coverage + via the
// aggregate parser.
func TestParseStepInterval_RejectsSuffixedStrings(t *testing.T) {
	tests := []struct {
		name     string
		in       any
		wantNil  bool
		wantWarn bool
		wantVal  int64
	}{
		{name: "whole-int string", in: "60", wantVal: 60},
		{name: "json number", in: float64(120), wantVal: 120},
		{name: "duration 1h rejected", in: "1h", wantNil: true, wantWarn: true},
		{name: "duration 60s rejected", in: "60s", wantNil: true, wantWarn: true},
		{name: "garbage abc rejected", in: "abc", wantNil: true, wantWarn: true},
		{name: "hex-ish rejected", in: "0x10", wantNil: true, wantWarn: true},
		{name: "negative rejected", in: "-5", wantNil: true, wantWarn: true},
		{name: "zero rejected", in: "0", wantNil: true, wantWarn: true},
		{name: "empty string -> not set, no warn", in: "", wantNil: true, wantWarn: false},
		{name: "absent (nil) -> not set, no warn", in: nil, wantNil: true, wantWarn: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			si, warn := parseStepInterval(tt.in)
			if tt.wantNil && si != nil {
				t.Fatalf("expected nil interval, got %d", *si)
			}
			if !tt.wantNil {
				if si == nil {
					t.Fatalf("expected interval %d, got nil", tt.wantVal)
				}
				if *si != tt.wantVal {
					t.Fatalf("interval = %d, want %d", *si, tt.wantVal)
				}
			}
			if tt.wantWarn && warn == "" {
				t.Fatalf("expected a warning for %v", tt.in)
			}
			if !tt.wantWarn && warn != "" {
				t.Fatalf("unexpected warning for %v: %q", tt.in, warn)
			}
		})
	}

	// End-to-end through the aggregate parser: "1h" must not yield 1.
	req, err := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": "1h",
	}, "logs", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.StepInterval != nil {
		t.Fatalf(`"1h" must be rejected, not parsed as %d`, *req.StepInterval)
	}
	if req.StepIntervalWarning == "" {
		t.Fatal(`expected a warning for "1h"`)
	}
}

// Comment-1 regression: the list_metrics completeness note must NOT instruct
// callers to paginate by offset (the tool has no offset param).
func TestHandleListMetrics_NoteDoesNotClaimOffset(t *testing.T) {
	// 5 rows at limit 5 -> hasMore=true branch, the one that previously said
	// "fetch the next page with offset=".
	mock := &client.MockClient{
		ListMetricsFn: func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"metrics":[{},{},{},{},{}]}}`), nil
		},
	}
	h := newTestHandler(mock)
	res, err := h.handleListMetrics(testCtx(), makeToolRequest("signoz_list_metrics", map[string]any{"limit": "5"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	note := res.Content[1].(mcp.TextContent).Text
	// Must NOT instruct the caller to paginate by offset (no offset param). The
	// clarifying phrase "no offset paging" is fine; an "offset=" page-fetch
	// instruction or "next page" directive is not.
	if strings.Contains(note, "offset=") {
		t.Fatalf("list_metrics note must not give an offset= page-fetch instruction; got %q", note)
	}
	if strings.Contains(strings.ToLower(note), "next page") {
		t.Fatalf("list_metrics note must not say 'next page' (no offset paging); got %q", note)
	}
	if !strings.Contains(note, "hasMore=true") {
		t.Fatalf("expected hasMore=true in note, got %q", note)
	}
	if !strings.Contains(strings.ToLower(note), "narrow") {
		t.Fatalf("expected a narrow-the-result-set hint, got %q", note)
	}
}

// --- N1: execute_builder_query surfaces backend warnings ---

func TestHandleExecuteBuilderQuery_SurfacesBackendWarning(t *testing.T) {
	warningMessage := "Key http.status_code is ambiguous; using resource context"
	response := json.RawMessage(`{"status":"success","data":{"warning":{"warnings":[{"message":"` + warningMessage + `"}]},"data":{"results":[]}}}`)
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return response, nil
		},
	}
	h := newTestHandler(mock)

	// A minimal-but-valid QB v5 payload.
	query := map[string]any{
		"schemaVersion": "v1",
		"start":         1711123200000,
		"end":           1711130400000,
		"requestType":   "raw",
		"compositeQuery": map[string]any{
			"queries": []any{
				map[string]any{
					"type": "builder_query",
					"spec": map[string]any{
						"name":   "A",
						"signal": "logs",
						"limit":  100,
						"order": []any{
							map[string]any{"key": map[string]any{"name": "timestamp"}, "direction": "desc"},
							map[string]any{"key": map[string]any{"name": "id"}, "direction": "desc"},
						},
					},
				},
			},
		},
	}
	req := makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query})

	result, err := h.handleExecuteBuilderQuery(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want raw JSON + warning note", len(result.Content))
	}
	block0 := result.Content[0].(mcp.TextContent).Text
	if block0 != string(response) {
		t.Fatalf("block 0 = %q, want raw response unchanged", block0)
	}
	note := result.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, warningMessage) {
		t.Fatalf("warning note = %q, want backend warning message", note)
	}
}

func TestHandleExecuteBuilderQuery_NoWarningSingleBlock(t *testing.T) {
	response := json.RawMessage(`{"status":"success","data":{"data":{"results":[]}}}`)
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return response, nil
		},
	}
	h := newTestHandler(mock)
	query := map[string]any{
		"schemaVersion": "v1",
		"start":         1711123200000,
		"end":           1711130400000,
		"requestType":   "raw",
		"compositeQuery": map[string]any{"queries": []any{map[string]any{"type": "builder_query", "spec": map[string]any{
			"name": "A", "signal": "logs", "limit": 100,
			"order": []any{
				map[string]any{"key": map[string]any{"name": "timestamp"}, "direction": "desc"},
				map[string]any{"key": map[string]any{"name": "id"}, "direction": "desc"},
			},
		}}}},
	}
	req := makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query})

	result, err := h.handleExecuteBuilderQuery(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content block count = %d, want raw JSON only (no warnings)", len(result.Content))
	}
}

func TestHandleExecuteBuilderQuery_SurfacesAppliedBounds(t *testing.T) {
	response := json.RawMessage(`{"status":"success","data":{"data":{"results":[]}}}`)
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = append([]byte(nil), body...)
			return response, nil
		},
	}
	h := newTestHandler(mock)
	query := map[string]any{
		"schemaVersion": "v1",
		"start":         1711123200000,
		"end":           1711130400000,
		"requestType":   "raw",
		"compositeQuery": map[string]any{"queries": []any{map[string]any{
			"type": "builder_query",
			"spec": map[string]any{"name": "A", "signal": "logs"},
		}}},
	}

	result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want raw JSON + decisions note", len(result.Content))
	}
	note := result.Content[1].(mcp.TextContent).Text
	for _, want := range []string{"[Decisions applied]", "limit=100", "timestamp desc, id desc"} {
		if !strings.Contains(note, want) {
			t.Fatalf("decisions note = %q, want %q", note, want)
		}
	}

	var payload types.QueryPayload
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("captured payload is invalid: %v; body=%s", err, captured)
	}
	spec := payload.CompositeQuery.Queries[0].Spec.(types.QuerySpec)
	if spec.Limit != types.DefaultRawQueryLimit || len(spec.Order) != 2 || spec.Order[1].Key.Name != "id" {
		t.Fatalf("captured bounds = limit %d order %#v", spec.Limit, spec.Order)
	}
}

func TestHandleExecuteBuilderQuery_SurfacesFormulaInputDefault(t *testing.T) {
	response := json.RawMessage(`{"status":"success","data":{"data":{"results":[]}}}`)
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = append([]byte(nil), body...)
			return response, nil
		},
	}
	h := newTestHandler(mock)
	query := map[string]any{
		"schemaVersion": "v1",
		"start":         1711123200000,
		"end":           1711130400000,
		"requestType":   "time_series",
		"compositeQuery": map[string]any{"queries": []any{
			map[string]any{"type": "builder_query", "spec": map[string]any{
				"name": "A", "signal": "metrics", "aggregations": []any{map[string]any{"metricName": "errors", "spaceAggregation": "sum"}},
			}},
			map[string]any{"type": "builder_formula", "spec": map[string]any{"name": "F1", "expression": "A * 100"}},
		}},
	}

	result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if !resultNotesContain(result, "limit=10000 (formula-input default; applied before formula evaluation)") {
		t.Fatalf("formula-input decision missing: %v", allTextBlocks(result))
	}

	var payload types.QueryPayload
	if err := json.Unmarshal(captured, &payload); err != nil {
		t.Fatalf("captured payload is invalid: %v; body=%s", err, captured)
	}
	if got := payload.CompositeQuery.Queries[0].Spec.(types.QuerySpec).Limit; got != types.DefaultFormulaInputQueryLimit {
		t.Fatalf("formula input limit = %d, want %d", got, types.DefaultFormulaInputQueryLimit)
	}
	if got := payload.CompositeQuery.Queries[1].Spec.(types.FormulaSpec).Limit; got != types.DefaultAggregateQueryLimit {
		t.Fatalf("formula result limit = %d, want %d", got, types.DefaultAggregateQueryLimit)
	}
}

// --- N4: completeness notes ---

func TestCountQueryRangeRows(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantN   int
		wantOK  bool
	}{
		{
			name:    "two results with rows",
			payload: `{"data":{"data":{"results":[{"rows":[{"data":{}},{"data":{}}]},{"rows":[{"data":{}}]}]}}}`,
			wantN:   3, wantOK: true,
		},
		{
			name:    "genuinely empty results array is known-zero",
			payload: `{"data":{"data":{"results":[]}}}`,
			wantN:   0, wantOK: true,
		},
		{
			name:    "result present with empty rows array is known-zero",
			payload: `{"data":{"data":{"results":[{"rows":[]}]}}}`,
			wantN:   0, wantOK: true,
		},
		// --- MISSING key / non-array-non-null leaf -> uncountable (fail open) ---
		{
			name:    "result object missing rows key -> fail open",
			payload: `{"data":{"data":{"results":[{}]}}}`,
			wantOK:  false,
		},
		{
			name:    "results key missing -> fail open",
			payload: `{"data":{"data":{}}}`,
			wantOK:  false,
		},
		{
			name:    "results not an array -> fail open",
			payload: `{"data":{"data":{"results":{}}}}`,
			wantOK:  false,
		},
		{
			name:    "rows not an array -> fail open",
			payload: `{"data":{"data":{"results":[{"rows":{}}]}}}`,
			wantOK:  false,
		},
		{
			name:    "non-QB shape -> fail open",
			payload: `{"data":[]}`,
			wantOK:  false,
		},
		// --- present-null leaf -> NORMAL empty (known-zero), matching the
		// InjectRowsWebURL contract. NOT fail-open. ---
		{
			name:    "present null results -> known-zero",
			payload: `{"data":{"data":{"results":null}}}`,
			wantN:   0, wantOK: true,
		},
		{
			name:    "result object with present null rows -> known-zero",
			payload: `{"data":{"data":{"results":[{"rows":null}]}}}`,
			wantN:   0, wantOK: true,
		},
		{
			name:    "mix of present-null rows and a populated result",
			payload: `{"data":{"data":{"results":[{"rows":null},{"rows":[{"data":{}},{"data":{}}]}]}}}`,
			wantN:   2, wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := countQueryRangeRows([]byte(tt.payload))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (n=%d)", ok, tt.wantOK, n)
			}
			if tt.wantOK && n != tt.wantN {
				t.Fatalf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestCountDataArrayRows(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		key     string
		wantN   int
		wantOK  bool
	}{
		{name: "two items", payload: `{"data":{"items":[{},{}]}}`, key: "items", wantN: 2, wantOK: true},
		{name: "empty array is known-zero", payload: `{"data":{"metrics":[]}}`, key: "metrics", wantN: 0, wantOK: true},
		// present-null leaf -> NORMAL empty (known-zero), not fail-open.
		{name: "present null key -> known-zero", payload: `{"data":{"metrics":null}}`, key: "metrics", wantN: 0, wantOK: true},
		// --- MISSING key / non-array-non-null -> fail open ---
		{name: "key absent -> fail open", payload: `{"data":{}}`, key: "items", wantOK: false},
		{name: "key not an array -> fail open", payload: `{"data":{"samples":{}}}`, key: "samples", wantOK: false},
		{name: "data missing -> fail open", payload: `{"status":"success"}`, key: "items", wantOK: false},
		{name: "data null -> fail open", payload: `{"data":null}`, key: "items", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := countDataArrayRows([]byte(tt.payload), tt.key)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (n=%d)", ok, tt.wantOK, n)
			}
			if tt.wantOK && n != tt.wantN {
				t.Fatalf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestCountAlertHistoryRowsFamilyADataShapes(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantN   int
		wantOK  bool
	}{
		{name: "top-level data array counts", payload: `{"status":"success","data":[{},{}]}`, wantN: 2, wantOK: true},
		{name: "data items array counts", payload: `{"status":"success","data":{"items":[{}, {}, {}]}}`, wantN: 3, wantOK: true},
		{name: "present data null is known zero", payload: `{"status":"success","data":null}`, wantN: 0, wantOK: true},
		{name: "present items null is known zero", payload: `{"status":"success","data":{"items":null}}`, wantN: 0, wantOK: true},
		{name: "missing data fails open", payload: `{"status":"success"}`, wantOK: false},
		{name: "data object without items fails open", payload: `{"status":"success","data":{}}`, wantOK: false},
		{name: "data string fails open", payload: `{"status":"success","data":"unexpected"}`, wantOK: false},
		{name: "items object fails open", payload: `{"status":"success","data":{"items":{}}}`, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := countAlertHistoryRows([]byte(tt.payload))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (n=%d)", ok, tt.wantOK, n)
			}
			if tt.wantOK && n != tt.wantN {
				t.Fatalf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

// When the row count can't be located, the per-tool handlers must emit the
// generic "limit N applied" note instead of a misleading hasMore=false.
func TestHandlers_MissingLeaf_GenericNote(t *testing.T) {
	t.Run("search_logs missing rows key", func(t *testing.T) {
		mock := &client.MockClient{
			QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
				// result object present but no rows[] -> cannot count
				return json.RawMessage(`{"status":"success","data":{"data":{"results":[{}]}}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", map[string]any{"limit": "100"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if strings.Contains(note, "hasMore=false") {
			t.Fatalf("must not assert hasMore=false on un-countable body; note=%q", note)
		}
		if !strings.Contains(note, "limit 100 applied") {
			t.Fatalf("expected generic fallback note, got %q", note)
		}
	})

	t.Run("list_metrics missing metrics key", func(t *testing.T) {
		mock := &client.MockClient{
			ListMetricsFn: func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
				// "metrics" key absent (not null) -> uncountable -> generic note
				return json.RawMessage(`{"status":"success","data":{}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleListMetrics(testCtx(), makeToolRequest("signoz_list_metrics", map[string]any{"limit": "50"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if strings.Contains(note, "hasMore=false") {
			t.Fatalf("must not assert hasMore=false on missing metrics key; note=%q", note)
		}
		// list_metrics uses the limit-only note; its uncountable branch says
		// "limit N applied" and advises narrowing (no offset claim).
		if !strings.Contains(note, "limit 50 applied") {
			t.Fatalf("expected generic fallback note, got %q", note)
		}
	})

	t.Run("get_alert_history missing items", func(t *testing.T) {
		mock := &client.MockClient{
			GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
				return json.RawMessage(`{"data":{}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleGetAlertHistory(testCtx(), makeToolRequest("signoz_get_alert_history", map[string]any{
			"ruleId": "rule-x", "limit": "20",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if strings.Contains(note, "hasMore=false") {
			t.Fatalf("must not assert hasMore=false on missing items; note=%q", note)
		}
		if !strings.Contains(note, "limit 20 applied") {
			t.Fatalf("expected generic fallback note, got %q", note)
		}
	})

	t.Run("get_top_metrics non-array samples", func(t *testing.T) {
		mock := &client.MockClient{
			GetTopMetricsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
				// "samples" present but a non-array, non-null value -> uncountable
				return json.RawMessage(`{"status":"success","data":{"samples":{}}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleGetTopMetrics(testCtx(), makeToolRequest("signoz_get_top_metrics", map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if strings.Contains(note, "hasMore=false") {
			t.Fatalf("must not assert hasMore=false on non-array samples; note=%q", note)
		}
		// top_metrics generic branch: "showing up to the top 100"
		if !strings.Contains(note, "up to the top 100") {
			t.Fatalf("expected top-metrics generic fallback note, got %q", note)
		}
	})
}

// Present-null leaf arrays are a NORMAL empty collection (per the
// InjectRowsWebURL contract), so the handlers must report a known-zero result
// (hasMore=false), NOT the generic "more may exist" note.
func TestHandlers_PresentNullLeaf_HasMoreFalse(t *testing.T) {
	t.Run("search_logs present-null rows", func(t *testing.T) {
		mock := &client.MockClient{
			QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
				return json.RawMessage(`{"status":"success","data":{"data":{"results":[{"rows":null}]}}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", map[string]any{"limit": "100"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if !strings.Contains(note, "hasMore=false") {
			t.Fatalf("expected known-zero hasMore=false on present-null rows; note=%q", note)
		}
	})

	t.Run("list_metrics present-null metrics", func(t *testing.T) {
		mock := &client.MockClient{
			ListMetricsFn: func(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
				return json.RawMessage(`{"status":"success","data":{"metrics":null}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleListMetrics(testCtx(), makeToolRequest("signoz_list_metrics", map[string]any{"limit": "50"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if !strings.Contains(note, "hasMore=false") {
			t.Fatalf("expected known-zero hasMore=false on present-null metrics; note=%q", note)
		}
	})

	t.Run("get_alert_history present-null items", func(t *testing.T) {
		mock := &client.MockClient{
			GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
				return json.RawMessage(`{"data":{"items":null}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleGetAlertHistory(testCtx(), makeToolRequest("signoz_get_alert_history", map[string]any{
			"ruleId": "rule-x", "limit": "20",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		if !strings.Contains(note, "hasMore=false") {
			t.Fatalf("expected known-zero hasMore=false on present-null items; note=%q", note)
		}
	})

	t.Run("get_top_metrics present-null samples", func(t *testing.T) {
		mock := &client.MockClient{
			GetTopMetricsFn: func(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
				return json.RawMessage(`{"status":"success","data":{"samples":null}}`), nil
			},
		}
		h := newTestHandler(mock)
		res, err := h.handleGetTopMetrics(testCtx(), makeToolRequest("signoz_get_top_metrics", map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		note := res.Content[1].(mcp.TextContent).Text
		// 0 returned (< cap) -> "all ranked metrics returned for this window (hasMore=false)"
		if !strings.Contains(note, "hasMore=false") {
			t.Fatalf("expected known-zero hasMore=false on present-null samples; note=%q", note)
		}
	})
}

func TestCompletenessNote_HasMore(t *testing.T) {
	// returnedRows == limit -> hasMore=true, nextOffset advances
	note := completenessNote(100, 100, 0, true)
	if !strings.Contains(note, "hasMore=true") || !strings.Contains(note, "offset=100") {
		t.Fatalf("expected hasMore=true + offset=100, got %q", note)
	}
	// returnedRows < limit -> hasMore=false
	note = completenessNote(7, 100, 0, true)
	if !strings.Contains(note, "hasMore=false") {
		t.Fatalf("expected hasMore=false, got %q", note)
	}
	// unknown row count -> generic note
	note = completenessNote(0, 100, 0, false)
	if !strings.Contains(note, "limit 100 applied") {
		t.Fatalf("expected generic note when row count unknown, got %q", note)
	}
}

func TestHandleGetAlertHistoryFamilyA_TopLevelDataArrayCompletenessNote(t *testing.T) {
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"state":"firing"},{"state":"inactive"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleGetAlertHistory(testCtx(), makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId": "rule-x",
		"limit":  "2",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want JSON + completeness note", len(result.Content))
	}
	note := result.Content[1].(mcp.TextContent).Text
	if strings.Contains(note, "cannot count returned rows") {
		t.Fatalf("must count top-level data[] alert history rows; note=%q", note)
	}
	// v2 paginates by cursor: with no data.nextCursor there are no more pages,
	// so the note reports hasMore=false (the server emits a cursor only when
	// more rows exist — strictly more accurate than the old offset heuristic).
	if !strings.Contains(note, "returned 2 rows") || !strings.Contains(note, "hasMore=false") {
		t.Fatalf("completeness note = %q, want 2 rows / hasMore=false", note)
	}
}

// TestHandleGetAlertHistory_NextCursorHasMore pins the v2 pagination signal: a
// response carrying data.nextCursor reports hasMore=true and names the cursor,
// resolved absolute time range, and order to pass back. The upstream cursor is
// forwarded unchanged.
func TestHandleGetAlertHistory_NextCursorHasMore(t *testing.T) {
	var captured types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			captured = req
			return json.RawMessage(`{"status":"success","data":{"items":[{"state":"firing"},{"state":"inactive"}],"total":42,"nextCursor":"CURSOR_XYZ"}}`), nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleGetAlertHistory(testCtx(), makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId": "rule-x",
		"start":  "1697385600000",
		"end":    "1697472000000",
		"limit":  "2",
		"order":  "desc",
		"cursor": "CURSOR_PREV",
		"filter": "severity = 'critical'",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if captured.Cursor != "CURSOR_PREV" {
		t.Errorf("forwarded cursor = %q, want CURSOR_PREV", captured.Cursor)
	}
	if captured.FilterExpression != "severity = 'critical'" {
		t.Errorf("forwarded filterExpression = %q, want severity = 'critical'", captured.FilterExpression)
	}
	if captured.Start != 1697385600000 || captured.End != 1697472000000 || captured.Order != "desc" {
		t.Errorf("forwarded scope = start:%d end:%d order:%q, want explicit millisecond range and desc", captured.Start, captured.End, captured.Order)
	}
	note := result.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, "hasMore=true") || !strings.Contains(note, "CURSOR_XYZ") {
		t.Fatalf("completeness note = %q, want hasMore=true naming cursor CURSOR_XYZ", note)
	}
	for _, want := range []string{"start=1697385600000", "end=1697472000000", `order="desc"`, "same state and filter"} {
		if !strings.Contains(note, want) {
			t.Fatalf("completeness note = %q, want scoped continuation containing %q", note, want)
		}
	}
}

// TestHandleGetAlertHistory_ItemsExactFillNoCursor pins the key v2 semantic on
// the real timeline shape (data.items[]): a page whose item count equals the
// limit but which carries NO nextCursor is the last page, so the note must
// report hasMore=false. This is exactly the case the old row-count heuristic got
// wrong (it would have claimed hasMore=true because returnedRows == limit); v2
// trusts the server's cursor, which is only emitted when more rows remain.
func TestHandleGetAlertHistory_ItemsExactFillNoCursor(t *testing.T) {
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"items":[{"ruleId":"r1","state":"firing","unixMilli":1},{"ruleId":"r1","state":"inactive","unixMilli":2}],"total":2}}`), nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleGetAlertHistory(testCtx(), makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId": "r1",
		"limit":  "2",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	note := result.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, "returned 2 rows") || !strings.Contains(note, "hasMore=false") {
		t.Fatalf("completeness note = %q, want 2 rows / hasMore=false (exact-fill, no cursor)", note)
	}
}

func TestHandleSearchLogs_AppendsCompletenessNote(t *testing.T) {
	// 2 rows returned against a small limit -> hasMore=false
	response := json.RawMessage(`{"status":"success","data":{"data":{"results":[{"rows":[{"data":{"body":"a"}},{"data":{"body":"b"}}]}]}}}`)
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return response, nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_search_logs", map[string]any{"filter": "service.name = 'x'", "limit": "100"})
	result, err := h.handleSearchLogs(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want JSON + completeness note", len(result.Content))
	}
	note := result.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, "returned 2 rows") || !strings.Contains(note, "hasMore=false") {
		t.Fatalf("completeness note = %q, want 2 rows / hasMore=false", note)
	}
}

// --- N5: empty/non-array data coerced to empty page ---

func TestHandleListDashboards_NullDataEmptyPage(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","data":null}`,
		`{"status":"success"}`,
		`{"status":"success","data":{}}`,
	} {
		mock := &client.MockClient{
			ListDashboardsFn: func(ctx context.Context) (json.RawMessage, error) {
				return json.RawMessage(body), nil
			},
		}
		h := newTestHandler(mock)
		req := makeToolRequest("signoz_list_dashboards", map[string]any{})
		result, err := h.handleListDashboards(testCtx(), req)
		if err != nil {
			t.Fatalf("body=%s unexpected error: %v", body, err)
		}
		if result.IsError {
			t.Fatalf("body=%s expected empty page, got error result: %v", body, result.Content)
		}
		text := textContent(t, result)
		var resp map[string]any
		if err := json.Unmarshal([]byte(text), &resp); err != nil {
			t.Fatalf("body=%s response not JSON: %v", body, err)
		}
		data, ok := resp["data"].([]any)
		if !ok || len(data) != 0 {
			t.Fatalf("body=%s expected empty data array, got %v", body, resp["data"])
		}
	}
}

func TestHandleListNotificationChannels_NullDataEmptyPage(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","data":null}`,
		`{"status":"success"}`,
		`{"status":"success","data":{}}`,
	} {
		mock := &client.MockClient{
			ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
				return json.RawMessage(body), nil
			},
		}
		h := newTestHandler(mock)
		req := makeToolRequest("signoz_list_notification_channels", map[string]any{})
		result, err := h.handleListNotificationChannels(testCtx(), req)
		if err != nil {
			t.Fatalf("body=%s unexpected error: %v", body, err)
		}
		if result.IsError {
			t.Fatalf("body=%s expected empty page, got error result: %v", body, result.Content)
		}
		text := textContent(t, result)
		var resp map[string]any
		if err := json.Unmarshal([]byte(text), &resp); err != nil {
			t.Fatalf("body=%s response not JSON: %v", body, err)
		}
		data, ok := resp["data"].([]any)
		if !ok || len(data) != 0 {
			t.Fatalf("body=%s expected empty data array, got %v", body, resp["data"])
		}
	}
}

// --- N6: channel test-send failure -> success + prominent warning note ---

func TestHandleCreateNotificationChannel_TestFailWarningNote(t *testing.T) {
	mock := &client.MockClient{
		CreateNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"id":"ch-1","name":"bad-slack","type":"slack"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return errTestSend("webhook returned 403 forbidden")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "slack",
		"name":          "bad-slack",
		"slack_api_url": "https://hooks.slack.com/services/invalid",
	})

	result, err := h.handleCreateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fail OPEN: channel was created, IsError must NOT be set.
	if result.IsError {
		t.Fatal("expected success result; channel was created, IsError must not flip")
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want JSON body + warning note", len(result.Content))
	}
	note := result.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, "WARNING") || !strings.Contains(note, "webhook returned 403 forbidden") {
		t.Fatalf("expected prominent warning note carrying the test error, got %q", note)
	}
}

func TestHandleUpdateNotificationChannel_TestFailWarningNote(t *testing.T) {
	mock := &client.MockClient{
		UpdateNotificationChannelFn: func(ctx context.Context, id string, receiverJSON []byte) error {
			return nil
		},
		GetNotificationChannelFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"id":"1","name":"bad-slack","type":"slack"}}`), nil
		},
		TestNotificationChannelFn: func(ctx context.Context, receiverJSON []byte) error {
			return errTestSend("webhook returned 403 forbidden")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_notification_channel", map[string]any{
		"id":            "1",
		"type":          "slack",
		"name":          "bad-slack",
		"slack_api_url": "https://hooks.slack.com/services/invalid",
	})

	result, err := h.handleUpdateNotificationChannel(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result; channel was updated, IsError must not flip")
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want JSON body + warning note", len(result.Content))
	}
	note := result.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, "WARNING") || !strings.Contains(note, "webhook returned 403 forbidden") {
		t.Fatalf("expected prominent warning note carrying the test error, got %q", note)
	}
}

// errTestSend is a tiny error helper so the test reads cleanly.
type errTestSend string

func (e errTestSend) Error() string { return string(e) }
