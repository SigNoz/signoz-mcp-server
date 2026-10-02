package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func TestHandleListDashboards_UsesPublicWebURLWithoutChangingAPIURL(t *testing.T) {
	for _, tc := range []struct {
		name         string
		localhostAPI bool
		webURL       string
	}{
		{"numeric loopback backend", false, "https://signoz.example.com/"},
		{"localhost backend", true, "https://signoz.example.com/"},
		{"localhost browser", false, "http://localhost:3301/"},
		{"unconfigured external URL", true, ""},
		{"external base path", false, "https://signoz.example.com/observability/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan *http.Request, 2)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/global/config" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"external_url": tc.webURL}})
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
			gotPaths := map[string]bool{}
			for range 2 {
				select {
				case request := <-requests:
					gotPaths[request.URL.Path] = true
					if request.Method != http.MethodGet || request.Header.Get("SIGNOZ-API-KEY") != "test-key" {
						t.Error("API request did not retain method and credentials")
					}
				default:
					t.Fatal("API backend did not receive both resource and global config requests")
				}
			}
			if !gotPaths["/api/v1/global/config"] || !gotPaths["/api/v2/dashboards"] {
				t.Fatalf("unexpected API paths: %v", gotPaths)
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

func TestHandleCreateDashboard_GlobalConfigAuthFailurePreventsWrite(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, truncated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/truncated=%t", status, truncated), func(t *testing.T) {
				var writes, discoveries atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/global/config" {
						discoveries.Add(1)
						if truncated {
							w.Header().Set("Content-Length", "100")
						}
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"error":"denied"}`))
						return
					}
					writes.Add(1)
					_, _ = w.Write([]byte(`{"id":"abc-123"}`))
				}))
				defer backend.Close()
				h := NewHandler(logpkg.New("error"), &config.Config{URL: backend.URL, ClientCacheSize: 4})
				ctx := util.SetAPIKey(util.SetSigNozURL(testCtx(), backend.URL), "test-key")
				for range 2 {
					result, err := h.handleCreateDashboard(ctx, makeToolRequest("signoz_create_dashboard", map[string]any{
						"schemaVersion": "v6", "name": "test-dashboard", "spec": map[string]any{"display": map[string]any{"name": "Test"}, "panels": map[string]any{}},
					}))
					if err != nil || !result.IsError {
						t.Fatalf("expected coded error: %v %v", err, result)
					}
					want := "UNAUTHORIZED"
					if status == http.StatusForbidden {
						want = "PERMISSION_DENIED"
					}
					body, _ := json.Marshal(result.StructuredContent)
					if !strings.Contains(string(body), want) {
						t.Fatalf("expected %s, got %s", want, body)
					}
				}
				if writes.Load() != 0 || discoveries.Load() != 2 {
					t.Fatalf("auth rejection must prevent writes and stay uncached: writes=%d discoveries=%d", writes.Load(), discoveries.Load())
				}
			})
		}
	}
}

