package util

import (
	"encoding/json"
	"net/url"
	"strings"
)

// ResourceWebURL builds an absolute SigNoz web-UI deep link for a single-id
// resource type from a normalized base origin (scheme://host[:port]).
//
// Supported resourceType values: "dashboard", "alert", "service", "trace". Any
// other type, an empty base, or an empty id returns ("", false) so callers omit
// the field rather than emitting a broken link. Path/query segments are
// URL-encoded.
func ResourceWebURL(base, resourceType, id string) (string, bool) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || strings.TrimSpace(id) == "" {
		return "", false
	}

	switch resourceType {
	case "dashboard":
		return base + "/dashboard/" + url.PathEscape(id), true
	case "alert":
		q := url.Values{}
		q.Set("ruleId", id)
		return base + "/alerts/overview?" + q.Encode(), true
	case "service":
		return base + "/services/" + url.PathEscape(id), true
	case "trace":
		return base + "/trace/" + url.PathEscape(id), true
	default:
		return "", false
	}
}

// InjectWebURL adds a webUrl deep link to a single-resource passthrough JSON
// body. When the body is wrapped as {"data": {...}} the field is set on the
// inner object; otherwise it is set at the top level.
//
// The body is decoded only one level deep (two for the {"data": {...}} wrap)
// into map[string]json.RawMessage, so everything below the injection level —
// span trees, large int64 fields like durationNano, number formatting, key
// order — passes through as verbatim bytes rather than being re-encoded. On
// any failure — empty base, unsupported type, or a body that is not a JSON
// object — it returns the original bytes unchanged so enrichment can never
// corrupt a working response.
func InjectWebURL(data []byte, base, resourceType, id string) []byte {
	webURL, ok := ResourceWebURL(base, resourceType, id)
	if !ok {
		return data
	}

	obj, ok := decodeShallowObject(data)
	if !ok {
		return data
	}

	urlJSON, err := json.Marshal(webURL)
	if err != nil {
		return data
	}

	if inner, ok := decodeShallowObject(obj["data"]); ok {
		inner["webUrl"] = urlJSON
		innerJSON, err := json.Marshal(inner)
		if err != nil {
			return data
		}
		obj["data"] = innerJSON
	} else {
		obj["webUrl"] = urlJSON
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

// InjectRowsWebURL adds a per-row webUrl deep link to a query-builder v5 "raw"
// passthrough body, one link per result row built from the row's id field.
//
// The expected nesting (a render.Success envelope wrapping a QueryRangeResponse)
// is data.data.results[].rows[].data, with the id under rows[].data[idKey]; for
// traces that is data.traceID. Like InjectWebURL it decodes only as deep as each
// mutated level, leaving siblings — large int64 fields like durationNano, number
// formatting, key order — as verbatim json.RawMessage. Rows whose id is missing,
// empty, or non-string are left untouched. On any failure — empty base or a body
// that does not match the expected shape — it returns the original bytes
// unchanged so enrichment can never corrupt a working response.
func InjectRowsWebURL(data []byte, base, resourceType, idKey string) []byte {
	if strings.TrimSpace(base) == "" {
		return data
	}

	envelope, ok := decodeShallowObject(data)
	if !ok {
		return data
	}
	qrr, ok := decodeShallowObject(envelope["data"])
	if !ok {
		return data
	}
	queryData, ok := decodeShallowObject(qrr["data"])
	if !ok {
		return data
	}

	var results []json.RawMessage
	if err := json.Unmarshal(queryData["results"], &results); err != nil || results == nil {
		return data
	}

	changed := false
	for ri, rawResult := range results {
		result, ok := decodeShallowObject(rawResult)
		if !ok {
			continue
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(result["rows"], &rows); err != nil || rows == nil {
			continue
		}

		rowsChanged := false
		for i, rawRow := range rows {
			injected, ok := injectRowWebURL(rawRow, base, resourceType, idKey)
			if !ok {
				continue
			}
			rows[i] = injected
			rowsChanged = true
		}
		if !rowsChanged {
			continue
		}

		rowsJSON, err := json.Marshal(rows)
		if err != nil {
			return data
		}
		result["rows"] = rowsJSON
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return data
		}
		results[ri] = resultJSON
		changed = true
	}

	if !changed {
		return data
	}

	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return data
	}
	queryData["results"] = resultsJSON
	if !remarshalUp(envelope, qrr, queryData) {
		return data
	}

	out, err := json.Marshal(envelope)
	if err != nil {
		return data
	}
	return out
}

// injectRowWebURL adds a webUrl sibling to a single raw row's inner "data"
// object using rows[].data[idKey] as the resource id. ok is false (and the row
// is left untouched) when the row shape is unexpected, the id is missing/empty,
// or no link can be built for the type.
func injectRowWebURL(rawRow json.RawMessage, base, resourceType, idKey string) (json.RawMessage, bool) {
	row, ok := decodeShallowObject(rawRow)
	if !ok {
		return nil, false
	}
	rowData, ok := decodeShallowObject(row["data"])
	if !ok {
		return nil, false
	}

	var id string
	if err := json.Unmarshal(rowData[idKey], &id); err != nil {
		return nil, false
	}
	webURL, ok := ResourceWebURL(base, resourceType, id)
	if !ok {
		return nil, false
	}
	urlJSON, err := json.Marshal(webURL)
	if err != nil {
		return nil, false
	}

	rowData["webUrl"] = urlJSON
	rowDataJSON, err := json.Marshal(rowData)
	if err != nil {
		return nil, false
	}
	row["data"] = rowDataJSON
	rowJSON, err := json.Marshal(row)
	if err != nil {
		return nil, false
	}
	return rowJSON, true
}

// remarshalUp re-encodes the queryData -> qrr -> envelope nesting after results
// have been mutated, keeping every untouched sibling verbatim. Returns false on
// any marshal failure so the caller can fall open to the original bytes.
func remarshalUp(envelope, qrr, queryData map[string]json.RawMessage) bool {
	queryDataJSON, err := json.Marshal(queryData)
	if err != nil {
		return false
	}
	qrr["data"] = queryDataJSON
	qrrJSON, err := json.Marshal(qrr)
	if err != nil {
		return false
	}
	envelope["data"] = qrrJSON
	return true
}

// decodeShallowObject decodes raw as a JSON object one level deep: values stay
// verbatim json.RawMessage bytes. ok is false when raw is empty, "null", or
// not a JSON object (Unmarshal leaves the map nil for "null", so the nil check
// also guards writes into a nil map).
func decodeShallowObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}
