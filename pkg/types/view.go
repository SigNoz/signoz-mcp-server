package types

import "encoding/json"

// SavedView mirrors the SigNoz server SavedView model
// (pkg/query-service/model/v3/v3.go). Used for responses and internal
// wire formatting; for tool input schemas prefer SavedViewInput so the
// published schema excludes server-populated fields.
type SavedView struct {
	ID             string          `json:"id,omitempty"`
	Name           string          `json:"name"`
	Category       string          `json:"category,omitempty"`
	SourcePage     string          `json:"sourcePage"`
	Tags           []string        `json:"tags,omitempty"`
	CompositeQuery json.RawMessage `json:"compositeQuery"`
	ExtraData      string          `json:"extraData,omitempty"`
	CreatedAt      string          `json:"createdAt,omitempty"`
	CreatedBy      string          `json:"createdBy,omitempty"`
	UpdatedAt      string          `json:"updatedAt,omitempty"`
	UpdatedBy      string          `json:"updatedBy,omitempty"`
}

// SavedViewInput is the MCP input schema for signoz_create_view and the
// view field of signoz_update_view. It exposes only caller-settable
// fields — no id, no audit timestamps — and types CompositeQuery as
// an object so schema-driven clients know to send a JSON object, not
// a string.
type SavedViewInput struct {
	Name           string         `json:"name" jsonschema:"required" jsonschema_extras:"description=Display name of the view."`
	Category       string         `json:"category,omitempty" jsonschema_extras:"description=Free-form grouping label."`
	SourcePage     string         `json:"sourcePage" jsonschema:"required,enum=traces,enum=logs,enum=metrics" jsonschema_extras:"description=Which Explorer this view belongs to."`
	Tags           []string       `json:"tags,omitempty" jsonschema_extras:"description=Free-form tags."`
	CompositeQuery map[string]any `json:"compositeQuery" jsonschema:"required" jsonschema_extras:"description=The Query Builder payload as an object (not a string). Must contain queryType plus matching sub-query. See signoz://view/instructions and signoz://view/examples.,type=object,additionalProperties=true"`
	ExtraData      string         `json:"extraData,omitempty" jsonschema_extras:"description=UI-controlled options as a JSON-encoded string (safe to leave empty)."`
	SearchContext  string         `json:"searchContext,omitempty" jsonschema_extras:"description=Optional. The user's original question that triggered this tool call."`
}

// UpdateViewInput is the MCP input schema for signoz_update_view. The path
// parameter (viewId) and body (view) are bundled so schema-driven clients
// see both required fields. Mirrors the dashboard update pattern.
type UpdateViewInput struct {
	ViewID        string         `json:"viewId" jsonschema:"required" jsonschema_extras:"description=UUID of the view to replace."`
	View          SavedViewInput `json:"view" jsonschema:"required" jsonschema_extras:"description=Full SavedView body representing the complete post-update state. Call signoz_get_view first and pass its data field back here."`
	SearchContext string         `json:"searchContext,omitempty" jsonschema_extras:"description=Optional. The user's original question that triggered this tool call."`
}
