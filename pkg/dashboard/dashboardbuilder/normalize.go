package dashboardbuilder

import (
	"strconv"
	"strings"
)

// filterItemsSlice returns the items array from a filters map as []any,
// regardless of whether the caller constructed the slice as []any (the shape
// Go's JSON decoder produces when unmarshalling into map[string]any) or as
// []map[string]any (an idiomatic Go literal for a typed array of maps).
// Returns nil for any other type — missing key, wrong concrete type, or non-
// slice value — so callers that range over the result naturally no-op.
//
// This exists because filter-related helpers (normalizers and the expression
// consistency check) need to walk filter items regardless of which code path
// built the payload. Asserting on []any alone silently skips the whole items
// array when the caller happens to use the typed-slice literal, defeating
// the purpose of the helper.
func filterItemsSlice(items any) []any {
	switch v := items.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, len(v))
		for i, m := range v {
			out[i] = m
		}
		return out
	}
	return nil
}

// canonicalDynamicSource maps an incoming dynamicVariablesSource value to the
// canonical form written by the current SigNoz UI dropdown
// (frontend AttributeSource enum): "Traces", "Logs", "Metrics", "All telemetry".
// The GET API and older payloads may return any casing of these values, plus
// the legacy alias "all sources" which earlier versions of the SigNoz
// frontend used in place of "All telemetry". LLMs that echo a GET payload
// back on update would otherwise fail validation unnecessarily.
//
// Unknown values pass through untouched so Validate can emit a clear error.
func canonicalDynamicSource(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "all sources" {
		return DynamicSourceAllSources
	}
	for _, valid := range ValidDynamicVariableSources {
		if strings.ToLower(valid) == lower {
			return valid
		}
	}
	return s
}

// uppercaseFilterOpsInQueryMaps normalizes `filters.items[].op` within each
// query or formula entry to uppercase. The SigNoz write API only accepts the
// Capitalized operator forms in validFilterOperators (IN, NOT_IN, LIKE, ILIKE,
// EXISTS, CONTAINS, =, !=, ...), while the GET API may return either case.
// Without this, an LLM that echoes a GET payload back on update trips panel
// validation for a trivially-fixable case mismatch.
//
// Symbol operators (=, !=, >=, >, <=, <) are unaffected by ToUpper. Unknown
// operators are still rejected downstream by the panel validator.
func uppercaseFilterOpsInQueryMaps(entries []map[string]any) {
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		filters, ok := entry["filters"].(map[string]any)
		if !ok {
			continue
		}
		items := filterItemsSlice(filters["items"])
		if items == nil {
			continue
		}
		for _, it := range items {
			item, ok := it.(map[string]any)
			if !ok {
				continue
			}
			op, ok := item["op"].(string)
			if !ok {
				continue
			}
			item["op"] = strings.ToUpper(strings.TrimSpace(op))
		}
	}
}

// normalizeFilterItemsInQueryMaps walks each `filters.items[]` entry on a
// query or formula and heals two read/write shape quirks that stop the SigNoz
// UI edit modal from hydrating the filter row — even though the list/render
// path and the ClickHouse query path both accept the broken shape via the
// top-level `filter.expression` string:
//
//  1. `key.dataType` missing and `key.id` non-canonical. The edit modal
//     reverse-maps the filter key back to the autocomplete dropdown by id,
//     which must be `<key>--<dataType>--<type>` (a trailing `--` when type
//     is empty). Items with an id like `"k8s.node.name"` (missing the whole
//     suffix) fail to hydrate. We fill `dataType` (inferring from a
//     well-formed id when possible, else defaulting to `"string"`) and
//     rebuild `id` only when it is clearly malformed (empty or missing
//     `--`). Well-formed 3-part and 4-part ids pass through untouched.
//
//  2. `value` wrapped in a single-element array around a `$var` reference.
//     The canonical shape on every other IN-with-variable filter is a plain
//     string; `["$var"]` causes the form row to misrender. We unwrap only
//     when the sole element is a `$`-prefixed string, so genuine
//     single-element arrays of real values are preserved.
//
// Everything else (nil items, missing filters, wrong types, non-map keys) is
// skipped safely.
func normalizeFilterItemsInQueryMaps(entries []map[string]any) {
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		filters, ok := entry["filters"].(map[string]any)
		if !ok {
			continue
		}
		items := filterItemsSlice(filters["items"])
		if items == nil {
			continue
		}
		for _, it := range items {
			item, ok := it.(map[string]any)
			if !ok {
				continue
			}
			normalizeFilterItem(item)
		}
	}
}

func normalizeFilterItem(item map[string]any) {
	if key, ok := item["key"].(map[string]any); ok {
		keyName, _ := key["key"].(string)
		dataType, _ := key["dataType"].(string)
		keyType, _ := key["type"].(string)
		existingID, _ := key["id"].(string)

		// Fill dataType — prefer the value from a well-formed id if present.
		if dataType == "" && strings.Contains(existingID, "--") {
			if parts := strings.Split(existingID, "--"); len(parts) >= 2 && parts[1] != "" {
				dataType = parts[1]
				key["dataType"] = dataType
			}
		}
		if dataType == "" && keyName != "" {
			dataType = "string"
			key["dataType"] = dataType
		}

		// Rebuild id only when clearly malformed (empty or no `--` separator).
		// Leaves canonical 3-part (`key--dataType--type`) and richer 4-part
		// forms (e.g. trailing `--true` for isColumn) alone.
		if keyName != "" && (existingID == "" || !strings.Contains(existingID, "--")) {
			key["id"] = keyName + "--" + dataType + "--" + keyType
		}
	}

	// Unwrap `["$var"]` → `"$var"`. Preserves multi-element arrays and
	// single-element arrays of non-variable values.
	if arr, isArr := item["value"].([]any); isArr && len(arr) == 1 {
		if s, isStr := arr[0].(string); isStr && strings.HasPrefix(s, "$") {
			item["value"] = s
		}
	}
}

// coerceHavingInQueryMaps rewrites the `having` field of each query or formula
// entry from the GET-shape object `{expression: "..."}` to the POST/PUT-shape
// clause array `[{columnName, op, value}, ...]`. The empty/whitespace
// expression coerces to `[]`; a parseable expression coerces to a single-clause
// array (AND-joined expressions become multiple clauses). Shapes that aren't
// `{expression: <string>}` — missing key, non-string expression, already-array,
// nil, absent — are left untouched so downstream strict unmarshalling and
// validation can surface a clear error rather than silent data loss.
func coerceHavingInQueryMaps(entries []map[string]any) {
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		h, ok := entry["having"]
		if !ok {
			continue
		}
		obj, isObj := h.(map[string]any)
		if !isObj {
			continue
		}
		rawExpr, hasExpr := obj["expression"]
		if !hasExpr {
			continue
		}
		exprStr, isStr := rawExpr.(string)
		if !isStr {
			continue
		}
		trimmed := strings.TrimSpace(exprStr)
		if trimmed == "" {
			entry["having"] = []any{}
			continue
		}
		clauses, ok := parseHavingExpression(trimmed)
		if !ok {
			continue
		}
		out := make([]any, len(clauses))
		for i, c := range clauses {
			out[i] = c
		}
		entry["having"] = out
	}
}

// havingOps lists supported having operators, ordered so multi-character
// operators are matched before their single-character prefixes.
var havingOps = []string{">=", "<=", "!=", "=", ">", "<"}

// parseHavingExpression converts a free-form having expression into one or
// more clause maps `{columnName, op, value}`. Supports AND-joined comparisons.
// Returns ok=false if the expression contains a top-level OR (HAVING clause
// arrays are AND-joined; OR cannot be represented) or if any sub-expression
// cannot be parsed; the caller leaves the original object form in place so
// validation surfaces a clear error.
func parseHavingExpression(expr string) ([]map[string]any, bool) {
	parts, ok := splitTopLevelAnd(expr)
	if !ok || len(parts) == 0 {
		return nil, false
	}
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		clause, ok := parseHavingClause(strings.TrimSpace(p))
		if !ok {
			return nil, false
		}
		out = append(out, clause)
	}
	return out, true
}

// splitTopLevelAnd splits on `AND` (case-insensitive) outside of parentheses
// and quoted strings. Single `&&` is also treated as AND. Returns ok=false if
// a top-level `OR` / `||` is found, since HAVING coerces to an AND-joined
// clause array and OR cannot be represented faithfully.
func splitTopLevelAnd(s string) ([]string, bool) {
	var parts []string
	depth := 0
	var inQuote byte
	start := 0
	i := 0
	for i < len(s) {
		c := s[i]
		if inQuote != 0 {
			if c == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if c == inQuote {
				inQuote = 0
			}
			i++
			continue
		}
		switch c {
		case '\'', '"':
			inQuote = c
			i++
			continue
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth == 0 {
			if c == '|' && i+1 < len(s) && s[i+1] == '|' {
				return nil, false
			}
			if i+2 <= len(s) && (c == 'O' || c == 'o') && strings.EqualFold(s[i:i+2], "OR") {
				before := i == 0 || !isWordChar(s[i-1])
				after := i+2 == len(s) || !isWordChar(s[i+2])
				if before && after {
					return nil, false
				}
			}
			if c == '&' && i+1 < len(s) && s[i+1] == '&' {
				parts = append(parts, s[start:i])
				i += 2
				start = i
				continue
			}
			if i+3 <= len(s) && (c == 'A' || c == 'a') && strings.EqualFold(s[i:i+3], "AND") {
				before := i == 0 || !isWordChar(s[i-1])
				after := i+3 == len(s) || !isWordChar(s[i+3])
				if before && after {
					parts = append(parts, s[start:i])
					i += 3
					start = i
					continue
				}
			}
		}
		i++
	}
	parts = append(parts, s[start:])
	return parts, true
}

// isWordChar returns true for ASCII identifier characters (letters, digits,
// underscore). Used to detect token boundaries around AND/OR keywords so a
// keyword followed by punctuation (e.g. `OR(`, `AND"x"`) is correctly split,
// while substrings inside identifiers (e.g. `OrderCount`, `expanded`) are not.
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// parseHavingClause parses a single comparison `<columnName> <op> <value>`.
// Operator detection respects parentheses and quoted strings; the first
// top-level operator wins. Value is unquoted if it is a fully quoted string,
// parsed as a number when numeric, or kept verbatim otherwise.
func parseHavingClause(s string) (map[string]any, bool) {
	if s == "" {
		return nil, false
	}
	depth := 0
	var inQuote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			inQuote = c
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		for _, op := range havingOps {
			if strings.HasPrefix(s[i:], op) {
				lhs := strings.TrimSpace(s[:i])
				rhs := strings.TrimSpace(s[i+len(op):])
				if lhs == "" || rhs == "" {
					return nil, false
				}
				return map[string]any{
					"columnName": lhs,
					"op":         op,
					"value":      parseHavingValue(rhs),
				}, true
			}
		}
	}
	return nil, false
}

// parseHavingValue best-effort converts an RHS literal to a typed value:
// stripped quotes for fully quoted strings, int/float for numerics, else the
// raw trimmed string. Backend tolerates either typed or string values.
func parseHavingValue(s string) any {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '\'' || first == '"') && first == last {
			return s[1 : len(s)-1]
		}
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
