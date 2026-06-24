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
	// real bool true -> hasError = true
	reqTrue, err := parseSearchTracesArgs(map[string]any{"error": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reqTrue.FilterExpression, "hasError = true") {
		t.Fatalf("expected hasError = true in filter, got %q", reqTrue.FilterExpression)
	}

	// string "false" -> hasError = false
	reqFalse, err := parseSearchTracesArgs(map[string]any{"error": "false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reqFalse.FilterExpression, "hasError = false") {
		t.Fatalf("expected hasError = false in filter, got %q", reqFalse.FilterExpression)
	}

	// absent -> no hasError clause
	reqAbsent, err := parseSearchTracesArgs(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(reqAbsent.FilterExpression, "hasError") {
		t.Fatalf("expected no hasError clause when absent, got %q", reqAbsent.FilterExpression)
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
		"schemaVersion":  "v1",
		"start":          1711123200000,
		"end":            1711130400000,
		"requestType":    "raw",
		"compositeQuery": map[string]any{"queries": []any{map[string]any{"type": "builder_query", "spec": map[string]any{"name": "A", "signal": "logs"}}}},
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

// --- N4: completeness notes ---

func TestCountQueryRangeRows(t *testing.T) {
	payload := []byte(`{"data":{"data":{"results":[{"rows":[{"data":{}},{"data":{}}]},{"rows":[{"data":{}}]}]}}}`)
	n, ok := countQueryRangeRows(payload)
	if !ok || n != 3 {
		t.Fatalf("got (n=%d ok=%v), want (3, true)", n, ok)
	}

	// not the expected shape -> fail open
	if _, ok := countQueryRangeRows([]byte(`{"data":[]}`)); ok {
		t.Fatal("expected rowsKnown=false for non-QB shape")
	}
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
