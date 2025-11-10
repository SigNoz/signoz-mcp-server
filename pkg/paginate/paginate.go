package paginate

import (
	"encoding/json"
	"strconv"
)

const (
	DefaultLimit  = 50
	DefaultOffset = 0
)

// Metadata contains paged info for any listed responses
// helps LLMs to understand status of pagination
type Metadata struct {
	Total      int  `json:"total"`
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	HasMore    bool `json:"hasMore"`
	NextOffset int  `json:"nextOffset"`
}

// Response wraps a list data with paged metadata
type Response struct {
	Data       []any    `json:"data"`
	Pagination Metadata `json:"pagination"`
}

// ParseParams extracts limit and offset from request arguments.
func ParseParams(args any) (int, int) {
	limit := DefaultLimit
	offset := DefaultOffset

	m, ok := args.(map[string]any)
	if !ok {
		return limit, offset
	}

	if limitStr, ok := m["limit"].(string); ok {
		if limitInt, err := strconv.Atoi(limitStr); err == nil && limitInt > 0 {
			limit = limitInt
		}
	}
	if offsetStr, ok := m["offset"].(string); ok {
		if offsetInt, err := strconv.Atoi(offsetStr); err == nil && offsetInt >= 0 {
			offset = offsetInt
		}
	}
	return limit, offset
}

// Array returns the paged subset for list data.
func Array(arr []any, offset, limit int) []any {
	if limit <= 0 || offset >= len(arr) {
		return []any{}
	}

	end := offset + limit
	if end > len(arr) {
		end = len(arr)
	}
	return arr[offset:end]
}

// Wrap wraps paginated data and metadata into json.
func Wrap(data []any, total, offset, limit int) ([]byte, error) {
	nextOffset := offset + limit
	if nextOffset >= total {
		nextOffset = -1
	}

	hasMore := nextOffset != -1

	return json.Marshal(Response{
		Data: data,
		Pagination: Metadata{
			Total:      total,
			Offset:     offset,
			Limit:      limit,
			HasMore:    hasMore,
			NextOffset: nextOffset,
		},
	})
}
