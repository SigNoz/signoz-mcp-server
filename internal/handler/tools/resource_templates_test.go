package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func alertSummaryRequest() mcp.ReadResourceRequest {
	return mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "signoz://alert/rule-1/summary"},
	}
}

func alertSummaryJSON(t *testing.T, contents []mcp.ResourceContents) map[string]json.RawMessage {
	t.Helper()
	if len(contents) != 1 {
		t.Fatalf("got %d resource contents, want 1", len(contents))
	}
	content, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("content type = %T, want mcp.TextResourceContents", contents[0])
	}
	if content.MIMEType != "application/json" {
		t.Fatalf("MIME type = %q, want application/json", content.MIMEType)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content.Text), &payload); err != nil {
		t.Fatalf("unmarshal resource: %v", err)
	}
	return payload
}

func TestHandleAlertSummaryResourceIncludesBoundedHistoryMetadata(t *testing.T) {
	var gotHistoryReq types.AlertHistoryRequest
	mock := &signozclient.MockClient{
		GetAlertByRuleIDFn: func(_ context.Context, ruleID string) (json.RawMessage, error) {
			if ruleID != "rule-1" {
				t.Fatalf("rule ID = %q, want rule-1", ruleID)
			}
			return json.RawMessage(`{"id":"rule-1"}`), nil
		},
		GetAlertHistoryFn: func(_ context.Context, _ string, req types.AlertHistoryRequest) (json.RawMessage, error) {
			gotHistoryReq = req
			return json.RawMessage(`{"data":[{"status":"firing"}]}`), nil
		},
	}

	contents, err := newTestHandler(mock).handleAlertSummaryResource(testCtx(), alertSummaryRequest())
	if err != nil {
		t.Fatalf("handle alert summary: %v", err)
	}
	payload := alertSummaryJSON(t, contents)

	if gotHistoryReq.Limit != 10 || gotHistoryReq.Order != "desc" {
		t.Fatalf("history request = %+v, want limit 10 and descending order", gotHistoryReq)
	}
	const sixHoursMillis = int64(6 * 60 * 60 * 1000)
	if gotHistoryReq.End-gotHistoryReq.Start != sixHoursMillis {
		t.Fatalf("history window = %d ms, want %d", gotHistoryReq.End-gotHistoryReq.Start, sixHoursMillis)
	}
	if string(payload["historyAvailable"]) != "true" {
		t.Fatalf("historyAvailable = %s, want true", payload["historyAvailable"])
	}
	if _, ok := payload["recentHistory"]; !ok {
		t.Fatal("recentHistory missing from successful summary")
	}
	if _, ok := payload["asOf"]; !ok {
		t.Fatal("asOf missing from summary")
	}
	if _, ok := payload["historyWindow"]; !ok {
		t.Fatal("historyWindow missing from summary")
	}
	if _, ok := payload["warnings"]; ok {
		t.Fatal("warnings present on successful history fetch")
	}
}

func TestHandleAlertSummaryResourceReportsDegradedHistory(t *testing.T) {
	mock := &signozclient.MockClient{
		GetAlertByRuleIDFn: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"rule-1"}`), nil
		},
		GetAlertHistoryFn: func(context.Context, string, types.AlertHistoryRequest) (json.RawMessage, error) {
			return nil, errors.New("history temporarily unavailable")
		},
	}

	contents, err := newTestHandler(mock).handleAlertSummaryResource(testCtx(), alertSummaryRequest())
	if err != nil {
		t.Fatalf("handle alert summary: %v", err)
	}
	payload := alertSummaryJSON(t, contents)
	if string(payload["historyAvailable"]) != "false" {
		t.Fatalf("historyAvailable = %s, want false", payload["historyAvailable"])
	}
	if _, ok := payload["recentHistory"]; ok {
		t.Fatal("recentHistory present after failed history fetch")
	}
	if !strings.Contains(string(payload["warnings"]), "history is unavailable") {
		t.Fatalf("warnings = %s, want corrective history warning", payload["warnings"])
	}
}

func TestHandleAlertSummaryResourcePropagatesHistoryAuthorizationErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mock := &signozclient.MockClient{
				GetAlertByRuleIDFn: func(context.Context, string) (json.RawMessage, error) {
					return json.RawMessage(`{"id":"rule-1"}`), nil
				},
				GetAlertHistoryFn: func(context.Context, string, types.AlertHistoryRequest) (json.RawMessage, error) {
					return nil, &signozclient.HTTPStatusError{StatusCode: status, Body: `{}`}
				},
			}

			contents, err := newTestHandler(mock).handleAlertSummaryResource(testCtx(), alertSummaryRequest())
			if err == nil {
				t.Fatal("expected authorization error")
			}
			if contents != nil {
				t.Fatalf("contents = %#v, want nil", contents)
			}
			var statusErr *signozclient.HTTPStatusError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
				t.Fatalf("error = %v, want preserved HTTP status %d", err, status)
			}
		})
	}
}

func TestHandleDashboardSummaryResourceReturnsFullDefinition(t *testing.T) {
	want := json.RawMessage(`{"data":{"title":"Checkout RED","widgets":[{"title":"Request rate"}],"variables":{"service":{"type":"DYNAMIC"}}}}`)
	mock := &signozclient.MockClient{
		GetDashboardFn: func(_ context.Context, id string) (json.RawMessage, error) {
			if id != "dashboard-1" {
				t.Fatalf("dashboard ID = %q, want dashboard-1", id)
			}
			return want, nil
		},
	}
	req := mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{URI: "signoz://dashboard/dashboard-1/summary"},
	}

	contents, err := newTestHandler(mock).handleDashboardSummaryResource(testCtx(), req)
	if err != nil {
		t.Fatalf("handle dashboard definition: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("got %d resource contents, want 1", len(contents))
	}
	content, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("content type = %T, want mcp.TextResourceContents", contents[0])
	}
	if content.URI != req.Params.URI || content.MIMEType != "application/json" || content.Text != string(want) {
		t.Fatalf("dashboard resource = %#v, want unchanged full definition", content)
	}
}
