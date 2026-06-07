package util

import (
	"net/url"
	"strings"
)

// TenantURLAllowlist restricts which SigNoz backend hosts a multi-tenant
// deployment is willing to proxy to. It is built from a comma-separated list of
// host patterns (see ParseTenantURLAllowlist). A zero-value / empty allowlist
// permits every host, preserving the unrestricted default behavior.
type TenantURLAllowlist struct {
	// exact holds lowercased hostnames that must match in full.
	exact map[string]struct{}
	// suffixes holds dot-anchored suffixes (e.g. ".us.signoz.cloud") derived
	// from "*.us.signoz.cloud" patterns. A host matches when it ends with the
	// suffix and has at least one label in front of it.
	suffixes []string
}

// ParseTenantURLAllowlist parses a comma-separated list of host patterns into a
// TenantURLAllowlist. Each non-empty entry is either:
//
//   - an exact hostname, e.g. "signoz.example.com"; or
//   - a wildcard "*.suffix", e.g. "*.us.signoz.cloud", which matches any host
//     ending in ".suffix" (so it spans multiple labels: "*.signoz.cloud"
//     matches "tough-gecko.us.signoz.cloud"). The bare suffix itself
//     ("us.signoz.cloud") is NOT matched by its wildcard.
//
// Matching is hostname-only and case-insensitive. A scheme, port, or path
// accidentally included in an entry is tolerated and stripped to the host.
func ParseTenantURLAllowlist(raw string) TenantURLAllowlist {
	al := TenantURLAllowlist{exact: make(map[string]struct{})}
	for _, entry := range strings.Split(raw, ",") {
		pattern := stripToHostPattern(strings.ToLower(strings.TrimSpace(entry)))
		if pattern == "" {
			continue
		}
		if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
			if suffix != "" {
				al.suffixes = append(al.suffixes, "."+suffix)
			}
			continue
		}
		al.exact[pattern] = struct{}{}
	}
	return al
}

// Configured reports whether the allowlist contains any patterns. When false,
// every host is allowed.
func (a TenantURLAllowlist) Configured() bool {
	return len(a.exact) > 0 || len(a.suffixes) > 0
}

// AllowsHost reports whether host is permitted. An unconfigured allowlist allows
// everything; otherwise the host must exact-match or fall under a wildcard
// suffix.
func (a TenantURLAllowlist) AllowsHost(host string) bool {
	if !a.Configured() {
		return true
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if _, ok := a.exact[host]; ok {
		return true
	}
	for _, suffix := range a.suffixes {
		// len check ensures at least one label precedes the dot-anchored
		// suffix, so "*.us.signoz.cloud" does not match the apex
		// "us.signoz.cloud".
		if len(host) > len(suffix) && strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// AllowsURL parses a normalized origin URL (scheme://host[:port]) and reports
// whether its host is permitted. A malformed URL is rejected when the allowlist
// is configured.
func (a TenantURLAllowlist) AllowsURL(rawURL string) bool {
	if !a.Configured() {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return a.AllowsHost(parsed.Hostname())
}

// stripToHostPattern reduces an allowlist entry to its host portion, tolerating
// operators who paste a full URL ("https://*.us.signoz.cloud/") instead of a
// bare host pattern.
func stripToHostPattern(pattern string) string {
	if i := strings.Index(pattern, "://"); i >= 0 {
		pattern = pattern[i+3:]
	}
	if i := strings.IndexAny(pattern, "/?#"); i >= 0 {
		pattern = pattern[:i]
	}
	return pattern
}
