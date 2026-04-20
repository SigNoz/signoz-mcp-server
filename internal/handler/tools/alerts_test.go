package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func TestHandleListAlerts(t *testing.T) {
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			return json.RawMessage(`{
				"status": "success",
				"data": [
					{
						"labels": {"alertname": "HighCPU", "ruleId": "rule-1", "severity": "critical"},
						"startsAt": "2025-01-01T00:00:00Z",
						"endsAt": "2025-01-01T01:00:00Z",
						"status": {"state": "firing"}
					},
					{
						"labels": {"alertname": "HighMemory", "ruleId": "rule-2", "severity": "warning"},
						"startsAt": "2025-01-01T02:00:00Z",
						"endsAt": "2025-01-01T03:00:00Z",
						"status": {"state": "resolved"}
					}
				]
			}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{})

	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
}

func TestHandleListAlerts_WithPagination(t *testing.T) {
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			return json.RawMessage(`{
				"status": "success",
				"data": [
					{"labels": {"alertname": "A1", "ruleId": "1", "severity": "critical"}, "startsAt": "", "endsAt": "", "status": {"state": "firing"}},
					{"labels": {"alertname": "A2", "ruleId": "2", "severity": "critical"}, "startsAt": "", "endsAt": "", "status": {"state": "firing"}},
					{"labels": {"alertname": "A3", "ruleId": "3", "severity": "critical"}, "startsAt": "", "endsAt": "", "status": {"state": "firing"}}
				]
			}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{
		"limit":  "2",
		"offset": "0",
	})

	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
}

func TestHandleListAlerts_ClientError(t *testing.T) {
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{})

	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when client returns error")
	}
}

func TestHandleGetAlert(t *testing.T) {
	var capturedRuleID string
	mock := &client.MockClient{
		GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
			capturedRuleID = ruleID
			return json.RawMessage(`{"data":{"id":"rule-abc","name":"HighCPU"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{
		"ruleId": "rule-abc",
	})

	result, err := h.handleGetAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedRuleID != "rule-abc" {
		t.Errorf("expected ruleId=rule-abc, got %q", capturedRuleID)
	}
}

func TestHandleGetAlert_EmptyRuleId(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{
		"ruleId": "",
	})

	result, err := h.handleGetAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty ruleId")
	}
}

func TestHandleGetAlert_MissingRuleId(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{})

	result, err := h.handleGetAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing ruleId")
	}
}

func TestHandleGetAlert_ClientError(t *testing.T) {
	mock := &client.MockClient{
		GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
			return nil, fmt.Errorf("not found")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{
		"ruleId": "rule-xyz",
	})

	result, err := h.handleGetAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when client returns error")
	}
}

func TestHandleGetAlertHistory(t *testing.T) {
	var capturedRuleID string
	var capturedReq types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			capturedRuleID = ruleID
			capturedReq = req
			return json.RawMessage(`{"data":{"items":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-hist",
		"timeRange": "24h",
		"limit":     "50",
		"order":     "desc",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedRuleID != "rule-hist" {
		t.Errorf("expected ruleId=rule-hist, got %q", capturedRuleID)
	}
	if capturedReq.Limit != 50 {
		t.Errorf("expected limit=50, got %d", capturedReq.Limit)
	}
	if capturedReq.Order != "desc" {
		t.Errorf("expected order=desc, got %q", capturedReq.Order)
	}
}

func TestHandleGetAlertHistory_EmptyRuleId(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "",
		"timeRange": "1h",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty ruleId")
	}
}

func TestHandleGetAlertHistory_InvalidOrder(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-1",
		"timeRange": "1h",
		"order":     "invalid",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid order value")
	}
}

func TestHandleGetAlertHistory_WithStateFilter(t *testing.T) {
	var capturedReq types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			capturedReq = req
			return json.RawMessage(`{"data":{"items":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-1",
		"timeRange": "1h",
		"state":     "firing",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if capturedReq.State != "firing" {
		t.Errorf("expected state=firing, got %q", capturedReq.State)
	}
}

func TestHandleGetAlertHistory_InvalidState(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-1",
		"timeRange": "1h",
		"state":     "invalid",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid state value")
	}
}

func TestHandleGetAlertHistory_StateOmitted(t *testing.T) {
	var capturedReq types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			capturedReq = req
			return json.RawMessage(`{"data":{"items":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-1",
		"timeRange": "1h",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if capturedReq.State != "" {
		t.Errorf("expected state to be empty when omitted, got %q", capturedReq.State)
	}
}

func TestHandleListAlerts_WithFilterParams(t *testing.T) {
	var capturedParams types.ListAlertsParams
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			capturedParams = params
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{
		"active":   "false",
		"silenced": "true",
		"filter":   `alertname="HighCPU",severity="critical"`,
		"receiver": "slack-.*",
	})

	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if capturedParams.Active == nil || *capturedParams.Active != false {
		t.Errorf("expected active=false, got %v", capturedParams.Active)
	}
	if capturedParams.Silenced == nil || *capturedParams.Silenced != true {
		t.Errorf("expected silenced=true, got %v", capturedParams.Silenced)
	}
	if len(capturedParams.Filter) != 2 {
		t.Errorf("expected 2 filters, got %d: %v", len(capturedParams.Filter), capturedParams.Filter)
	}
	if capturedParams.Receiver != "slack-.*" {
		t.Errorf("expected receiver='slack-.*', got %q", capturedParams.Receiver)
	}
}

func TestHandleListAlerts_BoolParamNilWhenOmitted(t *testing.T) {
	var capturedParams types.ListAlertsParams
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			capturedParams = params
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{})

	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if capturedParams.Active != nil {
		t.Errorf("expected active=nil when omitted, got %v", *capturedParams.Active)
	}
	if capturedParams.Silenced != nil {
		t.Errorf("expected silenced=nil when omitted, got %v", *capturedParams.Silenced)
	}
	if capturedParams.Inhibited != nil {
		t.Errorf("expected inhibited=nil when omitted, got %v", *capturedParams.Inhibited)
	}
}

func TestHandleListAlerts_FilterSplitAndTrim(t *testing.T) {
	var capturedParams types.ListAlertsParams
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			capturedParams = params
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{
		"filter": ` alertname="A" , severity="critical" `,
	})

	result, err := h.handleListAlerts(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if len(capturedParams.Filter) != 2 {
		t.Fatalf("expected 2 filters, got %d: %v", len(capturedParams.Filter), capturedParams.Filter)
	}
	if capturedParams.Filter[0] != `alertname="A"` {
		t.Errorf("expected first filter='alertname=\"A\"', got %q", capturedParams.Filter[0])
	}
	if capturedParams.Filter[1] != `severity="critical"` {
		t.Errorf("expected second filter='severity=\"critical\"', got %q", capturedParams.Filter[1])
	}
}

func TestHandleCreateAlert(t *testing.T) {
	var capturedJSON []byte
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
		},
		CreateAlertRuleFn: func(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
			capturedJSON = alertJSON
			return json.RawMessage(`{"status":"success","data":{"id":"rule-123"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert":     "Test Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "graph",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
						"channels":  []any{"slack-alerts"},
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedJSON == nil {
		t.Fatal("expected CreateAlertRuleFn to be called")
	}

	// Verify defaults were applied in the JSON sent to the API
	var parsed map[string]any
	if err := json.Unmarshal(capturedJSON, &parsed); err != nil {
		t.Fatalf("failed to parse captured JSON: %v", err)
	}
	if parsed["version"] != "v5" {
		t.Errorf("expected version=v5, got %v", parsed["version"])
	}
	if parsed["schemaVersion"] != "v2alpha1" {
		t.Errorf("expected schemaVersion=v2alpha1, got %v", parsed["schemaVersion"])
	}
}

func TestHandleCreateAlert_StripsSearchContext(t *testing.T) {
	var capturedJSON []byte
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
		},
		CreateAlertRuleFn: func(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
			capturedJSON = alertJSON
			return json.RawMessage(`{"status":"success","data":{"id":"rule-456"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"searchContext": "user wants to create an alert for high CPU",
		"alert":         "CPU Alert",
		"alertType":     "METRIC_BASED_ALERT",
		"ruleType":      "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name": "warning", "target": float64(90),
						"op": "1", "matchType": "1",
						"channels": []any{"slack-alerts"},
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var parsed map[string]any
	if err := json.Unmarshal(capturedJSON, &parsed); err != nil {
		t.Fatalf("failed to parse captured JSON: %v", err)
	}
	if _, hasSearchContext := parsed["searchContext"]; hasSearchContext {
		t.Error("searchContext should be stripped from the API payload")
	}
}

func TestHandleCreateAlert_EmptyArgs(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty args")
	}
}

func TestHandleCreateAlert_ValidationError(t *testing.T) {
	mock := &client.MockClient{}
	h := newTestHandler(mock)
	// Missing required fields
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert": "Test Alert",
		// missing alertType, ruleType, condition
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for validation failure")
	}
}

func TestHandleCreateAlert_ClientError(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
		},
		CreateAlertRuleFn: func(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
			return nil, fmt.Errorf("unexpected status 400: bad request")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert":     "Test Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
						"channels":  []any{"slack-alerts"},
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when client returns error")
	}
}

func TestHandleCreateAlert_NoChannelsReturnsAvailable(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"},{"name":"pagerduty-oncall","type":"pagerduty"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert":     "Test Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when no channels are specified")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "slack-alerts") {
		t.Error("expected error to list available channel 'slack-alerts'")
	}
	if !strings.Contains(text, "pagerduty-oncall") {
		t.Error("expected error to list available channel 'pagerduty-oncall'")
	}
	if !strings.Contains(text, "signoz_create_notification_channel") {
		t.Error("expected error to mention signoz_create_notification_channel")
	}
}

func TestHandleCreateAlert_InvalidChannelReturnsError(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert":     "Test Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
						"channels":  []any{"nonexistent-channel"},
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when channel does not exist")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "nonexistent-channel") {
		t.Error("expected error to mention the invalid channel name")
	}
	if !strings.Contains(text, "slack-alerts") {
		t.Error("expected error to list available channels")
	}
}

func TestHandleCreateAlert_PreferredChannelsValidated(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"name":"slack-alerts","type":"slack"}]}`), nil
		},
		CreateAlertRuleFn: func(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"rule-789"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert":             "Test Alert",
		"alertType":         "METRIC_BASED_ALERT",
		"ruleType":          "threshold_rule",
		"preferredChannels": []any{"slack-alerts"},
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
}

func TestHandleCreateAlert_NoChannelsExist(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", map[string]any{
		"alert":     "Test Alert",
		"alertType": "METRIC_BASED_ALERT",
		"ruleType":  "threshold_rule",
		"condition": map[string]any{
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"queries": []any{
					map[string]any{
						"type": "builder_query",
						"spec": map[string]any{
							"name":   "A",
							"signal": "metrics",
							"aggregations": []any{
								map[string]any{"expression": "count()"},
							},
							"filter": map[string]any{"expression": ""},
						},
					},
				},
			},
			"thresholds": map[string]any{
				"kind": "basic",
				"spec": []any{
					map[string]any{
						"name":      "warning",
						"target":    float64(100),
						"op":        "1",
						"matchType": "1",
					},
				},
			},
		},
	})

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when no channels exist and none specified")
	}
	text := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(text, "No notification channels exist yet") {
		t.Error("expected error to indicate no channels exist")
	}
	if !strings.Contains(text, "signoz_create_notification_channel") {
		t.Error("expected error to suggest creating a new channel")
	}
}
