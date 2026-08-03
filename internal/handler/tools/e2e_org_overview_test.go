//go:build e2e

// Live contract verification for signoz_get_org_overview. Credentials are read
// only from SIGNOZ_E2E_URL / SIGNOZ_E2E_TOKEN by e2eSetup; this test never logs
// the token or any source values. It is strictly read-only and creates no
// resource, so cleanup is not applicable.
package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
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
	// Drive the handler with the exact response captured from the real client.
	// A second live request can legitimately advance telemetry counts or
	// freshness timestamps and would make an exact conservation check flaky.
	h.clientOverride = &signozclient.MockClient{
		GetOrgOverviewFn: func(context.Context) (json.RawMessage, error) {
			return raw, nil
		},
	}

	result, err := h.handleGetOrgOverview(ctx, makeToolRequest("signoz_get_org_overview", map[string]any{
		"searchContext": "Give me a complete SigNoz deployment posture overview",
	}))
	if err != nil {
		t.Fatalf("tool transport error: %v", err)
	}
	if result == nil {
		t.Fatal("tool returned a nil result")
	}
	if result.IsError {
		t.Fatalf("tool returned an error without echoing source data; code=%q", resultCode(t, result))
	}
	if result.StructuredContent == nil {
		t.Fatal("overview must return structuredContent")
	}
	outputJSON := []byte(firstTextBlock(t, result))
	if !sameJSONValue(t, outputJSON, result.StructuredContent) {
		t.Fatal("structuredContent differs from the exact-number text result")
	}

	var got orgOverviewOutput
	if err := json.Unmarshal(outputJSON, &got); err != nil {
		t.Fatalf("decode typed tool output: %v", err)
	}
	sourceStats := liveSourceStats(t, outputJSON)
	if len(sourceStats) != len(upstream.Data) {
		t.Fatalf("sourceStats cardinality=%d want=%d", len(sourceStats), len(upstream.Data))
	}
	for key, rawValue := range upstream.Data {
		want, err := decodeRawJSON(rawValue)
		if err != nil {
			t.Fatalf("decode live source field %q: %v", key, err)
		}
		actual, exists := sourceStats[key]
		if !exists {
			t.Fatalf("sourceStats omitted live source field %q", key)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("sourceStats changed the semantic value of live source field %q", key)
		}
	}

	projectedFields, invalidFields := assertLiveTypedProjections(t, upstream.Data, got)
	if got.Data.Metadata.ReportedStatCount != len(upstream.Data) {
		t.Fatalf("reportedStatCount=%d want=%d", got.Data.Metadata.ReportedStatCount, len(upstream.Data))
	}
	if got.Data.Metadata.ProjectedStatCount != len(projectedFields) {
		t.Fatalf("projectedStatCount=%d validated=%d", got.Data.Metadata.ProjectedStatCount, len(projectedFields))
	}
	if got.Data.Metadata.UnprojectedStatCount != len(upstream.Data)-len(projectedFields) || got.Data.Metadata.ProjectedStatCount+got.Data.Metadata.UnprojectedStatCount != got.Data.Metadata.ReportedStatCount {
		t.Fatalf("projection metadata counts do not reconcile: %#v", got.Data.Metadata)
	}
	sort.Strings(invalidFields)
	if !slices.Equal(got.Data.Metadata.InvalidProjectionFields, invalidFields) {
		t.Fatalf("invalidProjectionFields cardinality/content mismatch: got=%d want=%d", len(got.Data.Metadata.InvalidProjectionFields), len(invalidFields))
	}

	incompleteGroupNames := make([]string, 0, len(got.Data.Metadata.IncompleteGroups))
	for _, group := range got.Data.Metadata.IncompleteGroups {
		if group.Group == "" || len(group.Fields) == 0 || group.Reason == "" || group.NextAction == "" {
			t.Fatalf("incomplete group lacks recovery metadata: group=%q fields=%d", group.Group, len(group.Fields))
		}
		incompleteGroupNames = append(incompleteGroupNames, group.Group)
	}
	if got.Data.Metadata.ProjectionPartial != (len(got.Data.Metadata.IncompleteGroups) > 0) {
		t.Fatalf("projectionPartial=%t incompleteGroups=%d", got.Data.Metadata.ProjectionPartial, len(got.Data.Metadata.IncompleteGroups))
	}
	sort.Strings(projectedFields)
	sort.Strings(incompleteGroupNames)
	t.Logf("sourceStats round-tripped fields (%d): %s", len(sourceStats), strings.Join(sortedLiveKeys(upstream.Data), ", "))
	t.Logf("typed projections validated (%d): %s", len(projectedFields), strings.Join(projectedFields, ", "))
	t.Logf("projection completeness: partial=%t incompleteGroups=%s cloudSourceAvailability=%s",
		got.Data.Metadata.ProjectionPartial,
		strings.Join(incompleteGroupNames, ", "),
		got.Data.CloudIntegrations.SourceAvailability)
	t.Log("read-only E2E: created resources=0; cleanup=n/a; credential remained environment-only")
}

func liveSourceStats(t *testing.T, outputJSON []byte) map[string]any {
	t.Helper()
	root, ok := decodeJSONNumbers(t, outputJSON).(map[string]any)
	if !ok {
		t.Fatal("tool output is not an object")
	}
	data, ok := root["data"].(map[string]any)
	if !ok {
		t.Fatal("tool output data is not an object")
	}
	sourceStats, ok := data["sourceStats"].(map[string]any)
	if !ok {
		t.Fatal("tool output data.sourceStats is not an object")
	}
	return sourceStats
}

func assertLiveTypedProjections(t *testing.T, source map[string]json.RawMessage, got orgOverviewOutput) ([]string, []string) {
	t.Helper()
	projected := make(map[string]struct{})
	invalid := make(map[string]struct{})
	markInvalid := func(key string) {
		invalid[key] = struct{}{}
		if !containsString(got.Data.Metadata.InvalidProjectionFields, key) {
			t.Fatalf("invalid typed source field %q is absent from invalidProjectionFields", key)
		}
	}
	checkUint := func(key string, actual *uint64) {
		raw, exists := source[key]
		if !exists {
			if actual != nil {
				t.Fatalf("unreported source field %q populated a typed projection", key)
			}
			return
		}
		want, valid := rawUint64(raw)
		if !valid {
			if actual != nil {
				t.Fatalf("invalid source field %q populated a typed projection", key)
			}
			markInvalid(key)
			return
		}
		if actual == nil || *actual != want {
			t.Fatalf("typed projection mismatch for source field %q", key)
		}
		projected[key] = struct{}{}
	}
	checkInt64 := func(key string, actual *int64) {
		raw, exists := source[key]
		if !exists {
			if actual != nil {
				t.Fatalf("unreported source field %q populated a typed projection", key)
			}
			return
		}
		want, valid := rawInt64(raw)
		if !valid {
			if actual != nil {
				t.Fatalf("invalid source field %q populated a typed projection", key)
			}
			markInvalid(key)
			return
		}
		if actual == nil || *actual != want {
			t.Fatalf("typed projection mismatch for source field %q", key)
		}
		projected[key] = struct{}{}
	}
	checkString := func(key string, actual *string) {
		raw, exists := source[key]
		if !exists {
			if actual != nil {
				t.Fatalf("unreported source field %q populated a typed projection", key)
			}
			return
		}
		want, valid := rawString(raw)
		if !valid {
			if actual != nil {
				t.Fatalf("invalid source field %q populated a typed projection", key)
			}
			markInvalid(key)
			return
		}
		if actual == nil || *actual != want {
			t.Fatalf("typed projection mismatch for source field %q", key)
		}
		projected[key] = struct{}{}
	}
	checkBool := func(key string, actual *bool) {
		raw, exists := source[key]
		if !exists {
			if actual != nil {
				t.Fatalf("unreported source field %q populated a typed projection", key)
			}
			return
		}
		want, valid := rawBool(raw)
		if !valid {
			if actual != nil {
				t.Fatalf("invalid source field %q populated a typed projection", key)
			}
			markInvalid(key)
			return
		}
		if actual == nil || *actual != want {
			t.Fatalf("typed projection mismatch for source field %q", key)
		}
		projected[key] = struct{}{}
	}

	checkUint("telemetry.logs.count", got.Data.Signals.Logs.Count)
	checkString("telemetry.logs.last_observed.time", got.Data.Signals.Logs.LastObservedTime)
	checkInt64("telemetry.logs.last_observed.time_unix", got.Data.Signals.Logs.LastObservedTimeUnix)
	checkUint("telemetry.metrics.count", got.Data.Signals.Metrics.Count)
	checkString("telemetry.metrics.last_observed.time", got.Data.Signals.Metrics.LastObservedTime)
	checkInt64("telemetry.metrics.last_observed.time_unix", got.Data.Signals.Metrics.LastObservedTimeUnix)
	checkBool("telemetry.metrics.system.exists", got.Data.Signals.Metrics.Infrastructure.SystemExists)
	checkBool("telemetry.metrics.k8s.exists", got.Data.Signals.Metrics.Infrastructure.K8sExists)
	checkUint("telemetry.traces.count", got.Data.Signals.Traces.Count)
	checkString("telemetry.traces.last_observed.time", got.Data.Signals.Traces.LastObservedTime)
	checkInt64("telemetry.traces.last_observed.time_unix", got.Data.Signals.Traces.LastObservedTimeUnix)
	checkUint("dashboard.count", got.Data.Dashboards.Count)
	checkUint("public_dashboard.count", got.Data.Dashboards.PublicCount)
	checkUint("dashboard.panels.count", got.Data.Dashboards.Panels.Count)
	checkUint("dashboard.panels.logs.count", got.Data.Dashboards.Panels.Logs)
	checkUint("dashboard.panels.metrics.count", got.Data.Dashboards.Panels.Metrics)
	checkUint("dashboard.panels.traces.count", got.Data.Dashboards.Panels.Traces)
	checkUint("rule.count", got.Data.Alerts.Rules.Count)
	checkUint("alert.firing.count", got.Data.Alerts.Runtime.FiringRuleCount)
	checkString("alert.last_fired.time", got.Data.Alerts.Runtime.LastFiredTime)
	checkInt64("alert.last_fired.time_unix", got.Data.Alerts.Runtime.LastFiredTimeUnix)
	checkUint("alertmanager.channel.count", got.Data.Alerts.NotificationChannels.Count)
	checkUint("savedview.count", got.Data.Views.Count)
	checkUint("logs_pipeline.total.count", got.Data.LogPipelines.Count)
	checkUint("logs_pipeline.enabled.count", got.Data.LogPipelines.EnabledCount)
	checkUint("user.count", got.Data.Users.Count)
	checkUint("user.count.active", got.Data.Users.ActiveCount)
	checkUint("user.count.deleted", got.Data.Users.DeletedCount)
	checkUint("user.count.pending_invite", got.Data.Users.PendingInviteCount)
	checkUint("auth_token.count", got.Data.Authentication.Tokens.Count)
	checkString("auth_token.last_observed_at.max.time", got.Data.Authentication.Tokens.LastObservedTime)
	checkInt64("auth_token.last_observed_at.max.time_unix", got.Data.Authentication.Tokens.LastObservedTimeUnix)
	checkUint("authdomain.count", got.Data.Authentication.Domains.Count)
	checkUint("serviceaccount.count", got.Data.ServiceAccounts.Count)
	checkUint("serviceaccount.keys.count", got.Data.ServiceAccounts.KeyCount)
	checkUint("role.count", got.Data.Authorization.Roles.Count)
	checkString("license.id", got.Data.License.ID)
	checkString("license.plan.name", got.Data.License.PlanName)
	checkString("license.state.name", got.Data.License.StateName)
	checkString("license.free_until.time", got.Data.License.FreeUntil)
	checkString("config.sqlstore.provider", got.Data.Configuration.SQLStoreProvider)
	checkString("config.tokenizer.provider", got.Data.Configuration.TokenizerProvider)
	checkString("config.cache.provider", got.Data.Configuration.CacheProvider)

	for key, raw := range source {
		var actual uint64
		var exists, recognized bool
		var label string
		switch {
		case strings.HasPrefix(key, "rule.type.") && strings.HasSuffix(key, ".count"):
			label = strings.TrimSuffix(strings.TrimPrefix(key, "rule.type."), ".count")
			actual, exists = got.Data.Alerts.Rules.ByType[label]
			recognized = true
		case strings.HasPrefix(key, "alert.type.") && strings.HasSuffix(key, ".count"):
			label = strings.TrimSuffix(strings.TrimPrefix(key, "alert.type."), ".count")
			actual, exists = got.Data.Alerts.Rules.BySignal[label]
			recognized = true
		case strings.HasPrefix(key, "alertmanager.channel.type."):
			label = strings.TrimPrefix(key, "alertmanager.channel.type.")
			actual, exists = got.Data.Alerts.NotificationChannels.ByType[label]
			recognized = true
		case strings.HasPrefix(key, "savedview.source.") && strings.HasSuffix(key, ".count"):
			label = strings.TrimSuffix(strings.TrimPrefix(key, "savedview.source."), ".count")
			actual, exists = got.Data.Views.BySource[label]
			recognized = true
		case strings.HasPrefix(key, "authdomain.") && strings.HasSuffix(key, ".count") && key != "authdomain.count":
			label = strings.TrimSuffix(strings.TrimPrefix(key, "authdomain."), ".count")
			actual, exists = got.Data.Authentication.Domains.ByType[label]
			recognized = true
		case strings.HasPrefix(key, "role.") && strings.HasSuffix(key, ".count") && key != "role.count":
			label = strings.TrimSuffix(strings.TrimPrefix(key, "role."), ".count")
			actual, exists = got.Data.Authorization.Roles.ByType[label]
			recognized = true
		case strings.HasPrefix(key, "cloudintegration.") && strings.HasSuffix(key, ".connectedaccounts.count"):
			label = strings.TrimSuffix(strings.TrimPrefix(key, "cloudintegration."), ".connectedaccounts.count")
			provider, providerExists := got.Data.CloudIntegrations.Providers[label]
			if providerExists && provider.DataAvailable && provider.ConnectedAccounts != nil {
				actual, exists = *provider.ConnectedAccounts, true
			}
			recognized = true
		}
		if !recognized || !orgOverviewProjectionLabel(label) {
			continue
		}
		want, valid := rawUint64(raw)
		if !valid {
			if exists {
				t.Fatalf("invalid dynamic source field %q populated a typed projection", key)
			}
			markInvalid(key)
			continue
		}
		if !exists || actual != want {
			t.Fatalf("typed projection mismatch for dynamic source field %q", key)
		}
		projected[key] = struct{}{}
	}

	assertAvailability := func(key string, actual bool) {
		raw, exists := source[key]
		_, valid := rawUint64(raw)
		want := exists && valid
		if actual != want {
			t.Fatalf("availability mismatch for source field %q", key)
		}
	}
	assertAvailability("telemetry.logs.count", got.Data.Signals.Logs.Available)
	assertAvailability("telemetry.metrics.count", got.Data.Signals.Metrics.Available)
	assertAvailability("telemetry.traces.count", got.Data.Signals.Traces.Available)
	assertAvailability("dashboard.count", got.Data.Dashboards.Available)
	assertAvailability("rule.count", got.Data.Alerts.Rules.Available)
	assertAvailability("alert.firing.count", got.Data.Alerts.Runtime.Available)
	assertAvailability("alertmanager.channel.count", got.Data.Alerts.NotificationChannels.Available)
	assertAvailability("savedview.count", got.Data.Views.Available)
	assertAvailability("logs_pipeline.total.count", got.Data.LogPipelines.Available)
	assertAvailability("user.count", got.Data.Users.Available)
	assertAvailability("auth_token.count", got.Data.Authentication.Tokens.Available)
	assertAvailability("authdomain.count", got.Data.Authentication.Domains.Available)
	assertAvailability("serviceaccount.count", got.Data.ServiceAccounts.Available)
	assertAvailability("role.count", got.Data.Authorization.Roles.Available)
	assertLiveCloudAvailability(t, source, got)

	projectedFields := make([]string, 0, len(projected))
	for key := range projected {
		projectedFields = append(projectedFields, key)
	}
	invalidFields := make([]string, 0, len(invalid))
	for key := range invalid {
		invalidFields = append(invalidFields, key)
	}
	return projectedFields, invalidFields
}

func assertLiveCloudAvailability(t *testing.T, source map[string]json.RawMessage, got orgOverviewOutput) {
	t.Helper()
	availableProviders := 0
	for _, provider := range []string{"aws", "azure"} {
		key := "cloudintegration." + provider + ".connectedaccounts.count"
		raw, reported := source[key]
		_, valid := rawUint64(raw)
		actual, exists := got.Data.CloudIntegrations.Providers[provider]
		if !exists {
			t.Fatalf("cloud provider %q is absent from typed output", provider)
		}
		if reported && valid {
			availableProviders++
			if !actual.DataAvailable || actual.ConnectedAccounts == nil {
				t.Fatalf("reported cloud provider %q is unavailable in typed output", provider)
			}
		} else if actual.DataAvailable || actual.ConnectedAccounts != nil {
			t.Fatalf("unreported or invalid cloud provider %q is available in typed output", provider)
		}
	}
	want := "unavailable"
	if availableProviders == 1 {
		want = "partial"
	} else if availableProviders == 2 {
		want = "complete"
	}
	if got.Data.CloudIntegrations.SourceAvailability != want {
		t.Fatalf("cloud sourceAvailability=%q want=%q", got.Data.CloudIntegrations.SourceAvailability, want)
	}
}

func sortedLiveKeys(source map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
