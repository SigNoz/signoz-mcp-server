package util

import (
	"bytes"
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
// It decodes with UseNumber so large int64 fields (e.g. trace durationNano,
// span ids, epoch-nanosecond timestamps) are preserved verbatim rather than
// being coerced through float64 and rounded. On any failure — empty base,
// unsupported type, or unparseable body — it returns the original bytes
// unchanged so enrichment can never corrupt a working response.
func InjectWebURL(data []byte, base, resourceType, id string) []byte {
	webURL, ok := ResourceWebURL(base, resourceType, id)
	if !ok {
		return data
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return data
	}

	if inner, ok := obj["data"].(map[string]any); ok {
		inner["webUrl"] = webURL
	} else {
		obj["webUrl"] = webURL
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}
