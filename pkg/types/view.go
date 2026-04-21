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
