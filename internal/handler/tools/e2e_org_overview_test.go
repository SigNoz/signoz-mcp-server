//go:build e2e

// Live contract verification for signoz_get_org_overview. Credentials are read
// only from SIGNOZ_E2E_URL / SIGNOZ_E2E_TOKEN by e2eSetup; this test never logs
// the token or the unfiltered upstream stats bag. It is strictly read-only and
// creates no resource, so cleanup is not applicable.
package tools

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestE2EOrgOverview(t *testing.T) {
	h, ctx := e2eSetup(t)

	raw, err := h.clientOverride.GetOrgOverview(ctx)
	if err != nil {
		t.Fatalf("fetch live upstream stats: %v", err)
	}
	var upstream struct {
		Status string                     `json:"status"`
		Data   map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &upstream); err != nil {
		t.Fatalf("decode live upstream stats envelope: %v", err)
	}
	if upstream.Status != "success" || upstream.Data == nil {
		t.Fatalf("unexpected live envelope status=%q dataPresent=%t", upstream.Status, upstream.Data != nil)
	}

	result, err := h.handleGetOrgOverview(ctx, makeToolRequest("signoz_get_org_overview", map[string]any{
		"searchContext": "Give me a safe organization posture overview",
	}))
	if err != nil {
		t.Fatalf("tool transport error: %v", err)
	}
	if result == nil {
		t.Fatal("tool returned a nil result")
	}
	if result.IsError {
		t.Fatalf("tool returned error: %s", firstTextBlock(t, result))
	}
	if result.StructuredContent == nil {
		t.Fatal("overview must return structuredContent")
	}

	var got orgOverviewOutput
	if err := json.Unmarshal([]byte(firstTextBlock(t, result)), &got); err != nil {
		t.Fatalf("decode tool output: %v", err)
	}
	if got.Data.Signals.Available || got.Data.Alerts.Runtime.Available {
		t.Fatal("deployment-wide signal or alert-runtime data was advertised as organization-scoped")
	}
	if got.Data.Metadata.ReportedStatCount != len(upstream.Data) {
		t.Fatalf("reportedStatCount=%d want=%d", got.Data.Metadata.ReportedStatCount, len(upstream.Data))
	}
	if got.Data.Metadata.IncludedStatCount+got.Data.Metadata.OmittedStatCount != got.Data.Metadata.ReportedStatCount {
		t.Fatalf("metadata counts do not reconcile: %#v", got.Data.Metadata)
	}
	incompleteGroupNames := make([]string, 0, len(got.Data.Metadata.IncompleteGroups))
	for _, group := range got.Data.Metadata.IncompleteGroups {
		if group.Group == "" || len(group.Fields) == 0 || group.Reason == "" || group.NextAction == "" {
			t.Fatalf("incomplete group lacks recovery metadata: %#v", group)
		}
		incompleteGroupNames = append(incompleteGroupNames, group.Group)
	}
	assertLiveCloudAvailability(t, upstream.Data, got)
	sort.Strings(incompleteGroupNames)
	t.Logf("overview completeness: partial=%t incompleteGroups=%s cloudSourceAvailability=%s",
		got.Data.Metadata.Partial,
		strings.Join(incompleteGroupNames, ", "),
		got.Data.CloudIntegrations.SourceAvailability)

	outputJSON := firstTextBlock(t, result)
	for key := range upstream.Data {
		if isDeploymentWideOrgStat(key) && strings.Contains(outputJSON, `"`+key+`"`) {
			t.Fatalf("deployment-wide source field %q leaked into output", key)
		}
	}

	roundTripped := roundTrippedOrgOverviewFields(t, upstream.Data, got)
	if len(roundTripped) == 0 {
		t.Fatal("live response contained no recognized organization-scoped overview fields")
	}
	sort.Strings(roundTripped)
	t.Logf("round-tripped organization-scoped source fields (%d): %s", len(roundTripped), strings.Join(roundTripped, ", "))
	t.Log("read-only E2E: created resources=0; cleanup=n/a")
}

func assertLiveCloudAvailability(t *testing.T, source map[string]json.RawMessage, got orgOverviewOutput) {
	t.Helper()
	reportedProviders := 0
	invalidProviders := 0
	for _, provider := range []string{"aws", "azure"} {
		key := "cloudintegration." + provider + ".connectedaccounts.count"
		raw, reported := source[key]
		actual, exists := got.Data.CloudIntegrations.Providers[provider]
		if !exists {
			t.Fatalf("cloud provider %q is absent from owned output", provider)
		}
		if !reported {
			if actual.DataAvailable || actual.ConnectedAccounts != nil {
				t.Fatalf("unreported provider %q was advertised as available: %#v", provider, actual)
			}
			continue
		}

		want, valid := rawUint64(raw)
		if !valid {
			invalidProviders++
			if actual.DataAvailable || actual.ConnectedAccounts != nil || !containsString(got.Data.Metadata.InvalidStatFields, key) {
				t.Fatalf("invalid provider field %q was not represented safely: provider=%#v invalid=%v", key, actual, got.Data.Metadata.InvalidStatFields)
			}
			continue
		}
		reportedProviders++
		if !actual.DataAvailable || actual.ConnectedAccounts == nil || *actual.ConnectedAccounts != want {
			t.Fatalf("provider field %q did not round-trip availability: got=%#v want=%d", key, actual, want)
		}
	}

	wantAvailability := "unavailable"
	if reportedProviders == 1 {
		wantAvailability = "partial"
	} else if reportedProviders == 2 {
		wantAvailability = "complete"
	}
	if got.Data.CloudIntegrations.SourceAvailability != wantAvailability {
		t.Fatalf("cloud sourceAvailability=%q want=%q", got.Data.CloudIntegrations.SourceAvailability, wantAvailability)
	}

	recovery := incompleteGroup(got.Data.Metadata.IncompleteGroups, "cloudIntegrations")
	switch {
	case reportedProviders == 0 && invalidProviders == 0:
		if recovery != nil {
			t.Fatalf("structurally absent cloud stats produced retry recovery: %#v", recovery)
		}
	case reportedProviders < 2:
		if recovery == nil || recovery.NextAction == "" || containsString(recovery.NextTools, "signoz_get_org_overview") {
			t.Fatalf("partial/invalid cloud stats lack actionable non-recursive recovery: %#v", recovery)
		}
	case recovery != nil:
		t.Fatalf("complete cloud stats produced incomplete recovery: %#v", recovery)
	}
}

func roundTrippedOrgOverviewFields(t *testing.T, source map[string]json.RawMessage, got orgOverviewOutput) []string {
	t.Helper()
	roundTripped := make([]string, 0)
	check := func(key string, target *uint64) {
		raw, exists := source[key]
		if !exists {
			return
		}
		want, valid := rawUint64(raw)
		if !valid {
			t.Fatalf("live source field %q is not a non-negative integer", key)
		}
		if target == nil || *target != want {
			t.Fatalf("field %q did not round-trip: got=%v want=%d", key, target, want)
		}
		roundTripped = append(roundTripped, key)
	}

	check("dashboard.count", got.Data.Dashboards.Count)
	check("public_dashboard.count", got.Data.Dashboards.PublicCount)
	check("dashboard.panels.count", got.Data.Dashboards.Panels.Count)
	check("dashboard.panels.logs.count", got.Data.Dashboards.Panels.Logs)
	check("dashboard.panels.metrics.count", got.Data.Dashboards.Panels.Metrics)
	check("dashboard.panels.traces.count", got.Data.Dashboards.Panels.Traces)
	check("rule.count", got.Data.Alerts.Rules.Count)
	check("alertmanager.channel.count", got.Data.Alerts.NotificationChannels.Count)
	check("savedview.count", got.Data.Views.Count)
	check("logs_pipeline.total.count", got.Data.LogPipelines.Count)
	check("logs_pipeline.enabled.count", got.Data.LogPipelines.EnabledCount)

	for key, raw := range source {
		want, valid := rawUint64(raw)
		if !valid {
			continue
		}
		var actual uint64
		var found bool
		switch {
		case strings.HasPrefix(key, "rule.type.") && strings.HasSuffix(key, ".count"):
			label := strings.TrimSuffix(strings.TrimPrefix(key, "rule.type."), ".count")
			actual, found = got.Data.Alerts.Rules.ByType[label]
		case strings.HasPrefix(key, "alert.type.") && strings.HasSuffix(key, ".count"):
			label := strings.TrimSuffix(strings.TrimPrefix(key, "alert.type."), ".count")
			actual, found = got.Data.Alerts.Rules.BySignal[label]
		case strings.HasPrefix(key, "alertmanager.channel.type."):
			label := strings.TrimPrefix(key, "alertmanager.channel.type.")
			actual, found = got.Data.Alerts.NotificationChannels.ByType[label]
		case strings.HasPrefix(key, "savedview.source.") && strings.HasSuffix(key, ".count"):
			label := strings.TrimSuffix(strings.TrimPrefix(key, "savedview.source."), ".count")
			actual, found = got.Data.Views.BySource[label]
		case strings.HasPrefix(key, "cloudintegration.") && strings.HasSuffix(key, ".connectedaccounts.count"):
			label := strings.TrimSuffix(strings.TrimPrefix(key, "cloudintegration."), ".connectedaccounts.count")
			provider, exists := got.Data.CloudIntegrations.Providers[label]
			if exists && provider.DataAvailable && provider.ConnectedAccounts != nil {
				actual = *provider.ConnectedAccounts
				found = true
			}
		}
		if found {
			if actual != want {
				t.Fatalf("field %q did not round-trip: got=%d want=%d", key, actual, want)
			}
			roundTripped = append(roundTripped, key)
		}
	}
	return roundTripped
}
