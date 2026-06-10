package util

import (
	"net/url"
	"strings"
)

// ResourceWebURL builds an absolute SigNoz web-UI deep link for a single-id
// resource type from a normalized base origin (scheme://host[:port]).
//
// Supported resourceType values: "dashboard", "alert", "service". Any other
// type, an empty base, or an empty id returns ("", false) so callers omit the
// field rather than emitting a broken link. Path/query segments are URL-encoded.
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
	default:
		return "", false
	}
}
