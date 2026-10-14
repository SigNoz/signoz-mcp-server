package types

import (
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// CreateAlertInput is the signoz_create_alert input schema: the
// upstream-generated PostableRule (regenerated from the pinned SigNoz release
// spec by make gen) plus the MCP-only searchContext envelope field. Never
// hand-maintain a copy of the rule shape here; field-level teaching lives in
// the tool description and the signoz://alert resources.
type CreateAlertInput struct {
	apitypes.RuletypesPostableRule
	SearchContext string `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

// UpdateAlertInput is the signoz_update_alert input schema: the generated
// PostableRule plus the target rule id and the searchContext envelope field.
type UpdateAlertInput struct {
	// id and ruleId are both optional properties (json ",omitempty", neither
	// required) so schema-validating clients accept either key; the handler
	// requires one via readResourceID (canonical "id" wins).
	ID           string `json:"id,omitempty" jsonschema:"UUIDv7 of the alert rule to update (required). Obtain it from signoz_list_alert_rules or signoz_get_alert."`
	LegacyRuleID string `json:"ruleId,omitempty" jsonschema:"Deprecated alias for 'id'."`
	apitypes.RuletypesPostableRule
	SearchContext string `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}
