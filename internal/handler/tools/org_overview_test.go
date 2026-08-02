package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleGetOrgOverview_GroupsWorkspacePosture(t *testing.T) {
	const largeCount = uint64(9007199254740993)
	mock := &client.MockClient{
		GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{
				"status":"success",
				"data":{
					"telemetry.logs.count":123,
					"telemetry.logs.last_observed.time":"2026-08-02T10:00:00Z",
					"telemetry.logs.last_observed.time_unix":1785664800,
					"dashboard.count":9007199254740993,
					"public_dashboard.count":1,
					"dashboard.panels.count":7,
					"dashboard.panels.logs.count":2,
					"dashboard.panels.metrics.count":5,
					"dashboard.panels.traces.count":0,
					"rule.count":2,
					"rule.type.threshold.count":1,
					"rule.type.anomaly.count":1,
					"alert.type.metric.count":2,
					"alert.firing.count":1,
					"alertmanager.channel.count":1,
					"alertmanager.channel.type.slack":1,
					"savedview.count":1,
					"savedview.source.logs.count":1,
					"logs_pipeline.total.count":2,
					"logs_pipeline.enabled.count":1,
					"cloudintegration.aws.connectedaccounts.count":4,
					"cloudintegration.azure.connectedaccounts.count":0,
					"user.count":99
				}
			}`), nil
		},
	}
	h := newTestHandler(mock)

	result, err := h.handleGetOrgOverview(testCtx(), requestWithNilArguments("signoz_get_org_overview"))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	if result.StructuredContent == nil {
		t.Fatal("code-controlled overview must carry structuredContent")
	}

	var got orgOverviewOutput
	if err := json.Unmarshal([]byte(textContent(t, result)), &got); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if got.Data.Signals.Available || !strings.Contains(got.Data.Signals.Reason, "deployment-wide") {
		t.Fatalf("signals availability = %#v, want explicit deployment-wide exclusion", got.Data.Signals)
	}
	if len(got.Data.Signals.NextTools) == 0 || len(got.Data.Alerts.Runtime.NextTools) == 0 || got.Data.Alerts.Runtime.NextTools[0] != "signoz_list_alerts" {
		t.Fatalf("missing tenant-scoped runtime recovery tools: signals=%v alerts=%v", got.Data.Signals.NextTools, got.Data.Alerts.Runtime.NextTools)
	}
	if got.Data.Dashboards.Count == nil || *got.Data.Dashboards.Count != largeCount {
		t.Fatalf("dashboard count = %#v, want %d", got.Data.Dashboards.Count, largeCount)
	}
	if !got.Data.Dashboards.Available || !got.Data.Alerts.Rules.Available || !got.Data.Alerts.NotificationChannels.Available || !got.Data.Views.Available || !got.Data.LogPipelines.Available {
		t.Fatalf("reported overview groups must be available: %#v", got.Data)
	}
	if got.Data.Alerts.Rules.ByType["threshold"] != 1 || got.Data.Alerts.Rules.BySignal["metric"] != 2 {
		t.Fatalf("unexpected alert grouping: %#v", got.Data.Alerts.Rules)
	}
	if got.Data.Alerts.NotificationChannels.ByType["slack"] != 1 {
		t.Fatalf("channel types = %#v", got.Data.Alerts.NotificationChannels.ByType)
	}
	aws := got.Data.CloudIntegrations.Providers["aws"]
	azure := got.Data.CloudIntegrations.Providers["azure"]
	if got.Data.CloudIntegrations.SourceAvailability != "complete" || !aws.DataAvailable || aws.ConnectedAccounts == nil || *aws.ConnectedAccounts != 4 || !azure.DataAvailable || azure.ConnectedAccounts == nil || *azure.ConnectedAccounts != 0 {
		t.Fatalf("cloud integrations = %#v", got.Data.CloudIntegrations)
	}
	if got.Data.Metadata.ExcludedDeploymentWideStatCount != 4 {
		t.Fatalf("excludedDeploymentWideStatCount = %d, want 4", got.Data.Metadata.ExcludedDeploymentWideStatCount)
	}
	if got.Data.Metadata.Partial || len(got.Data.Metadata.IncompleteGroups) != 0 || len(got.Data.Metadata.InvalidStatFields) != 0 {
		t.Fatalf("complete organization-scoped collectors reported partial metadata: %#v", got.Data.Metadata)
	}
	if strings.Contains(textContent(t, result), "user.count") || strings.Contains(textContent(t, result), "telemetry.logs.count") {
		t.Fatal("non-observability or deployment-wide stats must not leak into the overview")
	}
	if !strings.Contains(textContent(t, result), "9007199254740993") {
		t.Fatal("large count lost integer precision")
	}
}

func TestHandleGetOrgOverview_PreservesRelevantDriftAndWarns(t *testing.T) {
	var logs bytes.Buffer
	h := newTestHandler(&client.MockClient{
		GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"dashboard.count":"three","dashboard.panels.future.count":17}}`), nil
		},
	})
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	result, err := h.handleGetOrgOverview(testCtx(), makeToolRequest("signoz_get_org_overview", map[string]any{}))
	if err != nil || result.IsError {
		t.Fatalf("overview failed: result=%#v err=%v", result, err)
	}
	var got orgOverviewOutput
	if err := json.Unmarshal([]byte(textContent(t, result)), &got); err != nil {
		t.Fatal(err)
	}
	if _, exists := got.Data.AdditionalStats["dashboard.count"]; exists {
		t.Fatalf("malformed known stat must not conflict with the owned output: %#v", got.Data.AdditionalStats)
	}
	if got.Data.AdditionalStats["dashboard.panels.future.count"] == nil {
		t.Fatalf("compatible new stat was not preserved: %#v", got.Data.AdditionalStats)
	}
	if got.Data.Metadata.AdditionalStatCount != 1 || !containsString(got.Data.Metadata.InvalidStatFields, "dashboard.count") {
		t.Fatalf("unexpected drift metadata: %#v", got.Data.Metadata)
	}
	dashboardRecovery := incompleteGroup(got.Data.Metadata.IncompleteGroups, "dashboards")
	if dashboardRecovery == nil || !containsString(dashboardRecovery.NextTools, "signoz_list_dashboards") {
		t.Fatalf("missing dashboard recovery guidance: %#v", got.Data.Metadata.IncompleteGroups)
	}
	if !strings.Contains(logs.String(), "partial or drifted") {
		t.Fatalf("missing contract-drift WARN: %s", logs.String())
	}
}

func TestBuildOrgOverview_MissingCountsStayUnknown(t *testing.T) {
	got, drift, err := buildOrgOverview([]byte(`{"status":"success","data":{"rule.count":0}}`))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if len(drift) == 0 {
		t.Fatal("missing collector sentinels must surface as response drift")
	}
	if got.Data.Alerts.Rules.Count == nil || *got.Data.Alerts.Rules.Count != 0 {
		t.Fatalf("reported zero rule count = %#v, want explicit zero", got.Data.Alerts.Rules.Count)
	}
	if !got.Data.Alerts.Rules.Available {
		t.Fatal("explicit rule count must mark the group available")
	}
	if got.Data.Dashboards.Available || got.Data.Views.Available {
		t.Fatalf("missing collector groups must be unavailable, got dashboards=%t views=%t", got.Data.Dashboards.Available, got.Data.Views.Available)
	}
	if got.Data.Dashboards.Count != nil || got.Data.Views.Count != nil {
		t.Fatalf("missing collector counts must stay unknown, got dashboards=%v views=%v", got.Data.Dashboards.Count, got.Data.Views.Count)
	}
	if got.Data.CloudIntegrations.SourceAvailability != "unavailable" {
		t.Fatalf("cloud source availability = %q, want unavailable", got.Data.CloudIntegrations.SourceAvailability)
	}
	if incompleteGroup(got.Data.Metadata.IncompleteGroups, "cloudIntegrations") != nil {
		t.Fatalf("structurally absent cloud stats must not degrade the whole snapshot: %#v", got.Data.Metadata.IncompleteGroups)
	}
	if !got.Data.Metadata.Partial || incompleteGroup(got.Data.Metadata.IncompleteGroups, "alerts.rules") != nil {
		t.Fatalf("partial metadata must name missing groups without marking reported rules incomplete: %#v", got.Data.Metadata)
	}
	if recovery := incompleteGroup(got.Data.Metadata.IncompleteGroups, "views"); recovery == nil || recovery.NextAction == "" || len(recovery.NextTools) == 0 {
		t.Fatalf("missing view recovery guidance: %#v", got.Data.Metadata.IncompleteGroups)
	}
}

func TestBuildOrgOverview_CloudProviderAvailabilityIsIndependent(t *testing.T) {
	got, drift, err := buildOrgOverview([]byte(`{
		"status":"success",
		"data":{
			"dashboard.count":0,
			"dashboard.panels.count":0,
			"dashboard.panels.logs.count":0,
			"dashboard.panels.metrics.count":0,
			"dashboard.panels.traces.count":0,
			"rule.count":0,
			"alertmanager.channel.count":0,
			"savedview.count":0,
			"logs_pipeline.total.count":0,
			"logs_pipeline.enabled.count":0,
			"cloudintegration.aws.connectedaccounts.count":0
		}
	}`))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if got.Data.CloudIntegrations.SourceAvailability != "partial" {
		t.Fatalf("sourceAvailability = %q, want partial", got.Data.CloudIntegrations.SourceAvailability)
	}
	aws := got.Data.CloudIntegrations.Providers["aws"]
	if !aws.DataAvailable || aws.ConnectedAccounts == nil || *aws.ConnectedAccounts != 0 {
		t.Fatalf("reported AWS zero must remain available: %#v", aws)
	}
	azure := got.Data.CloudIntegrations.Providers["azure"]
	if azure.DataAvailable || azure.ConnectedAccounts != nil {
		t.Fatalf("missing Azure count must remain unavailable: %#v", azure)
	}
	if !containsString(drift, "cloudintegration.azure.connectedaccounts.count") {
		t.Fatalf("missing Azure source field must be observable: %v", drift)
	}
	if recovery := incompleteGroup(got.Data.Metadata.IncompleteGroups, "cloudIntegrations"); recovery == nil || recovery.NextAction == "" || containsString(recovery.NextTools, "signoz_get_org_overview") {
		t.Fatalf("missing cloud recovery guidance: %#v", got.Data.Metadata.IncompleteGroups)
	}
}

func TestBuildOrgOverview_CloudUnavailableDoesNotDegradeCompleteSnapshot(t *testing.T) {
	got, drift, err := buildOrgOverview(completeOrgOverviewPayload(nil))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if got.Data.CloudIntegrations.SourceAvailability != "unavailable" {
		t.Fatalf("sourceAvailability = %q, want unavailable", got.Data.CloudIntegrations.SourceAvailability)
	}
	if got.Data.Metadata.Partial || len(got.Data.Metadata.IncompleteGroups) != 0 {
		t.Fatalf("edition-gated cloud absence must not degrade an otherwise complete snapshot: %#v", got.Data.Metadata)
	}
	if len(drift) != 0 {
		t.Fatalf("edition-gated cloud absence must not emit drift: %v", drift)
	}
}

func TestBuildOrgOverview_LogPipelineTotalControlsAvailability(t *testing.T) {
	got, _, err := buildOrgOverview(completeOrgOverviewPayload(map[string]any{
		"logs_pipeline.enabled.count": nil,
	}))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if !got.Data.LogPipelines.Available || got.Data.LogPipelines.Count == nil {
		t.Fatalf("reported pipeline total must keep the group available: %#v", got.Data.LogPipelines)
	}
	group := incompleteGroup(got.Data.Metadata.IncompleteGroups, "logPipelines")
	if group == nil || !containsString(group.Fields, "logPipelines.enabledCount") {
		t.Fatalf("missing enabled count must remain explicit in recovery metadata: %#v", got.Data.Metadata)
	}
}

func completeOrgOverviewPayload(overrides map[string]any) []byte {
	stats := map[string]any{
		"dashboard.count":                0,
		"dashboard.panels.count":         0,
		"dashboard.panels.logs.count":    0,
		"dashboard.panels.metrics.count": 0,
		"dashboard.panels.traces.count":  0,
		"rule.count":                     0,
		"alertmanager.channel.count":     0,
		"savedview.count":                0,
		"logs_pipeline.total.count":      0,
		"logs_pipeline.enabled.count":    0,
	}
	for key, value := range overrides {
		if value == nil {
			delete(stats, key)
			continue
		}
		stats[key] = value
	}
	payload, err := json.Marshal(map[string]any{"status": "success", "data": stats})
	if err != nil {
		panic(err)
	}
	return payload
}

func incompleteGroup(groups []orgOverviewIncompleteGroup, name string) *orgOverviewIncompleteGroup {
	for i := range groups {
		if groups[i].Group == name {
			return &groups[i]
		}
	}
	return nil
}

func TestHandleGetOrgOverview_MalformedEnvelopeFailsClosedWithoutLeaking(t *testing.T) {
	const upstream = `{"status":"success","payload":{"telemetry.logs.count":1,"user.count":2}}`
	var logs bytes.Buffer
	h := newTestHandler(&client.MockClient{
		GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(upstream), nil
		},
	})
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	result, err := h.handleGetOrgOverview(testCtx(), makeToolRequest("signoz_get_org_overview", map[string]any{}))
	if err != nil || !result.IsError {
		t.Fatalf("expected a safe coded error: result=%#v err=%v", result, err)
	}
	if code := resultCode(t, result); code != CodeUpstreamError {
		t.Fatalf("code = %q, want %q", code, CodeUpstreamError)
	}
	if strings.Contains(textContent(t, result), "telemetry.logs.count") || strings.Contains(textContent(t, result), "user.count") {
		t.Fatal("coded error leaked unfiltered upstream stats")
	}
	if !strings.Contains(logs.String(), "refusing an unfiltered fallback") {
		t.Fatalf("missing safe-fallback WARN: %s", logs.String())
	}
}

func TestHandleGetOrgOverview_AuthzFailureReturnsUpstreamCode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		wantCode   string
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, wantCode: CodeUnauthorized},
		{name: "forbidden", statusCode: http.StatusForbidden, wantCode: CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&client.MockClient{
				GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
					return nil, fmt.Errorf("organization overview: %w", &client.HTTPStatusError{
						StatusCode: tc.statusCode,
						Body:       `{"status":"error","error":{"message":"denied"}}`,
					})
				},
			})
			result, err := h.handleGetOrgOverview(testCtx(), makeToolRequest("signoz_get_org_overview", map[string]any{}))
			if err != nil || !result.IsError {
				t.Fatalf("expected coded tool error: result=%#v err=%v", result, err)
			}
			if code := resultCode(t, result); code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}
