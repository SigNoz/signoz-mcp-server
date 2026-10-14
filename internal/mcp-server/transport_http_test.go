package mcp_server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

func TestProductionHTTPDeclaredBodyLimitPrecedesAuthentication(t *testing.T) {
	logger := logpkg.New("error")
	cfg := &config.Config{
		URL:             "https://tenant.example.com",
		ClientCacheSize: 1,
		ClientCacheTTL:  time.Minute,
		MaxRequestBytes: 16,
	}
	m := NewMCPServer(logger, tools.NewHandler(logger, cfg), cfg, noopanalytics.New(), nil)
	handler := m.buildHTTP(m.newSDKServer()).Handler

	oversized := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", 17)))
	oversizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", oversizedResponse.Code)
	}

	unauthenticated := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("under-limit unauthenticated status = %d, want 401", unauthenticatedResponse.Code)
	}
}

func TestProductionHTTPStatelessMethods(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/mcp", nil)
			req.Header.Set("Mcp-Session-Id", "client-supplied-session")
			rr := httptest.NewRecorder()
			oracle.handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405; body=%s", rr.Code, rr.Body.String())
			}
			if got := rr.Header().Get("Allow"); got != http.MethodPost {
				t.Fatalf("Allow = %q, want POST", got)
			}
			if got := rr.Header().Get("Mcp-Session-Id"); got != "" {
				t.Fatalf("Mcp-Session-Id = %q, want absent", got)
			}
		})
	}
}

func TestProductionHTTPCrossOriginProtection(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)
	body := protocolRequestJSON(t, 61, "tools/list", map[string]any{})

	tests := []struct {
		name       string
		origin     string
		fetchSite  string
		wantStatus int
	}{
		{name: "non-browser request", wantStatus: http.StatusOK},
		{name: "same origin", origin: "http://example.com", wantStatus: http.StatusOK},
		{name: "same-origin fetch metadata", origin: "http://example.com", fetchSite: "same-origin", wantStatus: http.StatusOK},
		{name: "cross origin", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
		{name: "cross-site fetch metadata", origin: "http://example.com", fetchSite: "cross-site", wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{"Mcp-Protocol-Version": {"2025-11-25"}}
			if tt.origin != "" {
				headers.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				headers.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			response := protocolPOST(t, oracle.handler, body, headers)
			if response.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.status, tt.wantStatus, response.body)
			}
		})
	}
}

func TestProductionHTTPContentNegotiation(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)
	body := protocolRequestJSON(t, 71, "tools/list", map[string]any{})

	t.Run("content type", func(t *testing.T) {
		tests := []struct {
			name        string
			contentType string
			wantStatus  int
		}{
			{name: "json", contentType: "application/json", wantStatus: http.StatusOK},
			{name: "json utf8", contentType: "application/json; charset=utf-8", wantStatus: http.StatusOK},
			{name: "json case variant parameter", contentType: "application/json; CHARSET=UTF-8", wantStatus: http.StatusOK},
			{name: "missing", contentType: "", wantStatus: http.StatusUnsupportedMediaType},
			{name: "wrong", contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				response := protocolPOST(t, oracle.handler, body, http.Header{
					"Content-Type":         {tt.contentType},
					"Mcp-Protocol-Version": {"2025-11-25"},
				})
				if response.status != tt.wantStatus {
					t.Fatalf("status = %d, want %d; body=%s", response.status, tt.wantStatus, response.body)
				}
				if tt.wantStatus == http.StatusOK && !strings.HasPrefix(response.header.Get("Content-Type"), "application/json") {
					t.Fatalf("response Content-Type = %q, want application/json", response.header.Get("Content-Type"))
				}
			})
		}
	})

	t.Run("accept", func(t *testing.T) {
		tests := []struct {
			name       string
			accept     []string
			wantStatus int
		}{
			{name: "json and event stream", accept: []string{"application/json, text/event-stream"}, wantStatus: http.StatusOK},
			{name: "parameters", accept: []string{"application/json;charset=utf-8, text/event-stream"}, wantStatus: http.StatusOK},
			{name: "separate header values", accept: []string{"application/json;charset=utf-8", "text/event-stream"}, wantStatus: http.StatusOK},
			{name: "wildcard", accept: []string{"*/*"}, wantStatus: http.StatusOK},
			{name: "type wildcards", accept: []string{"application/*, text/*"}, wantStatus: http.StatusOK},
			{name: "json only", accept: []string{"application/json"}, wantStatus: http.StatusBadRequest},
			{name: "event stream only", accept: []string{"text/event-stream"}, wantStatus: http.StatusBadRequest},
			{name: "missing", wantStatus: http.StatusBadRequest},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				response := protocolPOST(t, oracle.handler, body, http.Header{
					"Accept":               tt.accept,
					"Mcp-Protocol-Version": {"2025-11-25"},
				})
				if response.status != tt.wantStatus {
					t.Fatalf("status = %d, want %d; body=%s", response.status, tt.wantStatus, response.body)
				}
			})
		}
	})
}

func TestProductionHTTPModernBatchRejected(t *testing.T) {
	oracle := newWireOracle(t)
	t.Cleanup(oracle.close)

	request := protocolRequestJSON(t, 81, "tools/list", modernParams(nil, nil))
	body := "[" + request + "," + request + "]"
	response := protocolPOST(t, oracle.handler, body, http.Header{
		"Mcp-Protocol-Version": {modernProtocolVersion},
	})
	if response.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", response.status, response.body)
	}
	if !strings.Contains(strings.ToLower(string(response.body)), "batch") {
		t.Fatalf("body = %q, want batch rejection", response.body)
	}
	if got := response.header.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("Mcp-Session-Id = %q, want absent", got)
	}
}
