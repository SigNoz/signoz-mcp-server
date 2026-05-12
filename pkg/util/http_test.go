package util

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClientAddressValidatesForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest("POST", "https://internal.example/mcp", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("X-Forwarded-For", "not-an-ip, 198.51.100.1")
	req.Header.Set("X-Real-IP", "198.51.100.7")

	if got := HTTPClientAddress(req); got != "198.51.100.7" {
		t.Fatalf("HTTPClientAddress() = %q, want X-Real-IP fallback", got)
	}

	req.Header.Set("X-Forwarded-For", "198.51.100.9, 198.51.100.1")
	if got := HTTPClientAddress(req); got != "198.51.100.9" {
		t.Fatalf("HTTPClientAddress() = %q, want first valid X-Forwarded-For value", got)
	}
}

func TestHTTPServerAddressPrefersForwardedHost(t *testing.T) {
	req := httptest.NewRequest("POST", "https://internal.example/mcp", nil)
	req.Host = "internal.service.local:8080"
	req.Header.Set("X-Forwarded-Host", "mcp.us.signoz.cloud, proxy.local")

	if got := HTTPServerAddress(req); got != "mcp.us.signoz.cloud" {
		t.Fatalf("HTTPServerAddress() = %q, want forwarded host", got)
	}

	req.Header.Del("X-Forwarded-Host")
	req.Header.Set("Forwarded", `for=198.51.100.9;proto=https;host="mcp.eu.signoz.cloud"`)
	if got := HTTPServerAddress(req); got != "mcp.eu.signoz.cloud" {
		t.Fatalf("HTTPServerAddress() = %q, want RFC Forwarded host", got)
	}
}

func TestHTTPServerAddressNormalizesQuotedForwardedHostPort(t *testing.T) {
	tests := []struct {
		name      string
		forwarded string
		want      string
	}{
		{
			name:      "hostname with port",
			forwarded: `for=198.51.100.9;proto=https;host="mcp.us.signoz.cloud:443"`,
			want:      "mcp.us.signoz.cloud",
		},
		{
			name:      "bracketed IPv6 with port",
			forwarded: `for=198.51.100.9;proto=https;host="[2001:db8::1]:443"`,
			want:      "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "https://internal.example/mcp", nil)
			req.Header.Set("Forwarded", tt.forwarded)

			if got := HTTPServerAddress(req); got != tt.want {
				t.Fatalf("HTTPServerAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPUserAgentIsBounded(t *testing.T) {
	req := httptest.NewRequest("POST", "https://mcp.example.com/mcp", nil)
	req.Header.Set("User-Agent", strings.Repeat("a", CallerCorrelationHeaderMaxLen+10))

	if got := HTTPUserAgent(req); len(got) != CallerCorrelationHeaderMaxLen {
		t.Fatalf("HTTPUserAgent() length = %d, want %d", len(got), CallerCorrelationHeaderMaxLen)
	}
}
