package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
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

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

// parseBoolArg parses a boolean tool argument. It accepts a real JSON bool, or
// the case-insensitive strings "true"/"false" (so legacy string-typed callers
// keep working after the schema is declared as a real boolean). Any other
// non-empty value is a typed error so the LLM gets a correctable message rather
// than the value being silently dropped.
//
// Returns (value, present, error):
//   - present is false when the key is absent or an empty string (treat as "not set")
//   - error is non-nil only when a present value cannot be interpreted as a bool
func parseBoolArg(args map[string]any, key string) (bool, bool, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return false, false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, true, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return false, false, nil
		}
		switch strings.ToLower(s) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		default:
			return false, false, fmt.Errorf(`invalid %q value %q: must be a boolean (true or false)`, key, v)
		}
	default:
		return false, false, fmt.Errorf(`invalid %q value: must be a boolean (true or false)`, key)
	}
}

func resolveTimestamps(args map[string]any, defaultRange string) (int64, int64, error) {
	// Reject a present-but-malformed start/end LOUDLY before falling through to
	// the default window. GetTimestampsWithDefaults silently defaults on a bad
	// value, which would hand back the wrong time range with no error.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		return 0, 0, err
	}
	// Inject the tool's advertised default window when there is no usable explicit
	// time input — no usable timeRange string AND no usable explicit start. A
	// present-but-empty start (which timeutil treats as absent) must NOT block the
	// default, or GetTimestampsWithDefaults silently falls back to its generic 6h
	// window. A present-but-non-string timeRange is already rejected loudly by
	// ValidateExplicitTimestamps above; an empty-string timeRange means "use default".
	if tr, ok := args["timeRange"].(string); !ok || tr == "" {
		if !timeutil.HasUsableTimestamp(args, "start") {
			args["timeRange"] = defaultRange
		}
	}
	start, end := timeutil.GetTimestampsWithDefaults(args, "ms")
	var startTime, endTime int64
	if err := json.Unmarshal([]byte(start), &startTime); err != nil {
		return 0, 0, fmt.Errorf("invalid start timestamp: use timeRange instead (e.g., \"1h\", \"24h\")")
	}
	if err := json.Unmarshal([]byte(end), &endTime); err != nil {
		return 0, 0, fmt.Errorf("invalid end timestamp: use timeRange instead (e.g., \"1h\", \"24h\")")
	}
	return startTime, endTime, nil
}

// parseStepInterval parses an optional stepInterval (seconds) argument for the
// aggregate tools. It accepts a real JSON number OR a string that is ENTIRELY a
// positive integer. It deliberately does NOT use parseIntLoose for the string
// case: parseIntLoose scans a numeric prefix via Sscanf("%d"), so "1h"/"60s"
// would silently become 1/60 — a wrong bucket size. A present-but-invalid value
// (non-numeric, suffixed, or <= 0) yields a nil interval and a warning so the
// backend auto-selects rather than applying a silently-wrong granularity.
//
// Returns (nil, "") when the argument is absent or an empty string.
func parseStepInterval(raw any) (*int64, string) {
	if raw == nil {
		return nil, ""
	}
	warn := func() (*int64, string) {
		return nil, fmt.Sprintf(
			"stepInterval %v could not be parsed as a positive integer number of seconds; letting the backend auto-select the bucket size",
			raw)
	}
	switch v := raw.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, "" // treat empty string as not set
		}
		// Require the ENTIRE string to be a base-10 integer (no "1h"/"60s"/hex).
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return warn()
		}
		return &n, ""
	case float64:
		// JSON numbers decode as float64. Reject non-integral values (e.g.
		// 60.9) rather than truncating to 60 — the contract is "positive
		// integer seconds", so a fractional value is invalid → auto-select.
		if v != float64(int64(v)) || v <= 0 {
			return warn()
		}
		n := int64(v)
		return &n, ""
	case json.Number:
		// A json.Number preserves the literal; require it parse as a positive
		// integer (Int64 errors on a fractional literal like "60.9").
		n, err := v.Int64()
		if err != nil || n <= 0 {
			return warn()
		}
		return &n, ""
	case int:
		if v <= 0 {
			return warn()
		}
		n := int64(v)
		return &n, ""
	case int64:
		if v <= 0 {
			return warn()
		}
		return &v, ""
	default:
		return warn()
	}
}

// MaxRawResultLimit caps how many raw rows search_logs / search_traces will
// request from the backend. Each row is read fully into memory, decoded, and
// re-marshaled into the tool response (~1.6 MiB per 1000 log rows measured
// against a real backend), so an uncapped limit is an unbounded single-request
// memory vector on the shared, memory-limited multi-tenant pod. Callers that
// need more rows should paginate via offset.
const MaxRawResultLimit = querybuilder.MaxQueryLimit

// clampLimit bounds a parsed limit to MaxRawResultLimit. It returns the
// effective limit and whether clamping occurred so handlers can surface it.
func clampLimit(n int) (int, bool) {
	if n > MaxRawResultLimit {
		return MaxRawResultLimit, true
	}
	return n, false
}
