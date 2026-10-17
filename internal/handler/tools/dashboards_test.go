package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func TestHandleListDashboards_UsesPublicWebURLWithoutChangingAPIURL(t *testing.T) {
	for _, tc := range []struct {
		name         string
		localhostAPI bool
		webURL       string
	}{
		{"configured external URL", true, "https://signoz.example.com"},
		{"unconfigured external URL", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("SIGNOZ-API-KEY") != "test-key" {
					t.Errorf("API request = %s %s; discovery and dashboard reads must retain GET and tenant credentials", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/global/config" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"external_url": tc.webURL}})
					return
				}
				if r.URL.Path != "/api/v2/dashboards" {
					t.Errorf("unexpected API request: %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"data":{"dashboards":[{"id":"abc-123","name":"Hosts"}],"tags":[],"total":1}}`))
			}))
			defer backend.Close()
			apiURL := backend.URL
			if tc.localhostAPI {
				apiURL = strings.Replace(apiURL, "127.0.0.1", "localhost", 1)
			}
			t.Setenv(config.SignozURL, apiURL)
			t.Setenv(config.SignozApiKey, "test-key")
			cfg, err := config.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			h := NewHandler(logpkg.New("error"), cfg)
			ctx := util.SetAPIKey(util.SetSigNozURL(testCtx(), cfg.URL), cfg.APIKey)
			result, err := h.handleListDashboards(ctx, makeToolRequest("signoz_list_dashboards", map[string]any{}))
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("handler returned error result: %v", result.Content)
			}
			base := strings.TrimSuffix(tc.webURL, "/")
			if base == "" {
				base = apiURL
			}
			want := `"webUrl":"` + base + `/dashboard/abc-123"`
			if body := textContent(t, result); !strings.Contains(body, want) {
				t.Fatalf("expected %s, got: %s", want, body)
			}
		})
	}
}

func TestHandleListDashboards_DiscoversEachTenantsWebURL(t *testing.T) {
	makeBackend := func(webURL, key string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("SIGNOZ-API-KEY") != key {
				t.Error("request crossed tenant credentials")
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/global/config":
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"external_url": webURL}})
			case "/api/v2/dashboards":
				_, _ = w.Write([]byte(`{"dashboards":[{"id":"abc-123"}],"total":1}`))
			default:
				t.Errorf("unexpected request: %s", r.URL.Path)
			}
		}))
	}
	first := makeBackend("https://first.example.com", "first-key")
	defer first.Close()
	second := makeBackend("https://second.example.com", "second-key")
	defer second.Close()
	h := NewHandler(logpkg.New("error"), &config.Config{URL: first.URL, ClientCacheSize: 8})
	for _, tc := range []struct{ backend, key, web string }{
		{first.URL, "first-key", "https://first.example.com"},
		{second.URL, "second-key", "https://second.example.com"},
		{first.URL, "first-key", "https://first.example.com"},
	} {
		ctx := util.SetAPIKey(util.SetSigNozURL(testCtx(), tc.backend), tc.key)
		result, err := h.handleListDashboards(ctx, makeToolRequest("signoz_list_dashboards", map[string]any{}))
		if err != nil || result.IsError {
			t.Fatalf("handler error: %v, %v", err, result)
		}
		want := `"webUrl":"` + tc.web + `/dashboard/abc-123"`
		if body := textContent(t, result); !strings.Contains(body, want) {
			t.Fatalf("expected %s, got %s", want, body)
		}
	}
}

func TestDashboardWrites_GlobalConfigAuthFailurePreventsMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Handler, context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args map[string]any
	}{
		{"create", (*Handler).handleCreateDashboard, map[string]any{"schemaVersion": "v6", "name": "test-dashboard", "spec": map[string]any{"display": map[string]any{"name": "Test"}}}},
		{"update", (*Handler).handleUpdateDashboard, map[string]any{"id": "abc-123", "schemaVersion": "v6", "name": "test-dashboard", "spec": map[string]any{"display": map[string]any{"name": "Test"}}}},
		{"patch", (*Handler).handlePatchDashboard, map[string]any{"id": "abc-123", "patch": []any{map[string]any{"op": "replace", "path": "/spec/display/name", "value": "Test"}}}},
	} {
		for _, failure := range []struct {
			status int
			code   string
		}{
			{http.StatusUnauthorized, CodeUnauthorized},
			{http.StatusForbidden, CodePermissionDenied},
		} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, failure.status), func(t *testing.T) {
				var mutations atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/global/config" {
						w.WriteHeader(failure.status)
						_, _ = w.Write([]byte(`{"error":"denied"}`))
						return
					}
					mutations.Add(1)
					t.Errorf("discovery auth rejection must prevent resource requests, got %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}))
				t.Cleanup(backend.Close)
				h := NewHandler(logpkg.New("error"), &config.Config{URL: backend.URL, ClientCacheSize: 4})
				ctx := util.SetAPIKey(util.SetSigNozURL(testCtx(), backend.URL), "test-key")
				result, err := tc.call(h, ctx, makeToolRequest("signoz_"+tc.name+"_dashboard", maps.Clone(tc.args)))
				if err != nil || result == nil || !result.IsError {
					t.Fatalf("discovery auth rejection must return a coded tool error: err=%v result=%v", err, result)
				}
				structured := resultStructuredMap(t, result)
				if structured["code"] != failure.code || structured["status"] != failure.status {
					t.Fatalf("discovery auth rejection = %#v, want %s with status %d", structured, failure.code, failure.status)
				}
				if mutations.Load() != 0 {
					t.Fatalf("discovery auth rejection allowed %d resource requests; abort before mutation", mutations.Load())
				}
			})
		}
	}
}
