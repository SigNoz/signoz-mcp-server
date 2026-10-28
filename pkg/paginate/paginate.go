package paginate

import (
	"encoding/json"
	"strconv"
)

const (
	DefaultLimit  = 50
	DefaultOffset = 0
	// MaxLimit bounds the per-page size for the summary list tools (services,
	// dashboards, alerts, alert rules, views, notification channels). These
	// responses are built fully in memory before paging, so an unbounded limit
	// is a memory vector on the shared multi-tenant pod. Callers needing more
	// rows paginate via offset.
	MaxLimit = 1000
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

// ParseParamsClamped extracts limit and offset from request arguments,
// clamping the limit to MaxLimit and reporting whether it clamped so handlers
// can surface a note. Accepts limit/offset as a number or a string.
func ParseParamsClamped(args any) (limit, offset int, clamped bool) {
	limit = DefaultLimit
	offset = DefaultOffset

	m, ok := args.(map[string]any)
	if !ok {
		return limit, offset, false
	}

	if v, present, ok := parseLooseInt(m["limit"]); ok && present && v > 0 {
		if v > MaxLimit {
			limit = MaxLimit
			clamped = true
		} else {
			limit = int(v)
		}
	}
	if v, present, ok := parseLooseInt(m["offset"]); ok && present && v >= 0 {
		offset = int(v)
	}
	return limit, offset, clamped
}

// parseLooseInt accepts an int/float/json.Number/string limit-or-offset value.
// Mirrors the tools-package looseInt; duplicated here to keep paginate free of
// an import cycle on the handler package.
func parseLooseInt(v any) (value int64, present bool, ok bool) {
	switch n := v.(type) {
	case nil:
		return 0, false, true
	case int:
		return int64(n), true, true
	case int64:
		return n, true, true
	case float64:
		return int64(n), true, true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, true, false
		}
		return i, true, true
	case string:
		if n == "" {
			return 0, false, true
		}
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, true, false
		}
		return int64(i), true, true
	default:
		return 0, true, false
	}
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

// NewMetadata describes the page at offset and limit of total results.
// NextOffset is -1 on the last page.
func NewMetadata(total, offset, limit int) Metadata {
	nextOffset := offset + limit
	if nextOffset >= total {
		nextOffset = -1
	}
	return Metadata{
		Total:      total,
		Offset:     offset,
		Limit:      limit,
		HasMore:    nextOffset != -1,
		NextOffset: nextOffset,
	}
}

// Wrap wraps paginated data and metadata into json.
func Wrap(data []any, total, offset, limit int) ([]byte, error) {
	return json.Marshal(Response{Data: data, Pagination: NewMetadata(total, offset, limit)})
}
