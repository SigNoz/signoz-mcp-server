package gen

import "strings"

// excludedOperationIDs lists operations that the generator must skip.
// These are browser-redirect auth flows, side-effectful admin endpoints,
// or endpoints whose security scheme is undefined in the spec.
var excludedOperationIDs = map[string]bool{
	// SAML/OAuth callbacks — browser redirect flows, not meaningful for agents.
	"CompleteSAMLLogin":   true,
	"CompleteGoogleLogin": true,
	"CompleteOIDCLogin":   true,

	// Password reset — side-effect-heavy user flow.
	"ResetPassword": true,

	// Public dashboard ops reference the undefined `anonymous` security scheme.
	"GetPublicDashboard":    true,
	"GetPublicDashboardData": true,
}

// curatedToolNames lists MCP tool names that are already hand-written in
// internal/handler/tools. Generated tools whose name collides with a curated
// tool are skipped (curated wins) so hand-crafted descriptions and guidance
// are never overwritten.
var curatedToolNames = map[string]bool{
	"signoz_aggregate_logs":              true,
	"signoz_aggregate_traces":            true,
	"signoz_create_notification_channel": true,
	"signoz_create_view":                 true,
	"signoz_delete_dashboard":            true,
	"signoz_delete_notification_channel": true,
	"signoz_delete_view":                 true,
	"signoz_execute_builder_query":       true,
	"signoz_get_alert":                   true,
	"signoz_get_alert_history":           true,
	"signoz_get_dashboard":               true,
	"signoz_get_field_keys":              true,
	"signoz_get_field_values":            true,
	"signoz_get_notification_channel":    true,
	"signoz_get_service_top_operations":  true,
	"signoz_get_trace_details":           true,
	"signoz_get_view":                    true,
	"signoz_list_alerts":                 true,
	"signoz_list_dashboards":             true,
	"signoz_list_metrics":                true,
	"signoz_list_notification_channels":  true,
	"signoz_list_services":               true,
	"signoz_list_views":                  true,
	"signoz_query_metrics":               true,
	"signoz_search_logs":                 true,
	"signoz_search_traces":               true,
	"signoz_update_notification_channel": true,
	"signoz_update_view":                 true,
}

// isDeprecated returns true for legacy endpoints we don't want to expose.
func isDeprecated(operationID string) bool {
	return strings.HasSuffix(operationID, "Deprecated")
}
