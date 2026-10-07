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

// wantTrue, wantEq, and wantPtr assert one projection field each, so a failure
// names the exact field and both values instead of dumping a whole struct.
func wantTrue(t *testing.T, field string, got bool) {
	t.Helper()
	if !got {
		t.Fatalf("%s = false, want true", field)
	}
}

func wantEq[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func wantPtr[T comparable](t *testing.T, field string, got *T, want T) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", field, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", field, *got, want)
	}
}

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
	wantTrue(t, "signals.logs.available", got.Data.Signals.Logs.Available)
	wantPtr(t, "signals.logs.count", got.Data.Signals.Logs.Count, 123)
	wantPtr(t, "signals.logs.lastObservedTime", got.Data.Signals.Logs.LastObservedTime, "2026-08-02T10:00:00Z")
	wantTrue(t, "signals.metrics.available", got.Data.Signals.Metrics.Available)
	wantPtr(t, "signals.metrics.count", got.Data.Signals.Metrics.Count, 456)
	wantPtr(t, "signals.metrics.infrastructure.systemExists", got.Data.Signals.Metrics.Infrastructure.SystemExists, true)
	wantPtr(t, "signals.metrics.infrastructure.k8sExists", got.Data.Signals.Metrics.Infrastructure.K8sExists, false)
	wantTrue(t, "signals.traces.available", got.Data.Signals.Traces.Available)
	wantPtr(t, "signals.traces.count", got.Data.Signals.Traces.Count, 789)
	wantTrue(t, "dashboards.available", got.Data.Dashboards.Available)
	wantPtr(t, "dashboards.count", got.Data.Dashboards.Count, largeCount)
	wantPtr(t, "dashboards.publicCount", got.Data.Dashboards.PublicCount, 1)
	wantPtr(t, "dashboards.panels.count", got.Data.Dashboards.Panels.Count, 7)
	wantTrue(t, "alerts.rules.available", got.Data.Alerts.Rules.Available)
	wantPtr(t, "alerts.rules.count", got.Data.Alerts.Rules.Count, 3)
	wantEq(t, `alerts.rules.byType["anomaly"]`, got.Data.Alerts.Rules.ByType["anomaly"], 1)
	wantEq(t, `alerts.rules.bySignal["metric"]`, got.Data.Alerts.Rules.BySignal["metric"], 2)
	wantTrue(t, "alerts.runtime.available", got.Data.Alerts.Runtime.Available)
	wantPtr(t, "alerts.runtime.firingRuleCount", got.Data.Alerts.Runtime.FiringRuleCount, 1)
	wantPtr(t, "alerts.runtime.lastFiredTimeUnix", got.Data.Alerts.Runtime.LastFiredTimeUnix, 1785668400)
	wantTrue(t, "alerts.notificationChannels.available", got.Data.Alerts.NotificationChannels.Available)
	wantPtr(t, "alerts.notificationChannels.count", got.Data.Alerts.NotificationChannels.Count, 5)
	wantEq(t, `alerts.notificationChannels.byType["slack"]`, got.Data.Alerts.NotificationChannels.ByType["slack"], 1)
	if _, exists := got.Data.Alerts.NotificationChannels.ByType["slack.enabled"]; exists {
		t.Fatalf("deeper future channel keys must stay source-only: %#v", got.Data.Alerts.NotificationChannels.ByType)
	}
	wantTrue(t, "views.available", got.Data.Views.Available)
	wantPtr(t, "views.count", got.Data.Views.Count, 4)
	wantEq(t, `views.bySource["meter"]`, got.Data.Views.BySource["meter"], 1)
	wantTrue(t, "logPipelines.available", got.Data.LogPipelines.Available)
	wantPtr(t, "logPipelines.count", got.Data.LogPipelines.Count, 2)
	wantPtr(t, "logPipelines.enabledCount", got.Data.LogPipelines.EnabledCount, 1)
	aws := got.Data.CloudIntegrations.Providers["aws"]
	azure := got.Data.CloudIntegrations.Providers["azure"]
	wantEq(t, "cloudIntegrations.sourceAvailability", got.Data.CloudIntegrations.SourceAvailability, "complete")
	wantTrue(t, `cloudIntegrations.providers["aws"].dataAvailable`, aws.DataAvailable)
	wantPtr(t, `cloudIntegrations.providers["aws"].connectedAccounts`, aws.ConnectedAccounts, 4)
	wantTrue(t, `cloudIntegrations.providers["azure"].dataAvailable`, azure.DataAvailable)
	wantPtr(t, `cloudIntegrations.providers["azure"].connectedAccounts`, azure.ConnectedAccounts, 0)
	wantTrue(t, "users.available", got.Data.Users.Available)
	wantPtr(t, "users.count", got.Data.Users.Count, 99)
	wantPtr(t, "users.activeCount", got.Data.Users.ActiveCount, 90)
	wantPtr(t, "users.deletedCount", got.Data.Users.DeletedCount, 4)
	wantPtr(t, "users.pendingInviteCount", got.Data.Users.PendingInviteCount, 5)
	wantTrue(t, "authentication.tokens.available", got.Data.Authentication.Tokens.Available)
	wantPtr(t, "authentication.tokens.count", got.Data.Authentication.Tokens.Count, 2)
	wantPtr(t, "authentication.tokens.lastObservedTimeUnix", got.Data.Authentication.Tokens.LastObservedTimeUnix, 1785661200)
	wantTrue(t, "authentication.domains.available", got.Data.Authentication.Domains.Available)
	wantPtr(t, "authentication.domains.count", got.Data.Authentication.Domains.Count, 1)
	wantEq(t, `authentication.domains.byType["google_auth"]`, got.Data.Authentication.Domains.ByType["google_auth"], 1)
	wantTrue(t, "serviceAccounts.available", got.Data.ServiceAccounts.Available)
	wantPtr(t, "serviceAccounts.count", got.Data.ServiceAccounts.Count, 6)
	wantPtr(t, "serviceAccounts.keyCount", got.Data.ServiceAccounts.KeyCount, 7)
	wantTrue(t, "authorization.roles.available", got.Data.Authorization.Roles.Available)
	wantPtr(t, "authorization.roles.count", got.Data.Authorization.Roles.Count, 3)
	wantEq(t, `authorization.roles.byType["custom"]`, got.Data.Authorization.Roles.ByType["custom"], 1)
	wantEq(t, `authorization.roles.byType["managed"]`, got.Data.Authorization.Roles.ByType["managed"], 2)
	if _, exists := got.Data.Authorization.Roles.ByType["custom.scope"]; exists {
		t.Fatalf("deeper future role keys must stay source-only: %#v", got.Data.Authorization.Roles.ByType)
	}
	wantPtr(t, "license.id", got.Data.License.ID, "019fc113-6e1f-7e91-8a4c-47013e400dfa")
	wantPtr(t, "license.planName", got.Data.License.PlanName, "Enterprise")
	wantPtr(t, "license.stateName", got.Data.License.StateName, "active")
	wantPtr(t, "license.freeUntil", got.Data.License.FreeUntil, "2026-09-01T00:00:00Z")
	wantPtr(t, "configuration.sqlStoreProvider", got.Data.Configuration.SQLStoreProvider, "postgres")
	wantPtr(t, "configuration.tokenizerProvider", got.Data.Configuration.TokenizerProvider, "opaque")
	wantPtr(t, "configuration.cacheProvider", got.Data.Configuration.CacheProvider, "redis")

	wantEq(t, "metadata.reportedStatCount", got.Data.Metadata.ReportedStatCount, len(sourceStats))
	wantEq(t, "metadata.projectedStatCount", got.Data.Metadata.ProjectedStatCount, len(sourceStats)-5)
	wantEq(t, "metadata.unprojectedStatCount", got.Data.Metadata.UnprojectedStatCount, 5)
	wantEq(t, "metadata.projectionPartial", got.Data.Metadata.ProjectionPartial, false)
	wantEq(t, "metadata.incompleteGroups", len(got.Data.Metadata.IncompleteGroups), 0)
	wantEq(t, "metadata.invalidProjectionFields", len(got.Data.Metadata.InvalidProjectionFields), 0)
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

func TestHandleGetOrgOverview_UnavailableRouteIncludesConditionalRecovery(t *testing.T) {
	notFound := func(body string) error {
		return fmt.Errorf("organization overview: %w", &client.HTTPStatusError{StatusCode: http.StatusNotFound, Body: body})
	}
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "json 404", err: notFound(`{"status":"error","error":{"code":"not_found","message":"route not found"}}`), wantCode: CodeNotFound},
		{name: "html 404", err: notFound(`<html><body>workspace not found</body></html>`), wantCode: CodeNotFound},
		{name: "html 200", err: fmt.Errorf("organization overview: %w", client.ErrNonJSONResponse), wantCode: CodeUpstreamError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&client.MockClient{
				GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
					return nil, tc.err
				},
			})

			result, err := h.handleGetOrgOverview(testCtx(), makeToolRequest("signoz_get_org_overview", map[string]any{}))
			if err != nil || !result.IsError {
				t.Fatalf("expected coded tool error: result=%#v err=%v", result, err)
			}
			if code := resultCode(t, result); code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
			text := textContent(t, result)
			for _, want := range []string{"Verify that the configured SigNoz URL points to an active deployment", "signoz_list_dashboards", "signoz_list_alert_rules", "signoz_search_logs", "signoz_query_metrics"} {
				if !strings.Contains(text, want) {
					t.Fatalf("recovery missing %q: %s", want, text)
				}
			}
		})
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
