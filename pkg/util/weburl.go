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
// span trees, large int64 fields like duration_nano, number formatting, key
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

// InjectRowsResult reports what InjectRowsWebURL observed while walking a v5 raw
// body, so callers can distinguish ordinary "no data" from a probable upstream
// shape change. Because enrichment fails open, that drift is otherwise silent;
// these fields give callers a signal to surface. Three drift modes, each at a
// deeper level of the nesting:
//   - envelope drift: ResultsReached == false — the data.data.results[] array
//     could not be walked (the envelope nesting changed or was renamed);
//   - rows-key drift: ResultCount > 0 but RowsArraysReached == 0 — result objects
//     were present, but none exposed a readable rows[] array (the per-result
//     "rows" key was renamed/removed). The v5 contract always emits a "rows" key
//     (an array, or null when empty), so this only happens on a shape change;
//   - column-alias drift: RowsSeen > 0 but RowsEnriched == 0 — rows were present
//     yet none carried a linkable id (the id column alias was renamed).
//
// An ordinary empty result is ResultsReached == true with either ResultCount == 0
// or RowsArraysReached > 0 && RowsSeen == 0 (a present-but-empty rows[]). All
// fields are meaningful only when a base was supplied (an empty base returns
// early with the zero value, since enrichment was not attempted).
type InjectRowsResult struct {
	ResultsReached    bool
	ResultCount       int
	RowsArraysReached int
	RowsSeen          int
	RowsEnriched      int
}

// InjectRowsWebURL adds a per-row webUrl deep link to a query-builder v5 "raw"
// passthrough body, one link per result row built from the row's id field. It
// returns the (possibly enriched) body and an InjectRowsResult describing how
// many rows it saw versus enriched.
//
// The expected nesting (a render.Success envelope wrapping a QueryRangeResponse)
// is data.data.results[].rows[].data, with the id under the first matching
// rows[].data[idKeys] entry; for traces that is data.trace_id, with legacy
// fallback keys during migration. Like InjectWebURL it decodes only as deep as each
// mutated level, leaving siblings — large int64 fields like duration_nano, number
// formatting, key order — as verbatim json.RawMessage. Rows whose id is missing,
// empty, or non-string are left untouched. On any failure — empty base or a body
// that does not match the expected shape — it returns the original bytes
// unchanged so enrichment can never corrupt a working response.
func InjectRowsWebURL(data []byte, base, resourceType string, idKeys ...string) ([]byte, InjectRowsResult) {
	var res InjectRowsResult
	if strings.TrimSpace(base) == "" || len(idKeys) == 0 {
		return data, res
	}

	envelope, ok := decodeShallowObject(data)
	if !ok {
		return data, res
	}
	qrr, ok := decodeShallowObject(envelope["data"])
	if !ok {
		return data, res
	}
	queryData, ok := decodeShallowObject(qrr["data"])
	if !ok {
		return data, res
	}

	var results []json.RawMessage
	if err := json.Unmarshal(queryData["results"], &results); err != nil || results == nil {
		return data, res
	}
	res.ResultsReached = true
	res.ResultCount = len(results)

	changed := false
	for ri, rawResult := range results {
		result, ok := decodeShallowObject(rawResult)
		if !ok {
			continue
		}
		rawRows, present := result["rows"]
		if !present {
			continue // "rows" key absent — always present in the v5 contract, so this is drift
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(rawRows, &rows); err != nil {
			continue // "rows" present but not an array (renamed to a different type) — drift
		}
		// A present "rows" that decodes as an array or null (rows == nil) means the
		// per-result shape is intact even when empty — count it so an ordinary
		// empty result is not mistaken for rows-key drift.
		res.RowsArraysReached++

		rowsChanged := false
		for i, rawRow := range rows {
			res.RowsSeen++
			injected, ok := injectRowWebURL(rawRow, base, resourceType, idKeys)
			if !ok {
				continue
			}
			rows[i] = injected
			res.RowsEnriched++
			rowsChanged = true
		}
		if !rowsChanged {
			continue
		}

		rowsJSON, err := json.Marshal(rows)
		if err != nil {
			return data, res
		}
		result["rows"] = rowsJSON
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return data, res
		}
		results[ri] = resultJSON
		changed = true
	}

	if !changed {
		return data, res
	}

	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return data, res
	}
	queryData["results"] = resultsJSON
	if !remarshalUp(envelope, qrr, queryData) {
		return data, res
	}

	out, err := json.Marshal(envelope)
	if err != nil {
		return data, res
	}
	return out, res
}

// injectRowWebURL adds a webUrl sibling to a single raw row's inner "data"
// object using the first non-empty rows[].data[idKeys] string as the resource id.
// ok is false (and the row
// is left untouched) when the row shape is unexpected, the id is missing/empty,
// or no link can be built for the type.
func injectRowWebURL(rawRow json.RawMessage, base, resourceType string, idKeys []string) (json.RawMessage, bool) {
	row, ok := decodeShallowObject(rawRow)
	if !ok {
		return nil, false
	}
	rowData, ok := decodeShallowObject(row["data"])
	if !ok {
		return nil, false
	}

	id, ok := firstStringValue(rowData, idKeys)
	if !ok {
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

func firstStringValue(obj map[string]json.RawMessage, keys []string) (string, bool) {
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		var value string
		if err := json.Unmarshal(obj[key], &value); err != nil {
			continue
		}
		if strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
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
