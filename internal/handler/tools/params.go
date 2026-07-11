package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/mark3labs/mcp-go/mcp"
)

// listResult wraps a paginated list payload (a code-controlled envelope) as a
// structured tool result, so list tools keep StructuredContent alongside the
// text block. When the requested per-page limit was clamped to paginate.MaxLimit
// it appends a trailing advisory note.
func listResult(payload []byte, limitClamped bool) *mcp.CallToolResult {
	if !limitClamped {
		return structuredResult(payload)
	}
	return structuredResultWithNotes(payload, fmt.Sprintf(
		"note: limit clamped to %d per page to bound server memory; use \"offset\" to page through more results.",
		paginate.MaxLimit))
}

// looseInt parses a limit/offset-style integer that may arrive as a JSON number
// (float64 / json.Number / native int) OR a string, since MCP clients are
// inconsistent about typing numeric arguments. It returns (value, present, ok):
//   - present=false when the key is absent or an empty string (caller uses its default)
//   - ok=false when the value is present but cannot be parsed as an integer
//
// This is the single number-or-string parser shared by every limit/offset site
// so a numeric input never silently falls through to the default (the bug class
// this cleanup targets).
func looseInt(v any) (value int64, present bool, ok bool) {
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
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false, true
		}
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, true, false
		}
		return i, true, true
	default:
		return 0, true, false
	}
}

// intArg parses an integer argument that may be a number or a string. A missing
// or empty value yields defaultVal; a non-positive value also yields defaultVal
// (callers treat <=0 limits as "use the default"). A present-but-unparseable
// value is a hard error so the caller can correct it.
func intArg(args map[string]any, key string, defaultVal int) (int, error) {
	value, present, ok := looseInt(args[key])
	if !ok {
		return 0, fmt.Errorf("invalid %q value %v: must be a number", key, args[key])
	}
	if !present {
		return defaultVal, nil
	}
	if value <= 0 {
		return defaultVal, nil
	}
	return int(value), nil
}

// parseLimit parses a docs-search limit that may be a number or string. Unlike
// intArg it never errors — an unparseable value falls back to the provided
// default (the docs tools historically tolerate sloppy limits). It accepts both
// numeric and string forms so flipping the schema from WithNumber→WithString is
// transparent to existing numeric callers.
func parseLimit(v any, fallback int) int {
	value, present, ok := looseInt(v)
	if !ok || !present || value <= 0 {
		return fallback
	}
	return int(value)
}

// intOrStringType overrides a property's JSON-Schema "type" with the union
// ["integer","string"]. We need this because parseLimit (and looseInt) accept a
// limit as EITHER a JSON number or a string, but mcp.WithString advertises only
// "string". A schema-validating MCP client that naturally sends {"limit": 3}
// (a JSON number — totally normal for a limit) would then be rejected before the
// handler runs. Advertising the union keeps the schema honest about what the
// parser already accepts. Apply it AFTER mcp.WithString so it overwrites the
// "string" type the option set. It serializes to a JSON-Schema type array
// (`"type": ["integer","string"]`), which is valid Draft-07/2020-12.
func intOrStringType() mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["type"] = []string{"integer", "string"}
	}
}

func boolOrStringType() mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["type"] = []string{"boolean", "string"}
	}
}

func stringOrStringArrayType() mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["type"] = []string{"array", "string"}
		schema["items"] = map[string]any{"type": "string"}
	}
}

func stringOrArrayType() mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["type"] = []string{"array", "string"}
		schema["items"] = map[string]any{"type": "object"}
	}
}

// validRequestTypes maps the user-facing requestType values accepted by the
// aggregate and metrics tools to true. This is an MCP-owned, stable enum (not a
// backend-evolving set), so we hard-validate it at the arg layer and reject
// unknown values uniformly instead of silently coercing them.
var validRequestTypes = map[string]bool{
	"scalar":      true,
	"time_series": true,
}

// readRequestType reads the optional "requestType", rejecting a present-but-non-string
// value loudly instead of silently defaulting. Absent/nil yields ("", nil) so the
// caller applies its per-signal default.
func readRequestType(args map[string]any) (string, error) {
	raw, present := args["requestType"]
	if !present || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf(
			`%s "requestType" must be a string ("scalar" or "time_series")`,
			validationErrorPrefix)
	}
	return s, nil
}

// validateRequestType returns an error for an unknown requestType value. An
// empty value is allowed (the caller applies its per-signal default).
func validateRequestType(requestType string) error {
	if requestType == "" {
		return nil
	}
	if !validRequestTypes[requestType] {
		return fmt.Errorf(
			`parameter validation failed: "requestType" %q is invalid. Valid values: "scalar" (one aggregate value) or "time_series" (one value per time bucket)`,
			requestType,
		)
	}
	return nil
}

// readResourceID reads the canonical "id" param for a stored-resource handle,
// falling back to a legacy alias key (e.g. "ruleId", "uuid", "viewId") that the
// MCP server accepts forever for backward compatibility. The canonical "id"
// takes precedence when both are present and non-empty.
func readResourceID(args map[string]any, legacyKey string) string {
	if v, ok := args["id"].(string); ok && v != "" {
		return v
	}
	if v, ok := args[legacyKey].(string); ok {
		return v
	}
	return ""
}
