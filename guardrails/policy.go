// Package guardrails contains the centrally reviewed policy enforced by the
// repository's package-local TestGuardrail_* tests.
package guardrails

const (
	MaxServerInstructionsBytes   = 2048
	MaxServerPreambleBytes       = 512
	MaxToolDescriptionBytes      = 1024
	MaxParameterDescriptionBytes = 1024
	RequestTypeTargetBytes       = 512
	MaxTopLevelProperties        = 15
	MaxToolNameBytes             = 48
	MaxCombinedToolNameBytes     = 60
	MaxSerializedSchemaBytes     = 24 << 10
	MaxInputSchemaNestingDepth   = 13
)

// OfficialServerAliases are published in first-party setup examples or sent
// during initialize. Some clients combine alias + separator + tool name.
var OfficialServerAliases = []string{"signoz", "signoz-mcp-server", "SigNozMCP"}

// GrandfatheredWideSchemaProperties pins the exact property inventory of the
// only schemas allowed above MaxTopLevelProperties. Adding or changing an
// entry requires explicit guardrail review.
var GrandfatheredWideSchemaProperties = map[string][]string{
	"signoz_aggregate_traces": {
		"aggregateOn",
		"aggregation",
		"end",
		"error",
		"filter",
		"groupBy",
		"limit",
		"maxDuration",
		"minDuration",
		"operation",
		"orderBy",
		"requestType",
		"searchContext",
		"service",
		"start",
		"stepInterval",
		"timeRange",
	},
	"signoz_create_alert": {
		"alert",
		"alertType",
		"annotations",
		"condition",
		"description",
		"disabled",
		"evalWindow",
		"evaluation",
		"frequency",
		"labels",
		"notificationSettings",
		"preferredChannels",
		"ruleType",
		"schemaVersion",
		"searchContext",
		"source",
		"version",
	},
	"signoz_create_notification_channel": {
		"email_html",
		"email_to",
		"msteams_text",
		"msteams_title",
		"msteams_webhook_url",
		"name",
		"opsgenie_api_key",
		"opsgenie_description",
		"opsgenie_message",
		"opsgenie_priority",
		"pagerduty_description",
		"pagerduty_routing_key",
		"pagerduty_severity",
		"searchContext",
		"send_resolved",
		"slack_api_url",
		"slack_channel",
		"slack_text",
		"slack_title",
		"type",
		"webhook_password",
		"webhook_url",
		"webhook_username",
	},
	"signoz_query_metrics": {
		"end",
		"filter",
		"formula",
		"formulaQueries",
		"groupBy",
		"isMonotonic",
		"metricName",
		"metricType",
		"reduceTo",
		"requestType",
		"searchContext",
		"source",
		"spaceAggregation",
		"start",
		"stepInterval",
		"temporality",
		"timeAggregation",
		"timeRange",
	},
	"signoz_update_alert": {
		"alert",
		"alertType",
		"annotations",
		"condition",
		"description",
		"disabled",
		"evalWindow",
		"evaluation",
		"frequency",
		"id",
		"labels",
		"notificationSettings",
		"preferredChannels",
		"ruleId",
		"ruleType",
		"schemaVersion",
		"searchContext",
		"source",
		"version",
	},
	"signoz_update_notification_channel": {
		"email_html",
		"email_to",
		"id",
		"msteams_text",
		"msteams_title",
		"msteams_webhook_url",
		"name",
		"opsgenie_api_key",
		"opsgenie_description",
		"opsgenie_message",
		"opsgenie_priority",
		"pagerduty_description",
		"pagerduty_routing_key",
		"pagerduty_severity",
		"searchContext",
		"send_resolved",
		"slack_api_url",
		"slack_channel",
		"slack_text",
		"slack_title",
		"type",
		"webhook_password",
		"webhook_url",
		"webhook_username",
	},
}
