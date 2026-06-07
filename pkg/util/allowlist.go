package util

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MCPDocsURL links to the hosted MCP connection / region documentation, shown
// in tenant-not-permitted rejection messages.
const MCPDocsURL = "https://signoz.io/docs/ai/signoz-mcp-server/"

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
// accidentally included in an entry is tolerated and stripped to the bare host,
// so a pasted full URL like "https://demo.us.signoz.cloud:8080/" still matches.
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

// stripToHostPattern reduces an allowlist entry to its bare host, tolerating
// operators who paste a full URL ("https://*.us.signoz.cloud:443/") instead of
// a bare host pattern. The port is dropped (and IPv6 brackets unwrapped) so
// entries compare equal to url.Hostname(), which never carries the port.
func stripToHostPattern(pattern string) string {
	if i := strings.Index(pattern, "://"); i >= 0 {
		pattern = pattern[i+3:]
	}
	if i := strings.IndexAny(pattern, "/?#"); i >= 0 {
		pattern = pattern[:i]
	}
	if host, _, err := net.SplitHostPort(pattern); err == nil {
		return host
	}
	if strings.HasPrefix(pattern, "[") && strings.HasSuffix(pattern, "]") {
		return pattern[1 : len(pattern)-1]
	}
	return pattern
}

// signozCloudRegion returns the region label of a "<tenant>.<region>.signoz.cloud"
// host (e.g. "us" for "tough-gecko.us.signoz.cloud"), or "" when host is not a
// multi-label signoz.cloud tenant.
func signozCloudRegion(host string) string {
	const suffix = ".signoz.cloud"
	host = strings.ToLower(host)
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	labels := strings.Split(strings.TrimSuffix(host, suffix), ".")
	if len(labels) < 2 {
		return ""
	}
	return labels[len(labels)-1]
}

// TenantNotPermittedMessage builds the user-facing rejection message for a
// tenant URL that the allowlist refuses. For a SigNoz Cloud host it names the
// region and the correct regional MCP URL; otherwise it gives generic guidance.
// Both variants link to the MCP docs.
func TenantNotPermittedMessage(signozURL string) string {
	host := ""
	if u, err := url.Parse(signozURL); err == nil {
		host = u.Hostname()
	}
	if region := signozCloudRegion(host); region != "" {
		return fmt.Sprintf("SigNoz instance %s is in the %q region, which this MCP endpoint does not serve. "+
			"Connect to your region's MCP URL instead: https://mcp.%s.signoz.cloud/mcp . Docs: %s",
			host, region, region, MCPDocsURL)
	}
	return fmt.Sprintf("This SigNoz instance is not served by this MCP endpoint. SigNoz Cloud users must use their "+
		"region's MCP URL (https://mcp.<region>.signoz.cloud/mcp); self-hosted users should run the SigNoz MCP "+
		"server themselves. Docs: %s", MCPDocsURL)
}
