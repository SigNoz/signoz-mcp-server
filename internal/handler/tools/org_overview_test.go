package tools

import (
	"bytes"
	"encoding/json"
	"testing"
)

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
		"featureFlags",
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
