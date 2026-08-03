package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type orgOverviewOutput struct {
	Status string          `json:"status"`
	Data   orgOverviewData `json:"data"`
}

type orgOverviewData struct {
	SourceStats       map[string]any               `json:"sourceStats" jsonschema:"Authoritative complete flat stats bag for this response. It contains every key and value reported by SigNoz, including backend-owned and future fields. Dotted keys and value shapes can evolve; a missing key means unreported, not zero."`
	Signals           orgSignalsOverview           `json:"signals"`
	Dashboards        orgDashboardsOverview        `json:"dashboards"`
	Alerts            orgAlertsOverview            `json:"alerts"`
	Views             orgViewsOverview             `json:"views"`
	LogPipelines      orgLogPipelinesOverview      `json:"logPipelines"`
	CloudIntegrations orgCloudIntegrationsOverview `json:"cloudIntegrations"`
	Users             orgUsersOverview             `json:"users"`
	Authentication    orgAuthenticationOverview    `json:"authentication"`
	ServiceAccounts   orgServiceAccountsOverview   `json:"serviceAccounts"`
	Authorization     orgAuthorizationOverview     `json:"authorization"`
	License           orgLicenseOverview           `json:"license"`
	Configuration     orgConfigurationOverview     `json:"configuration"`
	Metadata          orgOverviewMetadata          `json:"metadata"`
}

type orgSignalsOverview struct {
	Logs    orgSignalOverview        `json:"logs"`
	Metrics orgMetricsSignalOverview `json:"metrics"`
	Traces  orgSignalOverview        `json:"traces"`
}

type orgSignalOverview struct {
	Available            bool    `json:"available" jsonschema:"Whether the signal count was reported. A missing optional last-observed timestamp does not make the count unavailable."`
	Count                *uint64 `json:"count,omitempty" jsonschema:"All-time telemetry row count; logs count log records and traces count span-index rows."`
	LastObservedTime     *string `json:"lastObservedTime,omitempty" jsonschema:"Latest observed telemetry timestamp as reported by SigNoz."`
	LastObservedTimeUnix *int64  `json:"lastObservedTimeUnix,omitempty" jsonschema:"Latest observed telemetry timestamp in Unix seconds."`
}

type orgMetricsSignalOverview struct {
	Available            bool                             `json:"available" jsonschema:"Whether the metric-sample count was reported. A missing optional last-observed timestamp does not make the count unavailable."`
	Count                *uint64                          `json:"count,omitempty" jsonschema:"All-time metric sample row count, not a count of distinct metric names."`
	LastObservedTime     *string                          `json:"lastObservedTime,omitempty" jsonschema:"Latest observed metric timestamp as reported by SigNoz."`
	LastObservedTimeUnix *int64                           `json:"lastObservedTimeUnix,omitempty" jsonschema:"Latest observed metric timestamp in Unix seconds."`
	Infrastructure       orgMetricsInfrastructureOverview `json:"infrastructure"`
}

type orgMetricsInfrastructureOverview struct {
	SystemExists *bool `json:"systemExists,omitempty" jsonschema:"Whether system metrics exist. Both infrastructure flags are reported together on collector success; absence means unknown due to collector failure, not that system metrics do not exist, and is reported in metadata.incompleteGroups."`
	K8sExists    *bool `json:"k8sExists,omitempty" jsonschema:"Whether Kubernetes metrics exist. Both infrastructure flags are reported together on collector success; absence means unknown due to collector failure, not that Kubernetes metrics do not exist, and is reported in metadata.incompleteGroups."`
}

type orgDashboardsOverview struct {
	Available   bool                      `json:"available" jsonschema:"Whether the upstream collector reported the dashboard total count."`
	Count       *uint64                   `json:"count,omitempty"`
	PublicCount *uint64                   `json:"publicCount,omitempty"`
	Panels      orgDashboardPanelOverview `json:"panels"`
}

type orgDashboardPanelOverview struct {
	Count   *uint64 `json:"count,omitempty"`
	Logs    *uint64 `json:"logs,omitempty"`
	Metrics *uint64 `json:"metrics,omitempty"`
	Traces  *uint64 `json:"traces,omitempty"`
}

type orgAlertsOverview struct {
	Rules                orgAlertRulesOverview           `json:"rules"`
	Runtime              orgAlertRuntimeOverview         `json:"runtime"`
	NotificationChannels orgNotificationChannelsOverview `json:"notificationChannels"`
}

type orgAlertRuntimeOverview struct {
	Available         bool    `json:"available" jsonschema:"Whether the firing-rule count was reported. Optional last-fired timestamps can be absent when no rule has fired."`
	FiringRuleCount   *uint64 `json:"firingRuleCount,omitempty" jsonschema:"Number of currently firing alert rules, not alert instances."`
	LastFiredTime     *string `json:"lastFiredTime,omitempty"`
	LastFiredTimeUnix *int64  `json:"lastFiredTimeUnix,omitempty" jsonschema:"Most recent alert firing time in Unix seconds."`
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

type orgUsersOverview struct {
	Available          bool    `json:"available" jsonschema:"Whether the user total was reported."`
	Count              *uint64 `json:"count,omitempty" jsonschema:"Total users, including active, deleted, and pending-invite users."`
	ActiveCount        *uint64 `json:"activeCount,omitempty"`
	DeletedCount       *uint64 `json:"deletedCount,omitempty"`
	PendingInviteCount *uint64 `json:"pendingInviteCount,omitempty"`
}

type orgAuthenticationOverview struct {
	Tokens  orgAuthTokensOverview  `json:"tokens"`
	Domains orgAuthDomainsOverview `json:"domains"`
}

type orgAuthTokensOverview struct {
	Available            bool    `json:"available" jsonschema:"Whether an authentication-token count was reported. JWT normally omits the count; for the opaque tokenizer, false means unknown due to collector failure and is reported in metadata.incompleteGroups."`
	Count                *uint64 `json:"count,omitempty" jsonschema:"Authentication-token count reported by supported tokenizers. Omission is expected for JWT but indicates collector failure for the opaque tokenizer."`
	LastObservedTime     *string `json:"lastObservedTime,omitempty"`
	LastObservedTimeUnix *int64  `json:"lastObservedTimeUnix,omitempty" jsonschema:"Most recent token observation time in Unix seconds."`
}

type orgAuthDomainsOverview struct {
	Available bool              `json:"available" jsonschema:"Whether the authentication-domain total was reported."`
	Count     *uint64           `json:"count,omitempty"`
	ByType    map[string]uint64 `json:"byType" jsonschema:"Authentication-domain counts keyed by backend-reported domain type."`
}

type orgServiceAccountsOverview struct {
	Available bool    `json:"available" jsonschema:"Whether the service-account total was reported."`
	Count     *uint64 `json:"count,omitempty"`
	KeyCount  *uint64 `json:"keyCount,omitempty"`
}

type orgAuthorizationOverview struct {
	Roles orgRolesOverview `json:"roles"`
}

type orgRolesOverview struct {
	Available bool              `json:"available" jsonschema:"Whether the role total was reported."`
	Count     *uint64           `json:"count,omitempty"`
	ByType    map[string]uint64 `json:"byType" jsonschema:"Role counts keyed by backend-reported role type."`
}

type orgLicenseOverview struct {
	ID        *string `json:"id,omitempty"`
	PlanName  *string `json:"planName,omitempty"`
	StateName *string `json:"stateName,omitempty"`
	FreeUntil *string `json:"freeUntil,omitempty" jsonschema:"License free-until timestamp as reported by SigNoz."`
}

type orgConfigurationOverview struct {
	SQLStoreProvider  *string `json:"sqlStoreProvider,omitempty"`
	TokenizerProvider *string `json:"tokenizerProvider,omitempty"`
	CacheProvider     *string `json:"cacheProvider,omitempty"`
}

type orgOverviewMetadata struct {
	ReportedStatCount       int                          `json:"reportedStatCount" jsonschema:"Number of authoritative fields preserved in sourceStats."`
	ProjectedStatCount      int                          `json:"projectedStatCount" jsonschema:"Number of sourceStats fields also represented in the typed convenience groups."`
	UnprojectedStatCount    int                          `json:"unprojectedStatCount" jsonschema:"Number of sourceStats fields available only in the authoritative flat bag, including future fields."`
	ProjectionPartial       bool                         `json:"projectionPartial" jsonschema:"Whether an expected typed projection field was unreported or invalid. This does not claim endpoint-wide completeness."`
	IncompleteGroups        []orgOverviewIncompleteGroup `json:"incompleteGroups,omitempty" jsonschema:"Machine-readable recovery guidance for expected typed projection fields that could not be populated."`
	InvalidProjectionFields []string                     `json:"invalidProjectionFields,omitempty" jsonschema:"Source fields retained in sourceStats but omitted from typed groups because their values were invalid for the projection."`
}

type orgOverviewIncompleteGroup struct {
	Group      string   `json:"group"`
	Fields     []string `json:"fields" jsonschema:"Owned output field paths that could not be populated."`
	Reason     string   `json:"reason"`
	NextAction string   `json:"nextAction"`
	NextTools  []string `json:"nextTools,omitempty"`
}

func (h *Handler) RegisterOrgOverviewHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering organization overview handlers")

	tool := mcp.NewTool("signoz_get_org_overview",
		mcp.WithOutputSchema[orgOverviewOutput](),
		withReadOnlyToolAnnotations(),
		mcp.WithDescription("Get the current status and overall posture of the SigNoz deployment in one call. Returns typed telemetry, infrastructure, dashboard, alert, integration, access, license, and configuration summaries plus sourceStats with every reported stats field. Use dedicated list and query tools for exact inventories or time-windowed data. Missing fields are unreported, not zero; metadata.incompleteGroups provides recovery guidance for partial projections."),
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
		h.logUpstreamFailure(ctx, "Failed to get deployment overview", err)
		errorResult := upstreamError(err)
		var statusErr *signozclient.HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			const recovery = "recovery: Verify that the configured SigNoz URL points to an active deployment. If the deployment is reachable but this route is unavailable, use signoz_list_dashboards, signoz_list_alert_rules, signoz_list_notification_channels, signoz_list_views, signoz_list_metrics, signoz_list_alerts, signoz_search_logs, signoz_search_traces, or signoz_query_metrics as appropriate."
			for i, content := range errorResult.Content {
				if text, ok := content.(mcp.TextContent); ok {
					text.Text += "\n\n" + recovery
					errorResult.Content[i] = text
					break
				}
			}
		}
		return errorResult, nil
	}

	overview, driftFields, err := buildOrgOverview(result)
	if err != nil {
		h.logger.WarnContext(ctx,
			"Unexpected response shape from deployment stats endpoint",
			logpkg.ErrAttr(err))
		return upstreamError(fmt.Errorf("deployment stats response could not be interpreted: %w", err)), nil
	}
	if len(driftFields) > 0 {
		sort.Strings(driftFields)
		const maxLoggedFields = 20
		loggedFields := driftFields
		if len(loggedFields) > maxLoggedFields {
			loggedFields = loggedFields[:maxLoggedFields]
		}
		h.logger.WarnContext(ctx,
			"Deployment stats typed projection was partial or drifted; every reported source field was retained",
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
	sourceStats := make(map[string]any, len(stats))
	for key, raw := range stats {
		value, err := decodeRawJSON(raw)
		if err != nil {
			return orgOverviewOutput{}, nil, fmt.Errorf("decode source stat %q: %w", key, err)
		}
		sourceStats[key] = value
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
	projectedSet := make(map[string]struct{})
	markProjected := func(key string) { projectedSet[key] = struct{}{} }
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
			SourceStats: sourceStats,
			Dashboards:  orgDashboardsOverview{},
			Alerts: orgAlertsOverview{
				Rules: orgAlertRulesOverview{
					ByType:   map[string]uint64{},
					BySignal: map[string]uint64{},
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
			Authentication: orgAuthenticationOverview{
				Domains: orgAuthDomainsOverview{ByType: map[string]uint64{}},
			},
			Authorization: orgAuthorizationOverview{
				Roles: orgRolesOverview{ByType: map[string]uint64{}},
			},
		},
	}

	consumeUint64 := func(key string, target **uint64, group, field string) {
		if !existsValue(stats, key) {
			return
		}
		value, valid := rawUint64(stats[key])
		if !valid {
			markInvalid(key)
			markIncomplete(group, field)
			return
		}
		*target = &value
		markProjected(key)
	}
	consumeInt64 := func(key string, target **int64, group, field string) {
		if !existsValue(stats, key) {
			return
		}
		value, valid := rawInt64(stats[key])
		if !valid {
			markInvalid(key)
			markIncomplete(group, field)
			return
		}
		*target = &value
		markProjected(key)
	}
	consumeString := func(key string, target **string, group, field string) {
		if !existsValue(stats, key) {
			return
		}
		value, valid := rawString(stats[key])
		if !valid {
			markInvalid(key)
			markIncomplete(group, field)
			return
		}
		*target = &value
		markProjected(key)
	}
	consumeBool := func(key string, target **bool, group, field string) {
		if !existsValue(stats, key) {
			return
		}
		value, valid := rawBool(stats[key])
		if !valid {
			markInvalid(key)
			markIncomplete(group, field)
			return
		}
		*target = &value
		markProjected(key)
	}

	consumeUint64("telemetry.logs.count", &overview.Data.Signals.Logs.Count, "signals.logs", "signals.logs.count")
	consumeString("telemetry.logs.last_observed.time", &overview.Data.Signals.Logs.LastObservedTime, "signals.logs", "signals.logs.lastObservedTime")
	consumeInt64("telemetry.logs.last_observed.time_unix", &overview.Data.Signals.Logs.LastObservedTimeUnix, "signals.logs", "signals.logs.lastObservedTimeUnix")
	consumeUint64("telemetry.metrics.count", &overview.Data.Signals.Metrics.Count, "signals.metrics", "signals.metrics.count")
	consumeString("telemetry.metrics.last_observed.time", &overview.Data.Signals.Metrics.LastObservedTime, "signals.metrics", "signals.metrics.lastObservedTime")
	consumeInt64("telemetry.metrics.last_observed.time_unix", &overview.Data.Signals.Metrics.LastObservedTimeUnix, "signals.metrics", "signals.metrics.lastObservedTimeUnix")
	consumeBool("telemetry.metrics.system.exists", &overview.Data.Signals.Metrics.Infrastructure.SystemExists, "signals.metrics", "signals.metrics.infrastructure.systemExists")
	consumeBool("telemetry.metrics.k8s.exists", &overview.Data.Signals.Metrics.Infrastructure.K8sExists, "signals.metrics", "signals.metrics.infrastructure.k8sExists")
	consumeUint64("telemetry.traces.count", &overview.Data.Signals.Traces.Count, "signals.traces", "signals.traces.count")
	consumeString("telemetry.traces.last_observed.time", &overview.Data.Signals.Traces.LastObservedTime, "signals.traces", "signals.traces.lastObservedTime")
	consumeInt64("telemetry.traces.last_observed.time_unix", &overview.Data.Signals.Traces.LastObservedTimeUnix, "signals.traces", "signals.traces.lastObservedTimeUnix")
	consumeUint64("dashboard.count", &overview.Data.Dashboards.Count, "dashboards", "dashboards.count")
	consumeUint64("public_dashboard.count", &overview.Data.Dashboards.PublicCount, "dashboards", "dashboards.publicCount")
	consumeUint64("dashboard.panels.count", &overview.Data.Dashboards.Panels.Count, "dashboards", "dashboards.panels.count")
	consumeUint64("dashboard.panels.logs.count", &overview.Data.Dashboards.Panels.Logs, "dashboards", "dashboards.panels.logs")
	consumeUint64("dashboard.panels.metrics.count", &overview.Data.Dashboards.Panels.Metrics, "dashboards", "dashboards.panels.metrics")
	consumeUint64("dashboard.panels.traces.count", &overview.Data.Dashboards.Panels.Traces, "dashboards", "dashboards.panels.traces")

	consumeUint64("rule.count", &overview.Data.Alerts.Rules.Count, "alerts.rules", "alerts.rules.count")
	consumeUint64("alert.firing.count", &overview.Data.Alerts.Runtime.FiringRuleCount, "alerts.runtime", "alerts.runtime.firingRuleCount")
	consumeString("alert.last_fired.time", &overview.Data.Alerts.Runtime.LastFiredTime, "alerts.runtime", "alerts.runtime.lastFiredTime")
	consumeInt64("alert.last_fired.time_unix", &overview.Data.Alerts.Runtime.LastFiredTimeUnix, "alerts.runtime", "alerts.runtime.lastFiredTimeUnix")
	consumeUint64("alertmanager.channel.count", &overview.Data.Alerts.NotificationChannels.Count, "alerts.notificationChannels", "alerts.notificationChannels.count")

	consumeUint64("savedview.count", &overview.Data.Views.Count, "views", "views.count")
	consumeUint64("logs_pipeline.total.count", &overview.Data.LogPipelines.Count, "logPipelines", "logPipelines.count")
	consumeUint64("logs_pipeline.enabled.count", &overview.Data.LogPipelines.EnabledCount, "logPipelines", "logPipelines.enabledCount")
	consumeUint64("user.count", &overview.Data.Users.Count, "users", "users.count")
	consumeUint64("user.count.active", &overview.Data.Users.ActiveCount, "users", "users.activeCount")
	consumeUint64("user.count.deleted", &overview.Data.Users.DeletedCount, "users", "users.deletedCount")
	consumeUint64("user.count.pending_invite", &overview.Data.Users.PendingInviteCount, "users", "users.pendingInviteCount")
	consumeUint64("auth_token.count", &overview.Data.Authentication.Tokens.Count, "authentication.tokens", "authentication.tokens.count")
	consumeString("auth_token.last_observed_at.max.time", &overview.Data.Authentication.Tokens.LastObservedTime, "authentication.tokens", "authentication.tokens.lastObservedTime")
	consumeInt64("auth_token.last_observed_at.max.time_unix", &overview.Data.Authentication.Tokens.LastObservedTimeUnix, "authentication.tokens", "authentication.tokens.lastObservedTimeUnix")
	consumeUint64("authdomain.count", &overview.Data.Authentication.Domains.Count, "authentication.domains", "authentication.domains.count")
	consumeUint64("serviceaccount.count", &overview.Data.ServiceAccounts.Count, "serviceAccounts", "serviceAccounts.count")
	consumeUint64("serviceaccount.keys.count", &overview.Data.ServiceAccounts.KeyCount, "serviceAccounts", "serviceAccounts.keyCount")
	consumeUint64("role.count", &overview.Data.Authorization.Roles.Count, "authorization.roles", "authorization.roles.count")
	consumeString("license.id", &overview.Data.License.ID, "license", "license.id")
	consumeString("license.plan.name", &overview.Data.License.PlanName, "license", "license.planName")
	consumeString("license.state.name", &overview.Data.License.StateName, "license", "license.stateName")
	consumeString("license.free_until.time", &overview.Data.License.FreeUntil, "license", "license.freeUntil")
	consumeString("config.sqlstore.provider", &overview.Data.Configuration.SQLStoreProvider, "configuration", "configuration.sqlStoreProvider")
	consumeString("config.tokenizer.provider", &overview.Data.Configuration.TokenizerProvider, "configuration", "configuration.tokenizerProvider")
	consumeString("config.cache.provider", &overview.Data.Configuration.CacheProvider, "configuration", "configuration.cacheProvider")

	overview.Data.Signals.Logs.Available = overview.Data.Signals.Logs.Count != nil
	overview.Data.Signals.Metrics.Available = overview.Data.Signals.Metrics.Count != nil
	overview.Data.Signals.Traces.Available = overview.Data.Signals.Traces.Count != nil
	overview.Data.Dashboards.Available = overview.Data.Dashboards.Count != nil
	overview.Data.Alerts.Rules.Available = overview.Data.Alerts.Rules.Count != nil
	overview.Data.Alerts.Runtime.Available = overview.Data.Alerts.Runtime.FiringRuleCount != nil
	overview.Data.Alerts.NotificationChannels.Available = overview.Data.Alerts.NotificationChannels.Count != nil
	overview.Data.Views.Available = overview.Data.Views.Count != nil
	overview.Data.LogPipelines.Available = overview.Data.LogPipelines.Count != nil
	overview.Data.Users.Available = overview.Data.Users.Count != nil
	overview.Data.Authentication.Tokens.Available = overview.Data.Authentication.Tokens.Count != nil
	overview.Data.Authentication.Domains.Available = overview.Data.Authentication.Domains.Count != nil
	overview.Data.ServiceAccounts.Available = overview.Data.ServiceAccounts.Count != nil
	overview.Data.Authorization.Roles.Available = overview.Data.Authorization.Roles.Count != nil

	missingSentinels := []struct {
		key, group, field string
		missing           bool
	}{
		{key: "telemetry.logs.count", group: "signals.logs", field: "signals.logs.count", missing: overview.Data.Signals.Logs.Count == nil},
		{key: "telemetry.metrics.count", group: "signals.metrics", field: "signals.metrics.count", missing: overview.Data.Signals.Metrics.Count == nil},
		{key: "telemetry.metrics.system.exists", group: "signals.metrics", field: "signals.metrics.infrastructure.systemExists", missing: overview.Data.Signals.Metrics.Infrastructure.SystemExists == nil},
		{key: "telemetry.metrics.k8s.exists", group: "signals.metrics", field: "signals.metrics.infrastructure.k8sExists", missing: overview.Data.Signals.Metrics.Infrastructure.K8sExists == nil},
		{key: "telemetry.traces.count", group: "signals.traces", field: "signals.traces.count", missing: overview.Data.Signals.Traces.Count == nil},
		{key: "dashboard.count", group: "dashboards", field: "dashboards.count", missing: overview.Data.Dashboards.Count == nil},
		{key: "dashboard.panels.count", group: "dashboards", field: "dashboards.panels.count", missing: overview.Data.Dashboards.Panels.Count == nil},
		{key: "dashboard.panels.logs.count", group: "dashboards", field: "dashboards.panels.logs", missing: overview.Data.Dashboards.Panels.Logs == nil},
		{key: "dashboard.panels.metrics.count", group: "dashboards", field: "dashboards.panels.metrics", missing: overview.Data.Dashboards.Panels.Metrics == nil},
		{key: "dashboard.panels.traces.count", group: "dashboards", field: "dashboards.panels.traces", missing: overview.Data.Dashboards.Panels.Traces == nil},
		{key: "rule.count", group: "alerts.rules", field: "alerts.rules.count", missing: overview.Data.Alerts.Rules.Count == nil},
		{key: "alert.firing.count", group: "alerts.runtime", field: "alerts.runtime.firingRuleCount", missing: overview.Data.Alerts.Runtime.FiringRuleCount == nil},
		{key: "alertmanager.channel.count", group: "alerts.notificationChannels", field: "alerts.notificationChannels.count", missing: overview.Data.Alerts.NotificationChannels.Count == nil},
		{key: "savedview.count", group: "views", field: "views.count", missing: overview.Data.Views.Count == nil},
		{key: "logs_pipeline.total.count", group: "logPipelines", field: "logPipelines.count", missing: overview.Data.LogPipelines.Count == nil},
		{key: "logs_pipeline.enabled.count", group: "logPipelines", field: "logPipelines.enabledCount", missing: overview.Data.LogPipelines.EnabledCount == nil},
		{key: "user.count", group: "users", field: "users.count", missing: overview.Data.Users.Count == nil},
		{key: "user.count.active", group: "users", field: "users.activeCount", missing: overview.Data.Users.ActiveCount == nil},
		{key: "user.count.deleted", group: "users", field: "users.deletedCount", missing: overview.Data.Users.DeletedCount == nil},
		{key: "user.count.pending_invite", group: "users", field: "users.pendingInviteCount", missing: overview.Data.Users.PendingInviteCount == nil},
		{key: "authdomain.count", group: "authentication.domains", field: "authentication.domains.count", missing: overview.Data.Authentication.Domains.Count == nil},
		{key: "serviceaccount.count", group: "serviceAccounts", field: "serviceAccounts.count", missing: overview.Data.ServiceAccounts.Count == nil},
		{key: "serviceaccount.keys.count", group: "serviceAccounts", field: "serviceAccounts.keyCount", missing: overview.Data.ServiceAccounts.KeyCount == nil},
		{key: "role.count", group: "authorization.roles", field: "authorization.roles.count", missing: overview.Data.Authorization.Roles.Count == nil},
		{key: "config.sqlstore.provider", group: "configuration", field: "configuration.sqlStoreProvider", missing: overview.Data.Configuration.SQLStoreProvider == nil},
		{key: "config.tokenizer.provider", group: "configuration", field: "configuration.tokenizerProvider", missing: overview.Data.Configuration.TokenizerProvider == nil},
		{key: "config.cache.provider", group: "configuration", field: "configuration.cacheProvider", missing: overview.Data.Configuration.CacheProvider == nil},
	}
	if overview.Data.Configuration.TokenizerProvider != nil {
		switch *overview.Data.Configuration.TokenizerProvider {
		case "opaque":
			missingSentinels = append(missingSentinels, struct {
				key, group, field string
				missing           bool
			}{
				key:     "auth_token.count",
				group:   "authentication.tokens",
				field:   "authentication.tokens.count",
				missing: overview.Data.Authentication.Tokens.Count == nil,
			})
		case "jwt":
		default:
			markDrift("config.tokenizer.provider")
		}
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
		case strings.HasPrefix(key, "authdomain.") && strings.HasSuffix(key, ".count") && key != "authdomain.count":
			target = overview.Data.Authentication.Domains.ByType
			label = strings.TrimSuffix(strings.TrimPrefix(key, "authdomain."), ".count")
			group = "authentication.domains"
			fieldPrefix = "authentication.domains.byType."
		case strings.HasPrefix(key, "role.") && strings.HasSuffix(key, ".count") && key != "role.count":
			target = overview.Data.Authorization.Roles.ByType
			label = strings.TrimSuffix(strings.TrimPrefix(key, "role."), ".count")
			group = "authorization.roles"
			fieldPrefix = "authorization.roles.byType."
		case strings.HasPrefix(key, "cloudintegration.") && strings.HasSuffix(key, ".connectedaccounts.count"):
			label = strings.TrimSuffix(strings.TrimPrefix(key, "cloudintegration."), ".connectedaccounts.count")
			if !orgOverviewProjectionLabel(label) {
				continue
			}
			value, valid := rawUint64(stats[key])
			if !valid {
				markInvalid(key)
				markIncomplete("cloudIntegrations", "cloudIntegrations.providers."+label+".connectedAccounts")
				continue
			}
			overview.Data.CloudIntegrations.Providers[label] = orgCloudProviderOverview{
				DataAvailable:     true,
				ConnectedAccounts: &value,
			}
			markProjected(key)
			continue
		}
		if target == nil {
			continue
		}
		if !orgOverviewProjectionLabel(label) {
			continue
		}
		value, valid := rawUint64(stats[key])
		if !valid {
			markInvalid(key)
			markIncomplete(group, fieldPrefix+label)
			continue
		}
		target[label] = value
		markProjected(key)
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

	invalidStatFields := make([]string, 0, len(invalidSet))
	for key := range invalidSet {
		invalidStatFields = append(invalidStatFields, key)
	}
	sort.Strings(invalidStatFields)
	incompleteGroups := buildOrgOverviewIncompleteGroups(incompleteFields)
	overview.Data.Metadata = orgOverviewMetadata{
		ReportedStatCount:       len(sourceStats),
		ProjectedStatCount:      len(projectedSet),
		UnprojectedStatCount:    len(sourceStats) - len(projectedSet),
		ProjectionPartial:       len(incompleteGroups) > 0,
		IncompleteGroups:        incompleteGroups,
		InvalidProjectionFields: invalidStatFields,
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
	case "signals.logs":
		return "The log count was not reported or was invalid.",
			"Use signoz_search_logs for a time-windowed ingestion check.",
			[]string{"signoz_search_logs"}
	case "signals.metrics":
		return "One or more metric or infrastructure stats were not reported or were invalid.",
			"Use signoz_list_metrics to inspect the metric catalog and signoz_query_metrics for time-windowed values.",
			[]string{"signoz_list_metrics", "signoz_query_metrics"}
	case "signals.traces":
		return "The trace count was not reported or was invalid.",
			"Use signoz_search_traces for a time-windowed ingestion check.",
			[]string{"signoz_search_traces"}
	case "dashboards":
		return "One or more dashboard stats were not reported or were invalid.",
			"Use signoz_list_dashboards for the exact inventory and signoz_get_dashboard for panel details.",
			[]string{"signoz_list_dashboards", "signoz_get_dashboard"}
	case "alerts.rules":
		return "One or more configured alert-rule stats were not reported or were invalid.",
			"Use signoz_list_alert_rules for the exact configured-rule inventory.",
			[]string{"signoz_list_alert_rules"}
	case "alerts.runtime":
		return "The firing-rule runtime stat was not reported or was invalid.",
			"Use signoz_list_alerts for current firing, silenced, or inhibited alert instances.",
			[]string{"signoz_list_alerts"}
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
			"Inspect Log Pipelines in SigNoz and rerun the overview after resolving collector access.",
			nil
	case "cloudIntegrations":
		return "One or more current cloud-provider stats were not reported or were invalid.",
			"Inspect Cloud Integrations in SigNoz for the unavailable provider, then rerun this overview after resolving provider access.",
			nil
	case "users":
		return "One or more user stats were not reported or were invalid.",
			"Inspect user management in SigNoz and rerun the overview after resolving collector access.",
			nil
	case "authentication.tokens", "authentication.domains":
		return "One or more authentication stats were not reported or were invalid.",
			"Inspect authentication settings in SigNoz and rerun the overview after resolving collector access.",
			nil
	case "serviceAccounts":
		return "One or more service-account stats were not reported or were invalid.",
			"Inspect service accounts in SigNoz and rerun the overview after resolving collector access.",
			nil
	case "authorization.roles":
		return "One or more role stats were not reported or were invalid.",
			"Inspect roles in SigNoz and rerun the overview after resolving collector access.",
			nil
	case "license":
		return "One or more license stats were invalid for the typed projection.",
			"Inspect license information in SigNoz; the original reported value remains available in sourceStats.",
			nil
	case "configuration":
		return "One or more deployment-configuration stats were not reported or were invalid.",
			"Inspect the SigNoz deployment configuration and server logs, then rerun the overview after resolving the collector issue.",
			nil
	default:
		return "One or more deployment stats were not reported or were invalid for the typed projection.",
			"Inspect sourceStats and the affected area in SigNoz before treating the typed group as complete.",
			nil
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

func rawInt64(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	return value, err == nil
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func rawBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

func orgOverviewProjectionLabel(label string) bool {
	return label != "" && !strings.Contains(label, ".")
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
