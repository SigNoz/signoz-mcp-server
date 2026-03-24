package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

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
