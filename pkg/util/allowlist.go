package util

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MCPDocsURL is linked in tenant-not-permitted rejection messages.
const MCPDocsURL = "https://signoz.io/docs/ai/signoz-mcp-server/"

// TenantURLAllowlist restricts which SigNoz backend hosts a multi-tenant
// deployment will proxy to (see ParseTenantURLAllowlist). An empty allowlist
// permits every host.
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
//   - a wildcard "*.suffix", e.g. "*.us.signoz.cloud", matching any host ending
//     in ".suffix" across labels ("*.signoz.cloud" matches
//     "tough-gecko.us.signoz.cloud"); the bare apex "us.signoz.cloud" is not.
//
// Matching is host-only and case-insensitive; a pasted scheme/port/path in an
// entry is stripped to the bare host.
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
		// len() guard requires a label before the suffix, excluding the apex.
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

// stripToHostPattern reduces an entry to its bare host — dropping a pasted
// scheme/port/path and unwrapping IPv6 brackets — so it compares equal to
// url.Hostname(), which never carries the port.
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

// TenantNotPermittedMessage is the rejection message for a tenant URL the
// allowlist refuses: SigNoz Cloud users should use their region's MCP URL and
// self-hosted users should run their own MCP. Links to the docs.
func TenantNotPermittedMessage() string {
	return fmt.Sprintf("This SigNoz instance is not served by this MCP endpoint. SigNoz Cloud users must use their "+
		"region's MCP URL (https://mcp.<region>.signoz.cloud/mcp); self-hosted users should run the SigNoz MCP "+
		"server themselves. Docs: %s", MCPDocsURL)
}
