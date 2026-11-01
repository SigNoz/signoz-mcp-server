package types

import (
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// CreateViewInput is the signoz_create_view input schema: the upstream-generated
// PostableSavedView (regenerated from the pinned SigNoz release spec by make
// gen) plus the MCP-only searchContext envelope field. Never hand-maintain a
// copy of the view shape here; query-envelope teaching lives in the
// signoz://view resources.
type CreateViewInput struct {
	apitypes.SavedviewtypesPostableSavedView
	// SchemaVersion shadows the generated required field: the handler forces
	// "v2", so callers may omit it.
	SchemaVersion string `json:"schemaVersion,omitempty" jsonschema:"The version of the spec shape. Always v2; filled in when omitted."`
	SearchContext string `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

// UpdateViewInput is the signoz_update_view input schema: the target view id
// and the generated UpdatableSavedView under "view", plus the searchContext
// envelope field.
type UpdateViewInput struct {
	// id is an optional property (json ",omitempty") so schema-validating
	// clients may still send the legacy "viewId" alias; the handler requires
	// one via readResourceID (canonical "id" wins).
	ID            string            `json:"id,omitempty" jsonschema:"UUID of the view to replace (required). Obtain it from signoz_list_views."`
	View          UpdatableViewBody `json:"view" jsonschema:"Complete saved view after the requested changes. Start with the data returned by signoz_get_view and pass the full object here; name is immutable and is dropped."`
	SearchContext string            `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

// UpdatableViewBody is the generated UpdatableSavedView with schemaVersion
// shadowed as optional, because the handler forces "v2".
type UpdatableViewBody struct {
	apitypes.SavedviewtypesUpdatableSavedView
	SchemaVersion string `json:"schemaVersion,omitempty" jsonschema:"The version of the spec shape. Always v2; filled in when omitted."`
}
