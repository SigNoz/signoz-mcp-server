package types

import "encoding/json"

// SavedView mirrors the SigNoz server SavedView model
// (pkg/query-service/model/v3/v3.go). CompositeQuery is passed through
// as raw JSON so this package does not need to track the query-builder
// schema across server versions.
//
// On create/update the caller supplies Name, SourcePage, CompositeQuery
// (required), plus optional Category, Tags, ExtraData. ID and the four
// timestamp/audit fields are server-populated.
type SavedView struct {
	ID             string          `json:"id,omitempty"`
	Name           string          `json:"name" jsonschema:"required" jsonschema_extras:"description=Display name of the view."`
	Category       string          `json:"category,omitempty" jsonschema_extras:"description=Free-form grouping label."`
	SourcePage     string          `json:"sourcePage" jsonschema:"required,enum=traces,enum=logs,enum=metrics" jsonschema_extras:"description=Which Explorer this view belongs to."`
	Tags           []string        `json:"tags,omitempty" jsonschema_extras:"description=Free-form tags."`
	CompositeQuery json.RawMessage `json:"compositeQuery" jsonschema:"required" jsonschema_extras:"description=The Query Builder payload. See signoz://view/instructions."`
	ExtraData      string          `json:"extraData,omitempty" jsonschema_extras:"description=UI-controlled options, JSON string."`
	CreatedAt      string          `json:"createdAt,omitempty"`
	CreatedBy      string          `json:"createdBy,omitempty"`
	UpdatedAt      string          `json:"updatedAt,omitempty"`
	UpdatedBy      string          `json:"updatedBy,omitempty"`
}

// UpdateViewInput is the MCP input schema for signoz_update_view. The path
// parameter (viewId) and body (view) are bundled so schema-driven clients
// see both required fields. Mirrors the dashboard update pattern.
type UpdateViewInput struct {
	ViewID string    `json:"viewId" jsonschema:"required" jsonschema_extras:"description=UUID of the view to replace."`
	View   SavedView `json:"view" jsonschema:"required" jsonschema_extras:"description=Full SavedView body representing the complete post-update state. Call signoz_get_view first and pass its data field back here."`
}
