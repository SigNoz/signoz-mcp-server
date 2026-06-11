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
