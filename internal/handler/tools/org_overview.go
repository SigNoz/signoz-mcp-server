package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type orgOverviewOutput struct {
	Status string          `json:"status"`
	Data   orgOverviewData `json:"data"`
}

type orgOverviewData struct {
	Signals           orgRuntimeAvailability       `json:"signals"`
	Dashboards        orgDashboardsOverview        `json:"dashboards"`
	Alerts            orgAlertsOverview            `json:"alerts"`
	Views             orgViewsOverview             `json:"views"`
	LogPipelines      orgLogPipelinesOverview      `json:"logPipelines"`
	CloudIntegrations orgCloudIntegrationsOverview `json:"cloudIntegrations"`
	Metadata          orgOverviewMetadata          `json:"metadata"`
	AdditionalStats   map[string]any               `json:"additionalStats,omitempty" jsonschema:"Compatible new organization-scoped observability stats not yet modeled by the owned output. Known or invalid fields never appear here."`
}

type orgRuntimeAvailability struct {
	Available bool     `json:"available"`
	Reason    string   `json:"reason"`
	NextTools []string `json:"nextTools" jsonschema:"Tenant-scoped tools to use instead for the unavailable runtime posture."`
}

type orgDashboardsOverview struct {
	Available   bool                      `json:"available" jsonschema:"Whether the upstream collector reported the dashboard total count."`
	Count       *uint64                   `json:"count,omitempty"`
	PublicCount *uint64                   `json:"publicCount,omitempty"`
	Panels      orgDashboardPanelOverview `json:"panels"`
}

type orgDashboardPanelOverview struct {
	Coverage string  `json:"coverage" jsonschema:"Coverage of the panel counters. legacyV1WidgetsOnly excludes v2/Perses panels and counts legacy builder-query entries by signal."`
	Count    *uint64 `json:"count,omitempty"`
	Logs     *uint64 `json:"logs,omitempty"`
	Metrics  *uint64 `json:"metrics,omitempty"`
	Traces   *uint64 `json:"traces,omitempty"`
}

type orgAlertsOverview struct {
	Rules                orgAlertRulesOverview           `json:"rules"`
	Runtime              orgRuntimeAvailability          `json:"runtime"`
	NotificationChannels orgNotificationChannelsOverview `json:"notificationChannels"`
}

type orgAlertRulesOverview struct {
	Available bool              `json:"available" jsonschema:"Whether the upstream collector reported the configured alert-rule total count."`
	Count     *uint64           `json:"count,omitempty"`
	ByType    map[string]uint64 `json:"byType"`
	BySignal  map[string]uint64 `json:"bySignal"`
}

type orgNotificationChannelsOverview struct {
	Available bool              `json:"available" jsonschema:"Whether the upstream collector reported the notification-channel total count."`
	Count     *uint64           `json:"count,omitempty"`
	ByType    map[string]uint64 `json:"byType"`
}

type orgViewsOverview struct {
	Available bool              `json:"available" jsonschema:"Whether the upstream collector reported the saved-view total count."`
	Count     *uint64           `json:"count,omitempty"`
	BySource  map[string]uint64 `json:"bySource"`
}

type orgLogPipelinesOverview struct {
	Available    bool    `json:"available" jsonschema:"Whether the upstream collector reported the log-pipeline total count. enabledCount can still be unavailable independently."`
	Count        *uint64 `json:"count,omitempty"`
	EnabledCount *uint64 `json:"enabledCount,omitempty"`
}

type orgCloudIntegrationsOverview struct {
	SourceAvailability string                              `json:"sourceAvailability" jsonschema:"Completeness of current AWS and Azure stats: complete when both providers were reported, partial when one was reported, and unavailable when neither was reported. Unavailable can mean the feature is unsupported or edition-gated, or that both provider queries failed; it never means zero configured integrations."`
	Providers          map[string]orgCloudProviderOverview `json:"providers" jsonschema:"Per-provider data availability and connected-account counts. Provider absence is never interpreted as zero."`
}

type orgCloudProviderOverview struct {
	DataAvailable     bool    `json:"dataAvailable" jsonschema:"Whether the upstream collector reported this provider's count."`
	ConnectedAccounts *uint64 `json:"connectedAccounts,omitempty" jsonschema:"Accounts not removed that have an account ID and at least one agent report; this does not assert a recent check-in or enabled integration service."`
}

type orgOverviewMetadata struct {
	ReportedStatCount               int                          `json:"reportedStatCount" jsonschema:"Number of fields in the upstream stats bag before tenant-scope filtering."`
	IncludedStatCount               int                          `json:"includedStatCount" jsonschema:"Number of organization-scoped fields represented in owned output fields or additionalStats."`
	OmittedStatCount                int                          `json:"omittedStatCount" jsonschema:"Number of upstream fields withheld because they are deployment-wide, unrelated metadata, or invalid known stats."`
	ExcludedDeploymentWideStatCount int                          `json:"excludedDeploymentWideStatCount" jsonschema:"Number of deployment-wide telemetry or alert-runtime fields intentionally withheld from this organization result."`
	AdditionalStatCount             int                          `json:"additionalStatCount" jsonschema:"Number of compatible new organization-scoped fields preserved under additionalStats."`
	Partial                         bool                         `json:"partial" jsonschema:"Whether one or more expected organization-scoped stats were not reported or were invalid."`
	IncompleteGroups                []orgOverviewIncompleteGroup `json:"incompleteGroups,omitempty" jsonschema:"Machine-readable recovery guidance for expected organization-scoped result fields that could not be populated."`
	InvalidStatFields               []string                     `json:"invalidStatFields,omitempty" jsonschema:"Known organization-scoped upstream stat fields ignored because their values were invalid; these values never appear in additionalStats."`
}

type orgOverviewIncompleteGroup struct {
	Group      string   `json:"group"`
	Fields     []string `json:"fields" jsonschema:"Owned output field paths that could not be populated."`
	Reason     string   `json:"reason"`
	NextAction string   `json:"nextAction"`
	NextTools  []string `json:"nextTools,omitempty"`
}

const (
	deploymentWideSignalsReason = "The upstream signal stats collectors are deployment-wide rather than organization-scoped; use tenant-scoped signal tools instead."
	deploymentWideAlertsReason  = "The upstream alert-runtime stats collector is deployment-wide rather than organization-scoped; use signoz_list_alerts instead."
)

func (h *Handler) RegisterOrgOverviewHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering organization overview handlers")

	tool := mcp.NewTool("signoz_get_org_overview",
		mcp.WithOutputSchema[orgOverviewOutput](),
		withReadOnlyToolAnnotations(),
		mcp.WithDescription("Use this when the user needs a one-call organization posture snapshot before inspecting specific resources. It returns organization-scoped dashboard, configured alert-rule, notification-channel, saved-view, log-pipeline, and cloud-integration counts. It intentionally excludes telemetry freshness/counts and alert firing runtime because those upstream collectors are deployment-wide; use signoz_search_logs, signoz_search_traces, or signoz_list_metrics for tenant-scoped ingestion checks. Use entity-specific list/get tools for names, IDs, definitions, or exhaustive membership. Missing fields mean unknown, not zero; cloud-provider availability is explicit, and dashboard panel counts cover legacy widgets only. When partial, metadata.incompleteGroups supplies affected paths, recovery guidance, and fallback tools where available. Example request: 'Summarize this workspace's observability setup before we configure alerts.'"),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
	)

	h.addTool(s, tool, h.handleGetOrgOverview)
}

func (h *Handler) handleGetOrgOverview(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_org_overview")
	result, err := client.GetOrgOverview(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get organization overview", err)
		return upstreamError(err), nil
	}

	overview, driftFields, err := buildOrgOverview(result)
	if err != nil {
		h.logger.WarnContext(ctx,
			"Unexpected response shape from organization stats endpoint; refusing an unfiltered fallback",
			logpkg.ErrAttr(err))
		return upstreamError(fmt.Errorf("organization stats response could not be safely interpreted: %w", err)), nil
	}
	if len(driftFields) > 0 {
		sort.Strings(driftFields)
		const maxLoggedFields = 20
		loggedFields := driftFields
		if len(loggedFields) > maxLoggedFields {
			loggedFields = loggedFields[:maxLoggedFields]
		}
		h.logger.WarnContext(ctx,
			"Organization stats response was partial or drifted; compatible reported fields were retained safely",
			slog.Int("fieldCount", len(driftFields)),
			slog.Any("fields", loggedFields))
	}

	payload, err := json.Marshal(overview)
	if err != nil {
		return InternalErrorResult(fmt.Sprintf("failed to encode organization overview: %v", err)), nil
	}
	return structuredResult(payload), nil
}

func buildOrgOverview(payload []byte) (orgOverviewOutput, []string, error) {
	var envelope struct {
		Status json.RawMessage `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return orgOverviewOutput{}, nil, fmt.Errorf("decode response envelope: %w", err)
	}

	var stats map[string]json.RawMessage
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return orgOverviewOutput{}, nil, fmt.Errorf("response data object is missing")
	}
	if err := json.Unmarshal(envelope.Data, &stats); err != nil || stats == nil {
		return orgOverviewOutput{}, nil, fmt.Errorf("decode response data object: %w", err)
	}

	driftSet := make(map[string]struct{})
	markDrift := func(key string) { driftSet[key] = struct{}{} }
	invalidSet := make(map[string]struct{})
	markInvalid := func(key string) {
		invalidSet[key] = struct{}{}
		markDrift(key)
	}
	incompleteFields := make(map[string]map[string]struct{})
	markIncomplete := func(group, field string) {
		if incompleteFields[group] == nil {
			incompleteFields[group] = make(map[string]struct{})
		}
		incompleteFields[group][field] = struct{}{}
	}
	if len(envelope.Status) == 0 {
		markDrift("status")
	} else {
		var status string
		if err := json.Unmarshal(envelope.Status, &status); err != nil {
			markDrift("status")
		} else if status != "success" {
			return orgOverviewOutput{}, nil, fmt.Errorf("unexpected response status %q", boundedErrorDetail(status))
		}
	}

	overview := orgOverviewOutput{
		Status: "success",
		Data: orgOverviewData{
			Signals: orgRuntimeAvailability{
				Available: false,
				Reason:    deploymentWideSignalsReason,
				NextTools: []string{"signoz_search_logs", "signoz_search_traces", "signoz_list_metrics"},
			},
			Dashboards: orgDashboardsOverview{
				Panels: orgDashboardPanelOverview{Coverage: "legacyV1WidgetsOnly"},
			},
			Alerts: orgAlertsOverview{
				Rules: orgAlertRulesOverview{
					ByType:   map[string]uint64{},
					BySignal: map[string]uint64{},
				},
				Runtime: orgRuntimeAvailability{
					Available: false,
					Reason:    deploymentWideAlertsReason,
					NextTools: []string{"signoz_list_alerts"},
				},
				NotificationChannels: orgNotificationChannelsOverview{ByType: map[string]uint64{}},
			},
			Views: orgViewsOverview{BySource: map[string]uint64{}},
			CloudIntegrations: orgCloudIntegrationsOverview{
				Providers: map[string]orgCloudProviderOverview{
					"aws":   {},
					"azure": {},
				},
			},
			AdditionalStats: map[string]any{},
		},
	}
	reportedCount := len(stats)
	excludedDeploymentWideCount := 0
	for key := range stats {
		if isDeploymentWideOrgStat(key) {
			excludedDeploymentWideCount++
		}
	}
	mappedCount := 0

	consumeUint64 := func(key string, target **uint64, group, field string) {
		value, valid := rawUint64(stats[key])
		if !existsValue(stats, key) {
			return
		}
		if !valid {
			markInvalid(key)
			markIncomplete(group, field)
			delete(stats, key)
			return
		}
		*target = &value
		delete(stats, key)
		mappedCount++
	}
	consumeUint64("dashboard.count", &overview.Data.Dashboards.Count, "dashboards", "dashboards.count")
	consumeUint64("public_dashboard.count", &overview.Data.Dashboards.PublicCount, "dashboards", "dashboards.publicCount")
	consumeUint64("dashboard.panels.count", &overview.Data.Dashboards.Panels.Count, "dashboards", "dashboards.panels.count")
	consumeUint64("dashboard.panels.logs.count", &overview.Data.Dashboards.Panels.Logs, "dashboards", "dashboards.panels.logs")
	consumeUint64("dashboard.panels.metrics.count", &overview.Data.Dashboards.Panels.Metrics, "dashboards", "dashboards.panels.metrics")
	consumeUint64("dashboard.panels.traces.count", &overview.Data.Dashboards.Panels.Traces, "dashboards", "dashboards.panels.traces")

	consumeUint64("rule.count", &overview.Data.Alerts.Rules.Count, "alerts.rules", "alerts.rules.count")
	consumeUint64("alertmanager.channel.count", &overview.Data.Alerts.NotificationChannels.Count, "alerts.notificationChannels", "alerts.notificationChannels.count")

	consumeUint64("savedview.count", &overview.Data.Views.Count, "views", "views.count")
	consumeUint64("logs_pipeline.total.count", &overview.Data.LogPipelines.Count, "logPipelines", "logPipelines.count")
	consumeUint64("logs_pipeline.enabled.count", &overview.Data.LogPipelines.EnabledCount, "logPipelines", "logPipelines.enabledCount")

	overview.Data.Dashboards.Available = overview.Data.Dashboards.Count != nil
	overview.Data.Alerts.Rules.Available = overview.Data.Alerts.Rules.Count != nil
	overview.Data.Alerts.NotificationChannels.Available = overview.Data.Alerts.NotificationChannels.Count != nil
	overview.Data.Views.Available = overview.Data.Views.Count != nil
	overview.Data.LogPipelines.Available = overview.Data.LogPipelines.Count != nil

	missingSentinels := []struct {
		key, group, field string
		missing           bool
	}{
		{key: "dashboard.count", group: "dashboards", field: "dashboards.count", missing: overview.Data.Dashboards.Count == nil},
		{key: "dashboard.panels.count", group: "dashboards", field: "dashboards.panels.count", missing: overview.Data.Dashboards.Panels.Count == nil},
		{key: "dashboard.panels.logs.count", group: "dashboards", field: "dashboards.panels.logs", missing: overview.Data.Dashboards.Panels.Logs == nil},
		{key: "dashboard.panels.metrics.count", group: "dashboards", field: "dashboards.panels.metrics", missing: overview.Data.Dashboards.Panels.Metrics == nil},
		{key: "dashboard.panels.traces.count", group: "dashboards", field: "dashboards.panels.traces", missing: overview.Data.Dashboards.Panels.Traces == nil},
		{key: "rule.count", group: "alerts.rules", field: "alerts.rules.count", missing: overview.Data.Alerts.Rules.Count == nil},
		{key: "alertmanager.channel.count", group: "alerts.notificationChannels", field: "alerts.notificationChannels.count", missing: overview.Data.Alerts.NotificationChannels.Count == nil},
		{key: "savedview.count", group: "views", field: "views.count", missing: overview.Data.Views.Count == nil},
		{key: "logs_pipeline.total.count", group: "logPipelines", field: "logPipelines.count", missing: overview.Data.LogPipelines.Count == nil},
		{key: "logs_pipeline.enabled.count", group: "logPipelines", field: "logPipelines.enabledCount", missing: overview.Data.LogPipelines.EnabledCount == nil},
	}
	for _, sentinel := range missingSentinels {
		if sentinel.missing {
			markDrift(sentinel.key)
			markIncomplete(sentinel.group, sentinel.field)
		}
	}

	remainingKeys := make([]string, 0, len(stats))
	for key := range stats {
		remainingKeys = append(remainingKeys, key)
	}
	sort.Strings(remainingKeys)
	for _, key := range remainingKeys {
		var target map[string]uint64
		var label, group, fieldPrefix string
		switch {
		case strings.HasPrefix(key, "rule.type.") && strings.HasSuffix(key, ".count"):
			target = overview.Data.Alerts.Rules.ByType
			label = strings.TrimSuffix(strings.TrimPrefix(key, "rule.type."), ".count")
			group = "alerts.rules"
			fieldPrefix = "alerts.rules.byType."
		case strings.HasPrefix(key, "alert.type.") && strings.HasSuffix(key, ".count"):
			target = overview.Data.Alerts.Rules.BySignal
			label = strings.TrimSuffix(strings.TrimPrefix(key, "alert.type."), ".count")
			group = "alerts.rules"
			fieldPrefix = "alerts.rules.bySignal."
		case strings.HasPrefix(key, "alertmanager.channel.type."):
			target = overview.Data.Alerts.NotificationChannels.ByType
			label = strings.TrimPrefix(key, "alertmanager.channel.type.")
			group = "alerts.notificationChannels"
			fieldPrefix = "alerts.notificationChannels.byType."
		case strings.HasPrefix(key, "savedview.source.") && strings.HasSuffix(key, ".count"):
			target = overview.Data.Views.BySource
			label = strings.TrimSuffix(strings.TrimPrefix(key, "savedview.source."), ".count")
			group = "views"
			fieldPrefix = "views.bySource."
		case strings.HasPrefix(key, "cloudintegration.") && strings.HasSuffix(key, ".connectedaccounts.count"):
			label = strings.TrimSuffix(strings.TrimPrefix(key, "cloudintegration."), ".connectedaccounts.count")
			value, valid := rawUint64(stats[key])
			if !valid || label == "" {
				markInvalid(key)
				field := "cloudIntegrations.providers"
				if label != "" {
					field += "." + label + ".connectedAccounts"
				}
				markIncomplete("cloudIntegrations", field)
				delete(stats, key)
				continue
			}
			overview.Data.CloudIntegrations.Providers[label] = orgCloudProviderOverview{
				DataAvailable:     true,
				ConnectedAccounts: &value,
			}
			delete(stats, key)
			mappedCount++
			continue
		}
		if target == nil {
			continue
		}
		if label == "" {
			markInvalid(key)
			markIncomplete(group, strings.TrimSuffix(fieldPrefix, "."))
			delete(stats, key)
			continue
		}
		value, valid := rawUint64(stats[key])
		if !valid {
			markInvalid(key)
			markIncomplete(group, fieldPrefix+label)
			delete(stats, key)
			continue
		}
		target[label] = value
		delete(stats, key)
		mappedCount++
	}

	availableCloudProviders := 0
	missingCloudProviders := make([]string, 0, 2)
	for _, provider := range []string{"aws", "azure"} {
		if cloudProviderAvailable(overview.Data.CloudIntegrations, provider) {
			availableCloudProviders++
		} else {
			missingCloudProviders = append(missingCloudProviders, provider)
		}
	}
	switch availableCloudProviders {
	case 2:
		overview.Data.CloudIntegrations.SourceAvailability = "complete"
	case 1:
		overview.Data.CloudIntegrations.SourceAvailability = "partial"
		for _, provider := range missingCloudProviders {
			markDrift("cloudintegration." + provider + ".connectedaccounts.count")
			markIncomplete("cloudIntegrations", "cloudIntegrations.providers."+provider+".connectedAccounts")
		}
	default:
		overview.Data.CloudIntegrations.SourceAvailability = "unavailable"
	}

	for _, key := range remainingKeys {
		raw, exists := stats[key]
		if !exists || !isOrgOverviewStat(key) {
			continue
		}
		value, err := decodeRawJSON(raw)
		if err != nil {
			markDrift(key)
			continue
		}
		overview.Data.AdditionalStats[key] = value
		markDrift(key)
	}
	if len(overview.Data.AdditionalStats) == 0 {
		overview.Data.AdditionalStats = nil
	}

	additionalCount := len(overview.Data.AdditionalStats)
	includedCount := mappedCount + additionalCount
	invalidStatFields := make([]string, 0, len(invalidSet))
	for key := range invalidSet {
		invalidStatFields = append(invalidStatFields, key)
	}
	sort.Strings(invalidStatFields)
	incompleteGroups := buildOrgOverviewIncompleteGroups(incompleteFields)
	overview.Data.Metadata = orgOverviewMetadata{
		ReportedStatCount:               reportedCount,
		IncludedStatCount:               includedCount,
		OmittedStatCount:                reportedCount - includedCount,
		ExcludedDeploymentWideStatCount: excludedDeploymentWideCount,
		AdditionalStatCount:             additionalCount,
		Partial:                         len(incompleteGroups) > 0,
		IncompleteGroups:                incompleteGroups,
		InvalidStatFields:               invalidStatFields,
	}

	driftFields := make([]string, 0, len(driftSet))
	for key := range driftSet {
		driftFields = append(driftFields, key)
	}
	return overview, driftFields, nil
}

func buildOrgOverviewIncompleteGroups(fieldsByGroup map[string]map[string]struct{}) []orgOverviewIncompleteGroup {
	groups := make([]string, 0, len(fieldsByGroup))
	for group := range fieldsByGroup {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	result := make([]orgOverviewIncompleteGroup, 0, len(groups))
	for _, group := range groups {
		fields := make([]string, 0, len(fieldsByGroup[group]))
		for field := range fieldsByGroup[group] {
			fields = append(fields, field)
		}
		sort.Strings(fields)

		reason, nextAction, nextTools := orgOverviewRecovery(group)
		result = append(result, orgOverviewIncompleteGroup{
			Group:      group,
			Fields:     fields,
			Reason:     reason,
			NextAction: nextAction,
			NextTools:  nextTools,
		})
	}
	return result
}

func orgOverviewRecovery(group string) (string, string, []string) {
	switch group {
	case "dashboards":
		return "One or more dashboard stats were not reported or were invalid.",
			"Use signoz_list_dashboards for the exact inventory and signoz_get_dashboard for panel details.",
			[]string{"signoz_list_dashboards", "signoz_get_dashboard"}
	case "alerts.rules":
		return "One or more configured alert-rule stats were not reported or were invalid.",
			"Use signoz_list_alert_rules for the exact configured-rule inventory.",
			[]string{"signoz_list_alert_rules"}
	case "alerts.notificationChannels":
		return "One or more notification-channel stats were not reported or were invalid.",
			"Use signoz_list_notification_channels for the exact channel inventory.",
			[]string{"signoz_list_notification_channels"}
	case "views":
		return "One or more saved-view stats were not reported or were invalid.",
			"Use signoz_list_views for the exact saved-view inventory.",
			[]string{"signoz_list_views"}
	case "logPipelines":
		return "One or more log-pipeline stats were not reported or were invalid.",
			"Retry signoz_get_org_overview; if the fields remain unavailable, inspect Log Pipelines in SigNoz.",
			[]string{"signoz_get_org_overview"}
	case "cloudIntegrations":
		return "One or more current cloud-provider stats were not reported or were invalid.",
			"Inspect Cloud Integrations in SigNoz for the unavailable provider, then rerun this overview after resolving provider access.",
			nil
	default:
		return "One or more organization stats were not reported or were invalid.",
			"Retry signoz_get_org_overview before treating the group as complete.",
			[]string{"signoz_get_org_overview"}
	}
}

func existsValue(values map[string]json.RawMessage, key string) bool {
	_, ok := values[key]
	return ok
}

func cloudProviderAvailable(overview orgCloudIntegrationsOverview, provider string) bool {
	value, exists := overview.Providers[provider]
	return exists && value.DataAvailable && value.ConnectedAccounts != nil
}

func rawUint64(raw json.RawMessage) (uint64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(number.String(), 10, 64)
	return value, err == nil
}

func decodeRawJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return value, nil
}

func isOrgOverviewStat(key string) bool {
	for _, prefix := range []string{
		"dashboard.",
		"public_dashboard.",
		"rule.",
		"alert.type.",
		"alertmanager.channel.",
		"savedview.",
		"logs_pipeline.",
		"cloudintegration.",
	} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func isDeploymentWideOrgStat(key string) bool {
	return strings.HasPrefix(key, "telemetry.") ||
		key == "alert.firing.count" ||
		key == "alert.last_fired.time" ||
		key == "alert.last_fired.time_unix"
}
