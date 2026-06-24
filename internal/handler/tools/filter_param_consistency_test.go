package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func TestParseFilterExpressionParam_AcceptsFilterAndLegacyQuery(t *testing.T) {
	type parserCase struct {
		name  string
		parse func(map[string]any) (string, error)
		base  map[string]any
	}

	parsers := []parserCase{
		{
			name: "search_logs",
			parse: func(args map[string]any) (string, error) {
				req, err := parseSearchLogsArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"timeRange": "1h"},
		},
		{
			name: "search_traces",
			parse: func(args map[string]any) (string, error) {
				req, err := parseSearchTracesArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"timeRange": "1h"},
		},
		{
			name: "aggregate_logs",
			parse: func(args map[string]any) (string, error) {
				req, err := parseAggregateLogsArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"aggregation": "count", "timeRange": "1h"},
		},
		{
			name: "aggregate_traces",
			parse: func(args map[string]any) (string, error) {
				req, err := parseAggregateTracesArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"aggregation": "count", "timeRange": "1h"},
		},
		{
			name: "query_metrics",
			parse: func(args map[string]any) (string, error) {
				req, err := parseMetricsQueryArgs(args)
				if err != nil {
					return "", err
				}
				return req.Filter, nil
			},
			base: map[string]any{"metricName": "system.cpu.time"},
		},
	}

	tests := []struct {
		name    string
		args    map[string]any
		want    string
		wantErr bool
	}{
		{
			name: "filter only",
			args: map[string]any{"filter": " service.name = 'checkout' "},
			want: " service.name = 'checkout' ",
		},
		{
			name: "legacy query only",
			args: map[string]any{"query": "service.name = 'checkout'"},
			want: "service.name = 'checkout'",
		},
		{
			name: "both equal after trimming prefer raw filter",
			args: map[string]any{
				"filter": " service.name = 'checkout' ",
				"query":  "service.name = 'checkout'",
			},
			want: " service.name = 'checkout' ",
		},
		{
			name: "both differ",
			args: map[string]any{
				"filter": "service.name = 'checkout'",
				"query":  "service.name = 'frontend'",
			},
			wantErr: true,
		},
	}

	for _, parser := range parsers {
		for _, tt := range tests {
			t.Run(parser.name+"/"+tt.name, func(t *testing.T) {
				args := mergeArgs(parser.base, tt.args)
				got, err := parser.parse(args)
				if tt.wantErr {
					if err == nil {
						t.Fatal("expected error")
					}
					if err.Error() != conflictingFilterAliasError {
						t.Fatalf("error = %q, want %q", err.Error(), conflictingFilterAliasError)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Fatalf("filter expression = %q, want %q", got, tt.want)
				}
			})
		}
	}
}

func TestExtractBackendWarningMessages(t *testing.T) {
	tests := []struct {
		name string
		body json.RawMessage
		want []string
	}{
		{
			name: "malformed body",
			body: json.RawMessage(`{not json`),
		},
		{
			name: "valid success without warning field",
			body: json.RawMessage(`{"status":"success","data":{"data":{"results":[]}}}`),
		},
		{
			name: "blank warning dropped",
			body: json.RawMessage(`{"status":"success","data":{"warning":{"warnings":[{"message":"   "},{"message":"keep me"}]}}}`),
			want: []string{"keep me"},
		},
		{
			name: "multiple warnings preserve order",
			body: json.RawMessage(`{"status":"success","data":{"warning":{"warnings":[{"message":"first"},{"message":"second"}]}}}`),
			want: []string{"first", "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBackendWarningMessages(tt.body)
			if tt.want == nil && got != nil {
				t.Fatalf("warnings = %#v, want nil", got)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("warning count = %d (%#v), want %d (%#v)", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("warning[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHandleSearchLogs_FilterParamReachesPayload(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", map[string]any{
		"filter":    "service.name='x'",
		"timeRange": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if got := payloadFilterExpression(t, captured); got != "service.name='x'" {
		t.Fatalf("payload filter = %q, want service.name='x'", got)
	}
}

func TestHandleAggregateLogs_LegacyQueryParamReachesPayload(t *testing.T) {
	var captured []byte
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			captured = body
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleAggregateLogs(testCtx(), makeToolRequest("signoz_aggregate_logs", map[string]any{
		"aggregation": "count",
		"query":       "service.name='x'",
		"timeRange":   "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if got := payloadFilterExpression(t, captured); got != "service.name='x'" {
		t.Fatalf("payload filter = %q, want service.name='x'", got)
	}
}

func TestFilterAliasConflict_HandlerErrorsAllFilterTools(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		handler func(*Handler, context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{
			name: "search_logs",
			args: map[string]any{"timeRange": "1h"},
			handler: func(h *Handler, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleSearchLogs(ctx, req)
			},
		},
		{
			name: "search_traces",
			args: map[string]any{"timeRange": "1h"},
			handler: func(h *Handler, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleSearchTraces(ctx, req)
			},
		},
		{
			name: "aggregate_logs",
			args: map[string]any{"aggregation": "count", "timeRange": "1h"},
			handler: func(h *Handler, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleAggregateLogs(ctx, req)
			},
		},
		{
			name: "aggregate_traces",
			args: map[string]any{"aggregation": "count", "timeRange": "1h"},
			handler: func(h *Handler, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleAggregateTraces(ctx, req)
			},
		},
		{
			name: "query_metrics",
			args: map[string]any{"metricName": "system.cpu.time", "metricType": "gauge", "timeRange": "1h"},
			handler: func(h *Handler, ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.handleQueryMetrics(ctx, req)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(&client.MockClient{})
			args := mergeArgs(tt.args, map[string]any{
				"filter": "service.name = 'checkout'",
				"query":  "service.name = 'frontend'",
			})
			result, err := tt.handler(h, testCtx(), makeToolRequest("signoz_"+tt.name, args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected error result")
			}
			body := noteText(t, result, 0)
			if !strings.Contains(body, conflictingFilterAliasError) {
				t.Fatalf("error body = %q, want conflict message", body)
			}
		})
	}
}

func TestFilterAlias_FalseFriendHandlersUnaffected(t *testing.T) {
	t.Run("list_alerts_filter_remains_alertmanager_matcher", func(t *testing.T) {
		var captured types.ListAlertsParams
		mock := &client.MockClient{
			ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
				captured = params
				return json.RawMessage(`{"status":"success","data":[]}`), nil
			},
		}
		h := newTestHandler(mock)
		result, err := h.handleListAlerts(testCtx(), makeToolRequest("signoz_list_alerts", map[string]any{
			"filter": `alertname="HighCPU",severity="critical"`,
			"query":  "service.name = 'checkout'",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("handler returned error result: %v", result.Content)
		}
		if len(captured.Filter) != 2 {
			t.Fatalf("captured filters = %#v, want 2 matcher filters", captured.Filter)
		}
		if captured.Filter[0] != `alertname="HighCPU"` || captured.Filter[1] != `severity="critical"` {
			t.Fatalf("captured filters = %#v, want Alertmanager matchers", captured.Filter)
		}
	})

	t.Run("execute_builder_query_still_requires_query_object", func(t *testing.T) {
		h := newTestHandler(&client.MockClient{})
		result, err := h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{
			"filter": "service.name = 'checkout'",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result")
		}
		body := noteText(t, result, 0)
		if !strings.Contains(body, `Parameter validation failed: "query" must be a JSON object`) {
			t.Fatalf("error body = %q, want query-object validation", body)
		}
	})

	t.Run("query_metrics_formula_query_filters_remain_nested", func(t *testing.T) {
		var captured []byte
		mock := &client.MockClient{
			QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
				captured = body
				return json.RawMessage(`{"status":"success","data":{"data":{"results":[]}}}`), nil
			},
		}
		h := newTestHandler(mock)
		result, err := h.handleQueryMetrics(testCtx(), makeToolRequest("signoz_query_metrics", map[string]any{
			"metricName":  "system.cpu.time",
			"metricType":  "gauge",
			"filter":      "service.name = 'frontend'",
			"timeRange":   "1h",
			"requestType": "time_series",
			"formulaQueries": []any{map[string]any{
				"name":       "B",
				"metricName": "system.memory.usage",
				"metricType": "gauge",
				"filter":     "attribute.instance = 'i-123'",
			}},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("handler returned error result: %v", result.Content)
		}
		filters := payloadFilterExpressionsByQueryName(t, captured)
		if filters["A"] != "service.name = 'frontend'" {
			t.Fatalf("primary query filter = %q, want top-level filter", filters["A"])
		}
		if filters["B"] != "attribute.instance = 'i-123'" {
			t.Fatalf("formula sub-query filter = %q, want nested formulaQueries filter", filters["B"])
		}
		if filters["B"] == filters["A"] {
			t.Fatalf("top-level filter leaked into formula sub-query: %#v", filters)
		}
	})

	t.Run("create_view_filter_args_do_not_mutate_nested_composite_query", func(t *testing.T) {
		var gotBody []byte
		mock := &client.MockClient{
			CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
				gotBody = body
				return json.RawMessage(`{"status":"success","data":{"id":"v1"}}`), nil
			},
		}
		h := newTestHandler(mock)
		nestedFilter := "severity_text = 'ERROR'"
		result, err := h.handleCreateView(testCtx(), makeToolRequest("signoz_create_view", map[string]any{
			"name":       "logs errors",
			"sourcePage": "logs",
			"filter":     "service.name = 'frontend'",
			"query":      "service.name = 'legacy'",
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "list",
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{
						"name":   "A",
						"signal": "logs",
						"filter": map[string]any{"expression": nestedFilter},
					},
				}},
			},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("handler returned error result: %v", result.Content)
		}
		if got := savedViewNestedFilterExpression(t, gotBody); got != nestedFilter {
			t.Fatalf("nested compositeQuery filter = %q, want %q", got, nestedFilter)
		}
	})
}

func TestBackendWarnings_SurfaceInToolResultAndWarnLog(t *testing.T) {
	warningMessage := "Key http.status_code is ambiguous; using resource context"
	response := json.RawMessage(`{"status":"success","data":{"warning":{"warnings":[{"message":"` + warningMessage + `"}]},"data":{"results":[]}}}`)
	var logs bytes.Buffer
	mock := &client.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return response, nil
		},
	}
	h := newTestHandler(mock)
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	result, err := h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", map[string]any{
		"filter":    "service.name = 'checkout'",
		"timeRange": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content block count = %d, want raw JSON + warning note", len(result.Content))
	}
	if block0 := noteText(t, result, 0); block0 != string(response) {
		t.Fatalf("block 0 = %q, want raw response unchanged", block0)
	}
	if note := noteText(t, result, 1); !strings.Contains(note, warningMessage) {
		t.Fatalf("warning note = %q, want backend warning message", note)
	}
	if gotLogs := logs.String(); !strings.Contains(gotLogs, "SigNoz query builder returned non-fatal warnings") || !strings.Contains(gotLogs, "warningCount=1") {
		t.Fatalf("expected WARN log with warningCount, got %q", gotLogs)
	}
}

func TestBackendWarnings_ComposeWithClampNote(t *testing.T) {
	payload := []byte(`{"status":"success","data":{"warning":{"warnings":[{"message":"ambiguous key"}]},"data":{"results":[]}}}`)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	result := rawSearchResult(testCtx(), logger, "signoz_search_logs", payload, true)
	if len(result.Content) != 3 {
		t.Fatalf("content block count = %d, want raw JSON + clamp note + warning note", len(result.Content))
	}
	if block0 := noteText(t, result, 0); block0 != string(payload) {
		t.Fatalf("block 0 = %q, want raw response unchanged", block0)
	}
	if clampNote := noteText(t, result, 1); !strings.Contains(clampNote, "result limited to") {
		t.Fatalf("block 1 = %q, want clamp note", clampNote)
	}
	if warningNote := noteText(t, result, 2); !strings.Contains(warningNote, "ambiguous key") {
		t.Fatalf("block 2 = %q, want warning note", warningNote)
	}
}

func mergeArgs(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

func payloadFilterExpression(t *testing.T, payload []byte) string {
	t.Helper()
	if len(payload) == 0 {
		t.Fatal("payload was not captured")
	}
	var decoded struct {
		CompositeQuery struct {
			Queries []struct {
				Spec struct {
					Filter struct {
						Expression string `json:"expression"`
					} `json:"filter"`
				} `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(decoded.CompositeQuery.Queries) != 1 {
		t.Fatalf("query count = %d, want 1", len(decoded.CompositeQuery.Queries))
	}
	return decoded.CompositeQuery.Queries[0].Spec.Filter.Expression
}

func payloadFilterExpressionsByQueryName(t *testing.T, payload []byte) map[string]string {
	t.Helper()
	if len(payload) == 0 {
		t.Fatal("payload was not captured")
	}
	var decoded struct {
		CompositeQuery struct {
			Queries []struct {
				Type string `json:"type"`
				Spec struct {
					Name   string `json:"name"`
					Filter *struct {
						Expression string `json:"expression"`
					} `json:"filter"`
				} `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	filters := map[string]string{}
	for _, q := range decoded.CompositeQuery.Queries {
		if q.Type != "builder_query" || q.Spec.Filter == nil {
			continue
		}
		filters[q.Spec.Name] = q.Spec.Filter.Expression
	}
	return filters
}

func savedViewNestedFilterExpression(t *testing.T, body []byte) string {
	t.Helper()
	if len(body) == 0 {
		t.Fatal("view body was not captured")
	}
	var decoded struct {
		CompositeQuery struct {
			Queries []struct {
				Spec struct {
					Filter struct {
						Expression string `json:"expression"`
					} `json:"filter"`
				} `json:"spec"`
			} `json:"queries"`
		} `json:"compositeQuery"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal view body: %v", err)
	}
	if len(decoded.CompositeQuery.Queries) != 1 {
		t.Fatalf("view query count = %d, want 1", len(decoded.CompositeQuery.Queries))
	}
	return decoded.CompositeQuery.Queries[0].Spec.Filter.Expression
}

func noteText(t *testing.T, result *mcp.CallToolResult, index int) string {
	t.Helper()
	if len(result.Content) <= index {
		t.Fatalf("result has %d content blocks, want index %d", len(result.Content), index)
	}
	text, ok := mcp.AsTextContent(result.Content[index])
	if !ok {
		t.Fatalf("content block %d is %T, want text", index, result.Content[index])
	}
	return text.Text
}
