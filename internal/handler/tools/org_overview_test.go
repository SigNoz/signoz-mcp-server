package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleGetOrgOverview_ProjectsEveryCurrentFamilyAndPreservesAllSourceStats(t *testing.T) {
	const largeCount = uint64(9007199254740993)
	payload := completeOrgOverviewPayload(nil)
	var logs bytes.Buffer
	h := newTestHandler(&client.MockClient{
		GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(payload), nil
		},
	})
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

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
	outputJSON := textContent(t, result)
	if !sameJSONValue(t, []byte(outputJSON), result.StructuredContent) {
		t.Fatal("structuredContent differs from the exact-number text result")
	}
	sourceStats := assertSourceStatsExact(t, payload, []byte(outputJSON))
	if number, ok := sourceStats["dashboard.count"].(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("large source count was not retained exactly: type=%T", sourceStats["dashboard.count"])
	}

	var got orgOverviewOutput
	if err := json.Unmarshal([]byte(outputJSON), &got); err != nil {
		t.Fatalf("decode typed overview: %v", err)
	}
	if !got.Data.Signals.Logs.Available || got.Data.Signals.Logs.Count == nil || *got.Data.Signals.Logs.Count != 123 || got.Data.Signals.Logs.LastObservedTime == nil || *got.Data.Signals.Logs.LastObservedTime != "2026-08-02T10:00:00Z" {
		t.Fatalf("logs projection = %#v", got.Data.Signals.Logs)
	}
	if !got.Data.Signals.Metrics.Available || got.Data.Signals.Metrics.Count == nil || *got.Data.Signals.Metrics.Count != 456 || got.Data.Signals.Metrics.Infrastructure.SystemExists == nil || !*got.Data.Signals.Metrics.Infrastructure.SystemExists || got.Data.Signals.Metrics.Infrastructure.K8sExists == nil || *got.Data.Signals.Metrics.Infrastructure.K8sExists {
		t.Fatalf("metrics projection = %#v", got.Data.Signals.Metrics)
	}
	if !got.Data.Signals.Traces.Available || got.Data.Signals.Traces.Count == nil || *got.Data.Signals.Traces.Count != 789 {
		t.Fatalf("traces projection = %#v", got.Data.Signals.Traces)
	}
	if !got.Data.Dashboards.Available || got.Data.Dashboards.Count == nil || *got.Data.Dashboards.Count != largeCount || got.Data.Dashboards.PublicCount == nil || *got.Data.Dashboards.PublicCount != 1 || got.Data.Dashboards.Panels.Count == nil || *got.Data.Dashboards.Panels.Count != 7 {
		t.Fatalf("dashboard projection = %#v", got.Data.Dashboards)
	}
	if !got.Data.Alerts.Rules.Available || got.Data.Alerts.Rules.Count == nil || *got.Data.Alerts.Rules.Count != 3 || got.Data.Alerts.Rules.ByType["anomaly"] != 1 || got.Data.Alerts.Rules.BySignal["metric"] != 2 {
		t.Fatalf("alert rules projection = %#v", got.Data.Alerts.Rules)
	}
	if !got.Data.Alerts.Runtime.Available || got.Data.Alerts.Runtime.FiringRuleCount == nil || *got.Data.Alerts.Runtime.FiringRuleCount != 1 || got.Data.Alerts.Runtime.LastFiredTimeUnix == nil || *got.Data.Alerts.Runtime.LastFiredTimeUnix != 1785668400 {
		t.Fatalf("alert runtime projection = %#v", got.Data.Alerts.Runtime)
	}
	if !got.Data.Alerts.NotificationChannels.Available || got.Data.Alerts.NotificationChannels.Count == nil || *got.Data.Alerts.NotificationChannels.Count != 5 || got.Data.Alerts.NotificationChannels.ByType["slack"] != 1 {
		t.Fatalf("notification channel projection = %#v", got.Data.Alerts.NotificationChannels)
	}
	if _, exists := got.Data.Alerts.NotificationChannels.ByType["slack.enabled"]; exists {
		t.Fatalf("deeper future channel keys must stay source-only: %#v", got.Data.Alerts.NotificationChannels.ByType)
	}
	if !got.Data.Views.Available || got.Data.Views.Count == nil || *got.Data.Views.Count != 4 || got.Data.Views.BySource["meter"] != 1 {
		t.Fatalf("saved-view projection = %#v", got.Data.Views)
	}
	if !got.Data.LogPipelines.Available || got.Data.LogPipelines.Count == nil || *got.Data.LogPipelines.Count != 2 || got.Data.LogPipelines.EnabledCount == nil || *got.Data.LogPipelines.EnabledCount != 1 {
		t.Fatalf("log-pipeline projection = %#v", got.Data.LogPipelines)
	}
	aws := got.Data.CloudIntegrations.Providers["aws"]
	azure := got.Data.CloudIntegrations.Providers["azure"]
	if got.Data.CloudIntegrations.SourceAvailability != "complete" || !aws.DataAvailable || aws.ConnectedAccounts == nil || *aws.ConnectedAccounts != 4 || !azure.DataAvailable || azure.ConnectedAccounts == nil || *azure.ConnectedAccounts != 0 {
		t.Fatalf("cloud integrations projection = %#v", got.Data.CloudIntegrations)
	}
	if !got.Data.Users.Available || got.Data.Users.Count == nil || *got.Data.Users.Count != 99 || got.Data.Users.ActiveCount == nil || *got.Data.Users.ActiveCount != 90 || got.Data.Users.DeletedCount == nil || *got.Data.Users.DeletedCount != 4 || got.Data.Users.PendingInviteCount == nil || *got.Data.Users.PendingInviteCount != 5 {
		t.Fatalf("users projection = %#v", got.Data.Users)
	}
	if !got.Data.Authentication.Tokens.Available || got.Data.Authentication.Tokens.Count == nil || *got.Data.Authentication.Tokens.Count != 2 || got.Data.Authentication.Tokens.LastObservedTimeUnix == nil || *got.Data.Authentication.Tokens.LastObservedTimeUnix != 1785661200 || !got.Data.Authentication.Domains.Available || got.Data.Authentication.Domains.Count == nil || *got.Data.Authentication.Domains.Count != 1 || got.Data.Authentication.Domains.ByType["google_auth"] != 1 {
		t.Fatalf("authentication projection = %#v", got.Data.Authentication)
	}
	if !got.Data.ServiceAccounts.Available || got.Data.ServiceAccounts.Count == nil || *got.Data.ServiceAccounts.Count != 6 || got.Data.ServiceAccounts.KeyCount == nil || *got.Data.ServiceAccounts.KeyCount != 7 {
		t.Fatalf("service-account projection = %#v", got.Data.ServiceAccounts)
	}
	if !got.Data.Authorization.Roles.Available || got.Data.Authorization.Roles.Count == nil || *got.Data.Authorization.Roles.Count != 3 || got.Data.Authorization.Roles.ByType["custom"] != 1 || got.Data.Authorization.Roles.ByType["managed"] != 2 {
		t.Fatalf("authorization projection = %#v", got.Data.Authorization)
	}
	if _, exists := got.Data.Authorization.Roles.ByType["custom.scope"]; exists {
		t.Fatalf("deeper future role keys must stay source-only: %#v", got.Data.Authorization.Roles.ByType)
	}
	if got.Data.License.ID == nil || *got.Data.License.ID != "019fc113-6e1f-7e91-8a4c-47013e400dfa" || got.Data.License.PlanName == nil || *got.Data.License.PlanName != "Enterprise" || got.Data.License.StateName == nil || *got.Data.License.StateName != "active" || got.Data.License.FreeUntil == nil || *got.Data.License.FreeUntil != "2026-09-01T00:00:00Z" {
		t.Fatalf("license projection = %#v", got.Data.License)
	}
	if got.Data.Configuration.SQLStoreProvider == nil || *got.Data.Configuration.SQLStoreProvider != "postgres" || got.Data.Configuration.TokenizerProvider == nil || *got.Data.Configuration.TokenizerProvider != "opaque" || got.Data.Configuration.CacheProvider == nil || *got.Data.Configuration.CacheProvider != "redis" {
		t.Fatalf("configuration projection = %#v", got.Data.Configuration)
	}

	if got.Data.Metadata.ReportedStatCount != len(sourceStats) || got.Data.Metadata.ProjectedStatCount != len(sourceStats)-5 || got.Data.Metadata.UnprojectedStatCount != 5 {
		t.Fatalf("projection counts do not reconcile: %#v", got.Data.Metadata)
	}
	if got.Data.Metadata.ProjectionPartial || len(got.Data.Metadata.IncompleteGroups) != 0 || len(got.Data.Metadata.InvalidProjectionFields) != 0 {
		t.Fatalf("complete typed projection reported partial metadata: %#v", got.Data.Metadata)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("unknown future object/array/null fields must not trigger WARN: %s", logs.String())
	}
}

func TestHandleGetOrgOverview_InvalidProjectionStaysAuthoritativeAndWarns(t *testing.T) {
	payload := completeOrgOverviewPayload(map[string]any{"dashboard.count": "three"})
	var logs bytes.Buffer
	h := newTestHandler(&client.MockClient{
		GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(payload), nil
		},
	})
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	result, err := h.handleGetOrgOverview(testCtx(), makeToolRequest("signoz_get_org_overview", map[string]any{}))
	if err != nil || result.IsError {
		t.Fatalf("overview failed: result=%#v err=%v", result, err)
	}
	sourceStats := assertSourceStatsExact(t, payload, []byte(textContent(t, result)))
	if sourceStats["dashboard.count"] != "three" {
		t.Fatalf("invalid known field was not retained in sourceStats: type=%T", sourceStats["dashboard.count"])
	}

	var got orgOverviewOutput
	if err := json.Unmarshal([]byte(textContent(t, result)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Dashboards.Count != nil || got.Data.Dashboards.Available {
		t.Fatalf("invalid dashboard count entered the typed projection: %#v", got.Data.Dashboards)
	}
	if !containsString(got.Data.Metadata.InvalidProjectionFields, "dashboard.count") || !got.Data.Metadata.ProjectionPartial {
		t.Fatalf("invalid projection diagnostics = %#v", got.Data.Metadata)
	}
	if got.Data.Metadata.ReportedStatCount != len(sourceStats) || got.Data.Metadata.ProjectedStatCount+got.Data.Metadata.UnprojectedStatCount != len(sourceStats) || got.Data.Metadata.UnprojectedStatCount != 6 {
		t.Fatalf("projection counts do not reconcile after invalid field: %#v", got.Data.Metadata)
	}
	dashboardRecovery := incompleteGroup(got.Data.Metadata.IncompleteGroups, "dashboards")
	if dashboardRecovery == nil || !containsString(dashboardRecovery.NextTools, "signoz_list_dashboards") {
		t.Fatalf("missing dashboard recovery guidance: %#v", got.Data.Metadata.IncompleteGroups)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "dashboard.count") {
		t.Fatalf("missing bounded projection-drift WARN: %s", logs.String())
	}
	if strings.Contains(logs.String(), "future.object") || strings.Contains(logs.String(), "future.array") || strings.Contains(logs.String(), "future.null") || strings.Contains(logs.String(), "alertmanager.channel.type.slack.enabled") {
		t.Fatalf("unknown future fields were incorrectly treated as drift: %s", logs.String())
	}
}

func TestBuildOrgOverview_MissingCountsStayUnknown(t *testing.T) {
	got, drift, err := buildOrgOverview([]byte(`{"status":"success","data":{"rule.count":0}}`))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if len(drift) == 0 {
		t.Fatal("missing projection sentinels must surface as response drift")
	}
	if got.Data.Alerts.Rules.Count == nil || *got.Data.Alerts.Rules.Count != 0 || !got.Data.Alerts.Rules.Available {
		t.Fatalf("reported zero rule count = %#v, want explicit available zero", got.Data.Alerts.Rules)
	}
	if got.Data.Dashboards.Available || got.Data.Views.Available || got.Data.Dashboards.Count != nil || got.Data.Views.Count != nil {
		t.Fatalf("missing collector counts must stay unavailable, dashboards=%#v views=%#v", got.Data.Dashboards, got.Data.Views)
	}
	if got.Data.ServiceAccounts.Available {
		t.Fatal("missing service-account total must make the group unavailable")
	}
	if got.Data.CloudIntegrations.SourceAvailability != "unavailable" {
		t.Fatalf("cloud source availability = %q, want unavailable", got.Data.CloudIntegrations.SourceAvailability)
	}
	if incompleteGroup(got.Data.Metadata.IncompleteGroups, "cloudIntegrations") != nil {
		t.Fatalf("structurally absent cloud stats must not add cloud recovery: %#v", got.Data.Metadata.IncompleteGroups)
	}
	if !got.Data.Metadata.ProjectionPartial || incompleteGroup(got.Data.Metadata.IncompleteGroups, "alerts.rules") != nil {
		t.Fatalf("partial metadata must name missing groups without marking reported rules incomplete: %#v", got.Data.Metadata)
	}
	if got.Data.Metadata.ReportedStatCount != 1 || got.Data.Metadata.ProjectedStatCount != 1 || got.Data.Metadata.UnprojectedStatCount != 0 {
		t.Fatalf("metadata counts = %#v", got.Data.Metadata)
	}
	if recovery := incompleteGroup(got.Data.Metadata.IncompleteGroups, "views"); recovery == nil || recovery.NextAction == "" || len(recovery.NextTools) == 0 {
		t.Fatalf("missing view recovery guidance: %#v", got.Data.Metadata.IncompleteGroups)
	}
}

func TestBuildOrgOverview_CloudProviderAvailabilityIsIndependent(t *testing.T) {
	got, drift, err := buildOrgOverview(completeOrgOverviewPayload(map[string]any{
		"cloudintegration.azure.connectedaccounts.count": nil,
	}))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if got.Data.CloudIntegrations.SourceAvailability != "partial" {
		t.Fatalf("sourceAvailability = %q, want partial", got.Data.CloudIntegrations.SourceAvailability)
	}
	aws := got.Data.CloudIntegrations.Providers["aws"]
	if !aws.DataAvailable || aws.ConnectedAccounts == nil || *aws.ConnectedAccounts != 4 {
		t.Fatalf("reported AWS count must remain available: %#v", aws)
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

func TestBuildOrgOverview_CloudUnavailableDoesNotDegradeCompleteProjection(t *testing.T) {
	got, drift, err := buildOrgOverview(completeOrgOverviewPayload(map[string]any{
		"cloudintegration.aws.connectedaccounts.count":   nil,
		"cloudintegration.azure.connectedaccounts.count": nil,
	}))
	if err != nil {
		t.Fatalf("build overview: %v", err)
	}
	if got.Data.CloudIntegrations.SourceAvailability != "unavailable" {
		t.Fatalf("sourceAvailability = %q, want unavailable", got.Data.CloudIntegrations.SourceAvailability)
	}
	if got.Data.Metadata.ProjectionPartial || len(got.Data.Metadata.IncompleteGroups) != 0 {
		t.Fatalf("edition-gated cloud absence must not degrade an otherwise complete projection: %#v", got.Data.Metadata)
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

func TestBuildOrgOverview_AuthTokenCountDependsOnTokenizer(t *testing.T) {
	t.Run("opaque tokenizer requires count", func(t *testing.T) {
		got, drift, err := buildOrgOverview(completeOrgOverviewPayload(map[string]any{
			"auth_token.count": nil,
		}))
		if err != nil {
			t.Fatalf("build overview: %v", err)
		}
		if got.Data.Authentication.Tokens.Available || !containsString(drift, "auth_token.count") || !got.Data.Metadata.ProjectionPartial {
			t.Fatalf("missing opaque-token count must be detectable: drift=%v metadata=%#v", drift, got.Data.Metadata)
		}
		group := incompleteGroup(got.Data.Metadata.IncompleteGroups, "authentication.tokens")
		if group == nil || !containsString(group.Fields, "authentication.tokens.count") {
			t.Fatalf("missing opaque-token count lacks recovery metadata: %#v", got.Data.Metadata.IncompleteGroups)
		}
	})

	t.Run("jwt tokenizer legitimately omits count", func(t *testing.T) {
		got, drift, err := buildOrgOverview(completeOrgOverviewPayload(map[string]any{
			"auth_token.count":          nil,
			"config.tokenizer.provider": "jwt",
		}))
		if err != nil {
			t.Fatalf("build overview: %v", err)
		}
		if got.Data.Authentication.Tokens.Available || containsString(drift, "auth_token.count") || got.Data.Metadata.ProjectionPartial {
			t.Fatalf("JWT token-count absence must remain optional: drift=%v metadata=%#v", drift, got.Data.Metadata)
		}
		if incompleteGroup(got.Data.Metadata.IncompleteGroups, "authentication.tokens") != nil {
			t.Fatalf("JWT token-count absence produced recovery: %#v", got.Data.Metadata.IncompleteGroups)
		}
	})

	t.Run("unknown tokenizer warns without false partial", func(t *testing.T) {
		got, drift, err := buildOrgOverview(completeOrgOverviewPayload(map[string]any{
			"auth_token.count":          nil,
			"config.tokenizer.provider": "future-tokenizer",
		}))
		if err != nil {
			t.Fatalf("build overview: %v", err)
		}
		if !containsString(drift, "config.tokenizer.provider") {
			t.Fatalf("unknown tokenizer provider must emit drift: %v", drift)
		}
		if got.Data.Metadata.ProjectionPartial || incompleteGroup(got.Data.Metadata.IncompleteGroups, "authentication.tokens") != nil {
			t.Fatalf("unknown tokenizer must warn without claiming a failed token collector: %#v", got.Data.Metadata)
		}
	})
}

func TestOrgOverviewRecoveryNeverRecommendsRecursiveOverviewCall(t *testing.T) {
	groups := []string{
		"signals.logs",
		"signals.metrics",
		"signals.traces",
		"dashboards",
		"alerts.rules",
		"alerts.runtime",
		"alerts.notificationChannels",
		"views",
		"logPipelines",
		"cloudIntegrations",
		"users",
		"authentication.tokens",
		"authentication.domains",
		"serviceAccounts",
		"authorization.roles",
		"license",
		"configuration",
	}
	for _, group := range groups {
		t.Run(group, func(t *testing.T) {
			_, nextAction, nextTools := orgOverviewRecovery(group)
			if nextAction == "" {
				t.Fatal("recovery guidance must include a next action")
			}
			if containsString(nextTools, "signoz_get_org_overview") {
				t.Fatalf("recovery must not recursively recommend signoz_get_org_overview: %v", nextTools)
			}
		})
	}
}

func TestOrgOverviewOutputSchema_SourceStatsAcceptsArbitraryValues(t *testing.T) {
	entry := registeredTestTools(t)["signoz_get_org_overview"]
	if entry == nil {
		t.Fatal("signoz_get_org_overview is not registered")
	}
	rawSchema := outputSchemaJSON(entry.Tool)
	var schema map[string]any
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("decode output schema: %v", err)
	}
	sourceStatsSchema := descend(t, schema, "data", "sourceStats")
	if additional, exists := sourceStatsSchema["additionalProperties"]; exists {
		switch value := additional.(type) {
		case bool:
			if !value {
				t.Fatal("data.sourceStats output schema is closed")
			}
		case map[string]any:
			if len(value) != 0 {
				t.Fatalf("data.sourceStats constrains arbitrary values: %#v", value)
			}
		default:
			t.Fatalf("unexpected data.sourceStats additionalProperties shape: %T", additional)
		}
	}

	overview, _, err := buildOrgOverview(completeOrgOverviewPayload(nil))
	if err != nil {
		t.Fatalf("build schema probe: %v", err)
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		t.Fatalf("encode schema probe: %v", err)
	}
	compiled, err := compileToolSchema("signoz_get_org_overview", "output", rawSchema)
	if err != nil {
		t.Fatalf("compile output schema: %v", err)
	}
	if err := validateSchemaValue(compiled.validator, decodeJSONNumbers(t, encoded), false); err != nil {
		t.Fatalf("output schema rejected object/array/null and exact numeric sourceStats values: %v", err)
	}
}

func TestHandleGetOrgOverview_MalformedEnvelopeReturnsCodedErrorWithoutLeaking(t *testing.T) {
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
		t.Fatalf("expected a coded error: result=%#v err=%v", result, err)
	}
	if code := resultCode(t, result); code != CodeUpstreamError {
		t.Fatalf("code = %q, want %q", code, CodeUpstreamError)
	}
	if strings.Contains(textContent(t, result), "telemetry.logs.count") || strings.Contains(textContent(t, result), "user.count") {
		t.Fatal("coded error leaked upstream stats")
	}
	if !strings.Contains(logs.String(), "Unexpected response shape") || strings.Contains(logs.String(), "telemetry.logs.count") || strings.Contains(logs.String(), "user.count") {
		t.Fatalf("malformed-envelope WARN missing or leaked source fields: %s", logs.String())
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

func TestHandleGetOrgOverview_NotFoundIncludesConditionalRecovery(t *testing.T) {
	for _, body := range []string{
		`{"status":"error","error":{"code":"not_found","message":"route not found"}}`,
		`<html><body>workspace not found</body></html>`,
	} {
		h := newTestHandler(&client.MockClient{
			GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
				return nil, fmt.Errorf("organization overview: %w", &client.HTTPStatusError{
					StatusCode: http.StatusNotFound,
					Body:       body,
				})
			},
		})

		result, err := h.handleGetOrgOverview(testCtx(), makeToolRequest("signoz_get_org_overview", map[string]any{}))
		if err != nil || !result.IsError {
			t.Fatalf("expected coded tool error: result=%#v err=%v", result, err)
		}
		if code := resultCode(t, result); code != CodeNotFound {
			t.Fatalf("code = %q, want %q", code, CodeNotFound)
		}
		text := textContent(t, result)
		for _, want := range []string{"Verify that the configured SigNoz URL points to an active deployment", "requires SigNoz v0.129.0 or newer", "narrower inventory and signal-query tools"} {
			if !strings.Contains(text, want) {
				t.Fatalf("404 recovery missing %q: %s", want, text)
			}
		}
	}
}

const completeOrgOverviewJSON = `{
	"status":"success",
	"data":{
		"telemetry.logs.count":123,
		"telemetry.logs.last_observed.time":"2026-08-02T10:00:00Z",
		"telemetry.logs.last_observed.time_unix":1785664800,
		"telemetry.metrics.count":456,
		"telemetry.metrics.last_observed.time":"2026-08-02T10:01:00Z",
		"telemetry.metrics.last_observed.time_unix":1785664860,
		"telemetry.metrics.system.exists":true,
		"telemetry.metrics.k8s.exists":false,
		"telemetry.traces.count":789,
		"telemetry.traces.last_observed.time":"2026-08-02T10:02:00Z",
		"telemetry.traces.last_observed.time_unix":1785664920,
		"dashboard.count":9007199254740993,
		"public_dashboard.count":1,
		"dashboard.panels.count":7,
		"dashboard.panels.logs.count":2,
		"dashboard.panels.metrics.count":5,
		"dashboard.panels.traces.count":0,
		"rule.count":3,
		"rule.type.anomaly.count":1,
		"rule.type.promql.count":1,
		"rule.type.threshold.count":1,
		"alert.type.exceptions.count":0,
		"alert.type.logs.count":1,
		"alert.type.metric.count":2,
		"alert.type.traces.count":0,
		"alert.firing.count":1,
		"alert.last_fired.time":"2026-08-02T11:00:00Z",
		"alert.last_fired.time_unix":1785668400,
		"alertmanager.channel.count":5,
		"alertmanager.channel.type.email":1,
		"alertmanager.channel.type.msteamsv2":1,
		"alertmanager.channel.type.pagerduty":1,
		"alertmanager.channel.type.slack":1,
		"alertmanager.channel.type.webhook":1,
		"savedview.count":4,
		"savedview.source.logs.count":1,
		"savedview.source.meter.count":1,
		"savedview.source.metrics.count":1,
		"savedview.source.traces.count":1,
		"logs_pipeline.total.count":2,
		"logs_pipeline.enabled.count":1,
		"cloudintegration.aws.connectedaccounts.count":4,
		"cloudintegration.azure.connectedaccounts.count":0,
		"user.count":99,
		"user.count.active":90,
		"user.count.deleted":4,
		"user.count.pending_invite":5,
		"auth_token.count":2,
		"auth_token.last_observed_at.max.time":"2026-08-02T09:00:00Z",
		"auth_token.last_observed_at.max.time_unix":1785661200,
		"authdomain.count":1,
		"authdomain.google_auth.count":1,
		"serviceaccount.count":6,
		"serviceaccount.keys.count":7,
		"role.count":3,
		"role.custom.count":1,
		"role.managed.count":2,
		"license.id":"019fc113-6e1f-7e91-8a4c-47013e400dfa",
		"license.plan.name":"Enterprise",
		"license.state.name":"active",
		"license.free_until.time":"2026-09-01T00:00:00Z",
		"config.sqlstore.provider":"postgres",
		"config.tokenizer.provider":"opaque",
		"config.cache.provider":"redis",
		"future.object":{"nested":9007199254740995,"enabled":true},
		"future.array":[1,"two",null,{"x":3}],
		"future.null":null,
		"alertmanager.channel.type.slack.enabled":true,
		"role.custom.scope.count":1
	}
}`

func completeOrgOverviewPayload(overrides map[string]any) []byte {
	envelope := decodeJSONNumbersForHelper([]byte(completeOrgOverviewJSON))
	stats := envelope["data"].(map[string]any)
	for key, value := range overrides {
		if value == nil {
			delete(stats, key)
			continue
		}
		stats[key] = value
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		panic(err)
	}
	return payload
}

func assertSourceStatsExact(t *testing.T, upstreamPayload, outputPayload []byte) map[string]any {
	t.Helper()
	upstream := decodeJSONNumbers(t, upstreamPayload).(map[string]any)
	want := upstream["data"].(map[string]any)
	output := decodeJSONNumbers(t, outputPayload).(map[string]any)
	data, ok := output["data"].(map[string]any)
	if !ok {
		t.Fatal("tool output data is not an object")
	}
	got, ok := data["sourceStats"].(map[string]any)
	if !ok {
		t.Fatal("tool output data.sourceStats is not an object")
	}
	if len(got) != len(want) {
		t.Fatalf("sourceStats cardinality=%d want=%d", len(got), len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("sourceStats did not preserve every upstream key/value exactly")
	}
	return got
}

func decodeJSONNumbers(t *testing.T, payload []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return value
}

func decodeJSONNumbersForHelper(payload []byte) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		panic(err)
	}
	return value
}

func incompleteGroup(groups []orgOverviewIncompleteGroup, name string) *orgOverviewIncompleteGroup {
	for i := range groups {
		if groups[i].Group == name {
			return &groups[i]
		}
	}
	return nil
}
