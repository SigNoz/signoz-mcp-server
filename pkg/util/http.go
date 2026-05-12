package util

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const HeaderMCPSessionID = "Mcp-Session-Id"

// HTTPClientAddress returns the best available client address for request
// telemetry. It prefers forwarded headers because production traffic reaches
// the server through ingress/proxy layers.
func HTTPClientAddress(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		if client := normalizeClientAddress(strings.Split(forwardedFor, ",")[0]); client != "" {
			return client
		}
	}
	if realIP := normalizeClientAddress(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if client := normalizeClientAddress(host); client != "" {
			return client
		}
	}
	return NormalizeCallerCorrelationValue(r.RemoteAddr)
}

// HTTPServerAddress returns the host handling the request without a port.
func HTTPServerAddress(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := forwardedHost(r.Header.Get("Forwarded"))
	if host == "" {
		host = firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	}
	if host == "" {
		host = r.Host
	}
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	return normalizeServerAddress(host)
}

func normalizeServerAddress(host string) string {
	host = strings.TrimSpace(host)
	host = strings.Trim(host, "\"")
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return NormalizeCallerCorrelationValue(strings.Trim(host, " []\""))
}

// HTTPUserAgent returns a bounded user-agent value for request telemetry.
func HTTPUserAgent(r *http.Request) string {
	if r == nil {
		return ""
	}
	return NormalizeCallerCorrelationValue(r.UserAgent())
}

// HTTPSessionID returns the MCP session ID header after basic normalization.
func HTTPSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return NormalizeCallerCorrelationValue(r.Header.Get(HeaderMCPSessionID))
}

func normalizeClientAddress(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "\"[]")
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = strings.Trim(host, "[]")
	}
	if _, err := netip.ParseAddr(s); err != nil {
		return ""
	}
	return s
}

func firstHeaderValue(s string) string {
	return strings.TrimSpace(strings.Split(s, ",")[0])
}

func forwardedHost(header string) string {
	for _, field := range strings.Split(firstHeaderValue(header), ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok || !strings.EqualFold(key, "host") {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}
