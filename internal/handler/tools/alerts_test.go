package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func listedNotificationChannels(displayNames ...string) types.NotificationChannelList {
	channels := make([]types.ListedNotificationChannel, 0, len(displayNames))
	for index, displayName := range displayNames {
		channels = append(channels, types.ListedNotificationChannel{
			ID:          fmt.Sprintf("0196634d-5d66-75c4-b778-%012x", index+1),
			Name:        fmt.Sprintf("channel-%d", index+1),
			DisplayName: displayName,
			Kind:        "slack",
			CreatedAt:   "2026-09-18T00:00:00Z",
			UpdatedAt:   "2026-09-18T00:00:00Z",
		})
	}
	return types.NotificationChannelList{Channels: channels, Total: len(channels)}
}

func TestHandleListAlertRules(t *testing.T) {
	mock := &client.MockClient{
		ListAlertRulesFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{
				"status": "success",
				"data": [
					{
						"id": "rule-1",
						"alert": "HighCPU",
						"alertType": "METRIC_BASED_ALERT",
						"ruleType": "threshold_rule",
						"state": "firing",
						"disabled": false,
						"labels": {"severity": "critical", "team": "infra"},
						"createdAt": "2026-04-01T00:00:00Z",
						"updatedAt": "2026-04-02T00:00:00Z"
					},
					{
						"id": "rule-2",
						"alert": "HighMemory",
						"alertType": "METRIC_BASED_ALERT",
						"ruleType": "threshold_rule",
						"state": "inactive",
						"disabled": false,
						"labels": {"severity": "warning"},
						"createAt": "2026-03-01T00:00:00Z",
						"updateAt": "2026-03-02T00:00:00Z"
					},
					{
						"id": "rule-3",
						"alert": "DisabledRule",
						"alertType": "LOGS_BASED_ALERT",
						"ruleType": "threshold_rule",
						"state": "disabled",
						"disabled": true
					}
				]
			}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alert_rules", map[string]any{
		"limit":  "2",
		"offset": "1",
	})

	result, err := h.handleListAlertRules(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}

	var resp struct {
		Data       []types.AlertRuleSummary `json:"data"`
		Pagination struct {
			Total      int  `json:"total"`
			Offset     int  `json:"offset"`
			Limit      int  `json:"limit"`
			HasMore    bool `json:"hasMore"`
			NextOffset int  `json:"nextOffset"`
		} `json:"pagination"`
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Pagination.Total != 3 {
		t.Fatalf("total = %d, want 3", resp.Pagination.Total)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(resp.Data))
	}
	if resp.Data[0].RuleID != "rule-2" || resp.Data[0].State != "inactive" || resp.Data[0].Severity != "warning" {
		t.Fatalf("unexpected first rule summary: %+v", resp.Data[0])
	}
	if resp.Data[0].CreatedAt != "2026-03-01T00:00:00Z" || resp.Data[0].UpdatedAt != "2026-03-02T00:00:00Z" {
		t.Fatalf("legacy timestamps were not preserved: %+v", resp.Data[0])
	}
	if !resp.Data[1].Disabled || resp.Data[1].RuleID != "rule-3" {
		t.Fatalf("unexpected second rule summary: %+v", resp.Data[1])
	}
}

func TestHandleListAlertRules_ClientError(t *testing.T) {
	mock := &client.MockClient{
		ListAlertRulesFn: func(ctx context.Context) (json.RawMessage, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alert_rules", map[string]any{})

	result, err := h.handleListAlertRules(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resultCode(t, result); got != CodeUpstreamError {
		t.Fatalf("code = %q, want %q", got, CodeUpstreamError)
	}
	text := resultText(t, result)
	if !strings.HasPrefix(text, "SigNoz API error:") || !strings.Contains(text, "connection refused") {
		t.Fatalf("error text = %q, want the SigNoz API error prefix naming the cause", text)
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

func TestHandleGetAlertHistory_ExplicitStartEndOverrideTimeRange(t *testing.T) {
	var capturedReq types.AlertHistoryRequest
	mock := &client.MockClient{
		GetAlertHistoryFn: func(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			capturedReq = req
			return json.RawMessage(`{"data":{"items":[]}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    "rule-hist",
		"timeRange": "1h",
		"start":     "1711123200000",
		"end":       "1711130400000",
	})

	result, err := h.handleGetAlertHistory(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedReq.Start != 1711123200000 {
		t.Fatalf("start = %d, want explicit start", capturedReq.Start)
	}
	if capturedReq.End != 1711130400000 {
		t.Fatalf("end = %d, want explicit end", capturedReq.End)
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
	if got := resultCode(t, result); got != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
	}
	if text := resultText(t, result); !strings.Contains(text, `Invalid "order" value: "invalid"`) {
		t.Fatalf("error text = %q, want it to name the invalid order value", text)
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
	if got := resultCode(t, result); got != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
	}
	if text := resultText(t, result); !strings.Contains(text, `Invalid "state" value: "invalid"`) {
		t.Fatalf("error text = %q, want it to name the invalid state value", text)
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
		ListNotificationChannelsV2Fn: func(ctx context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
			return listedNotificationChannels("slack-alerts"), nil
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
	assertForwardedMetricAlertBounds(t, parsed)
}

func TestHandleCreateAlert_StripsSearchContext(t *testing.T) {
	var capturedJSON []byte
	mock := &client.MockClient{
		ListNotificationChannelsV2Fn: func(ctx context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
			return listedNotificationChannels("slack-alerts"), nil
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
	if got := resultCode(t, result); got != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
	}
	if text := resultText(t, result); !strings.Contains(text, "empty or improperly formatted") {
		t.Fatalf("error text = %q, want the canonical empty-configuration message", text)
	}
}

func TestHandleCreateAlert_ForbiddenClientError(t *testing.T) {
	mock := &client.MockClient{
		ListNotificationChannelsV2Fn: func(ctx context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
			return listedNotificationChannels("slack-alerts"), nil
		},
		CreateAlertRuleFn: func(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
			return nil, &client.HTTPStatusError{
				StatusCode: http.StatusForbidden,
				Body:       `{"status":"error","error":{"type":"forbidden","code":"authz_forbidden","message":"only editors/admins can access this resource","errors":[],"suggestions":[]}}`,
			}
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_alert", validThresholdAlertArgs())

	result, err := h.handleCreateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result when upstream returns 403")
	}
	if code := resultCode(t, result); code != CodePermissionDenied {
		t.Fatalf("code = %q, want %q", code, CodePermissionDenied)
	}
	structured := resultStructuredMap(t, result)
	if got := structured["status"]; got != http.StatusForbidden {
		t.Fatalf("status = %v, want %d", got, http.StatusForbidden)
	}
	if got := structured["upstreamCode"]; got != "authz_forbidden" {
		t.Fatalf("upstreamCode = %v, want authz_forbidden", got)
	}
	if got := structured["upstreamMessage"]; got != "only editors/admins can access this resource" {
		t.Fatalf("upstreamMessage = %v, want backend message", got)
	}
	if text := textContent(t, result); !strings.Contains(text, "only editors/admins can access this resource") {
		t.Fatalf("error text should preserve backend message, got %q", text)
	}
}

func validThresholdAlertArgs() map[string]any {
	return map[string]any{
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
	}
}

func policyRoutedThresholdAlertArgs() map[string]any {
	args := validThresholdAlertArgs()
	condition := args["condition"].(map[string]any)
	thresholds := condition["thresholds"].(map[string]any)
	spec := thresholds["spec"].([]any)[0].(map[string]any)
	delete(spec, "channels")
	args["notificationSettings"] = map[string]any{"usePolicy": true}
	return args
}

func validAnomalyAlertArgs() map[string]any {
	return map[string]any{
		"alert":      "Anomalous ingest drop",
		"alertType":  "METRIC_BASED_ALERT",
		"ruleType":   "anomaly_rule",
		"evalWindow": "24h",
		"frequency":  "3h",
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
								map[string]any{"metricName": "otelcol_receiver_accepted_spans", "timeAggregation": "rate", "spaceAggregation": "sum"},
							},
							"functions": []any{
								map[string]any{"name": "anomaly", "args": []any{
									map[string]any{"name": "z_score_threshold", "value": 2},
								}},
							},
						},
					},
				},
			},
			"op":          "below",
			"matchType":   "all_the_times",
			"target":      float64(2),
			"algorithm":   "standard",
			"seasonality": "daily",
		},
	}
}

func TestHandleCreateAlert_AnomalyForwardsPreferredChannels(t *testing.T) {
	args := validAnomalyAlertArgs()
	args["preferredChannels"] = []any{"slack-alerts"}
	var capturedAlert []byte
	mock := &client.MockClient{
		CreateAlertRuleFn: func(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
			capturedAlert = alertJSON
			return json.RawMessage(`{"status":"success","data":{"id":"rule-anomaly"}}`), nil
		},
	}
	h := newTestHandler(mock)

	result, err := h.handleCreateAlert(testCtx(), makeToolRequest("signoz_create_alert", args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	var payload map[string]any
	if err := json.Unmarshal(capturedAlert, &payload); err != nil {
		t.Fatalf("decode created alert: %v", err)
	}
	if payload["ruleType"] != "anomaly_rule" {
		t.Fatalf("created ruleType = %v, want anomaly_rule", payload["ruleType"])
	}
	preferred, _ := payload["preferredChannels"].([]any)
	if len(preferred) != 1 || preferred[0] != "slack-alerts" {
		t.Fatalf("created preferredChannels = %v, want [slack-alerts]", payload["preferredChannels"])
	}
	if text := textContent(t, result); !strings.Contains(text, "rule-anomaly") {
		t.Fatalf("result text = %q, want it to carry the created rule id", text)
	}
}

// --- Update alert tests ---

const validRuleUUIDv7 = "0196634d-5d66-75c4-b778-e317f49dab7a"

func TestHandleUpdateAlert(t *testing.T) {
	var capturedID string
	var capturedJSON []byte
	mock := &client.MockClient{
		ListNotificationChannelsV2Fn: func(ctx context.Context, params types.NotificationChannelListParams) (types.NotificationChannelList, error) {
			return listedNotificationChannels("slack-alerts"), nil
		},
		UpdateAlertRuleFn: func(ctx context.Context, ruleID string, alertJSON []byte) error {
			capturedID = ruleID
			capturedJSON = alertJSON
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_alert", map[string]any{
		"ruleId":    validRuleUUIDv7,
		"alert":     "Updated Alert",
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
						"name":      "critical",
						"target":    float64(200),
						"op":        "1",
						"matchType": "1",
						"channels":  []any{"slack-alerts"},
					},
				},
			},
		},
	})

	result, err := h.handleUpdateAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedID != validRuleUUIDv7 {
		t.Errorf("expected ruleId=%s, got %s", validRuleUUIDv7, capturedID)
	}
	var parsed map[string]any
	if err := json.Unmarshal(capturedJSON, &parsed); err != nil {
		t.Fatalf("failed to parse captured JSON: %v", err)
	}
	if _, present := parsed["ruleId"]; present {
		t.Error("ruleId should be stripped from the rule body before sending")
	}
	assertForwardedMetricAlertBounds(t, parsed)
}

func assertForwardedMetricAlertBounds(t *testing.T, rule map[string]any) {
	t.Helper()
	spec := rule["condition"].(map[string]any)["compositeQuery"].(map[string]any)["queries"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	if spec["limit"] != float64(types.DefaultAggregateQueryLimit) {
		t.Fatalf("forwarded alert limit = %v, want %d", spec["limit"], types.DefaultAggregateQueryLimit)
	}
	order, ok := spec["order"].([]any)
	if !ok || len(order) != 1 {
		t.Fatalf("forwarded alert order = %v, want one entry", spec["order"])
	}
	entry := order[0].(map[string]any)
	if entry["direction"] != "desc" || entry["key"].(map[string]any)["name"] != "__result" {
		t.Fatalf("forwarded alert order = %v, want __result desc", order)
	}
}

// --- Delete alert tests ---

func TestHandleDeleteAlert(t *testing.T) {
	var capturedID string
	mock := &client.MockClient{
		DeleteAlertRuleFn: func(ctx context.Context, ruleID string) error {
			capturedID = ruleID
			return nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_delete_alert", map[string]any{
		"ruleId": validRuleUUIDv7,
	})

	result, err := h.handleDeleteAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if capturedID != validRuleUUIDv7 {
		t.Errorf("expected DELETE ruleId=%s, got %s", validRuleUUIDv7, capturedID)
	}
}

func TestHandleDeleteAlert_ClientError(t *testing.T) {
	mock := &client.MockClient{
		DeleteAlertRuleFn: func(ctx context.Context, ruleID string) error {
			return fmt.Errorf("rule not found")
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_delete_alert", map[string]any{
		"ruleId": validRuleUUIDv7,
	})

	result, err := h.handleDeleteAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resultCode(t, result); got != CodeUpstreamError {
		t.Fatalf("code = %q, want %q", got, CodeUpstreamError)
	}
	if text := resultText(t, result); !strings.Contains(text, "rule not found") {
		t.Fatalf("error text = %q, want it to carry the upstream cause", text)
	}
}

func TestHandleListAlerts_AddsWebURL(t *testing.T) {
	mock := &client.MockClient{
		ListAlertsFn: func(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[{"labels":{"alertname":"High CPU","ruleId":"rule-123","severity":"critical"},"status":{"state":"firing"},"startsAt":"2026-06-10T00:00:00Z","endsAt":"0001-01-01T00:00:00Z"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alerts", map[string]any{})
	result, err := h.handleListAlerts(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	body := textContent(t, result)
	if !strings.Contains(body, "/alerts/overview?ruleId=rule-123") {
		t.Fatalf("expected alert webUrl in list_alerts output, got: %s", body)
	}
}

func TestHandleListAlertRules_AddsWebURL(t *testing.T) {
	mock := &client.MockClient{
		ListAlertRulesFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"id":"rule-123","alert":"High CPU","state":"inactive","alertType":"METRIC_BASED_ALERT","ruleType":"threshold_rule","disabled":false}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alert_rules", map[string]any{})
	result, err := h.handleListAlertRules(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result")
	}
	body := textContent(t, result)
	if !strings.Contains(body, "rule-123") || !strings.Contains(body, "/alerts/overview?ruleId=rule-123") {
		t.Fatalf("expected alert webUrl, got: %s", body)
	}
}

func TestHandleListAlertRules_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		ListAlertRulesFn: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"id":"rule-123","alert":"High CPU","state":"inactive","alertType":"METRIC_BASED_ALERT","ruleType":"threshold_rule","disabled":false}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_alert_rules", map[string]any{})
	result, err := h.handleListAlertRules(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}

func TestHandleGetAlert_WrappedBodyGetsWebURL(t *testing.T) {
	mock := &client.MockClient{
		GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"id":"rule-123","alert":"High CPU"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{"ruleId": "rule-123"})
	result, err := h.handleGetAlert(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	inner, ok := obj["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected wrapped data object, got: %s", body)
	}
	if inner["webUrl"] != "https://signoz.example.com/alerts/overview?ruleId=rule-123" {
		t.Fatalf("expected webUrl on inner object, got: %v", inner["webUrl"])
	}
}

func TestHandleGetAlert_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		GetAlertByRuleIDFn: func(ctx context.Context, ruleID string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"id":"rule-123"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_alert", map[string]any{"ruleId": "rule-123"})
	result, err := h.handleGetAlert(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}
