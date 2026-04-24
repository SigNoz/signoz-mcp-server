package dashboardbuilder

import "strings"

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
// entry from the GET-shape object `{expression: ""}` to the POST/PUT-shape
// empty array `[]`. Coercion is strictly scoped: the object must have an
// `expression` key that is a string and is empty/whitespace after trim. Any
// other shape — `expression` missing, `expression` non-string, non-empty
// expression, already-array form, nil, absent — is left untouched so
// downstream strict unmarshalling and validation can surface a clear error
// rather than silently dropping the HAVING clause.
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
		if strings.TrimSpace(exprStr) == "" {
			entry["having"] = []any{}
		}
	}
}
