package types

import "encoding/json"

// SavedView mirrors the SigNoz server SavedView model
// (pkg/query-service/model/v3/v3.go). Used for responses and internal
// wire formatting.
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
