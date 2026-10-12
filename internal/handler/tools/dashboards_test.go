package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		{"unset override", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan *http.Request, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"dashboards":[{"id":"abc-123","name":"Hosts"}],"tags":[],"total":1}}`))
			}))
			defer backend.Close()
			apiURL := backend.URL
			if tc.localhostAPI {
				apiURL = strings.Replace(apiURL, "127.0.0.1", "localhost", 1)
			}
			t.Setenv(config.SignozURL, apiURL)
			t.Setenv(config.SignozWebURL, tc.webURL)
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
			select {
			case request := <-requests:
				if request.Method != http.MethodGet || request.URL.Path != "/api/v2/dashboards" {
					t.Errorf("API request = %s %s, want GET /api/v2/dashboards", request.Method, request.URL.Path)
				}
				if request.Header.Get("SIGNOZ-API-KEY") != "test-key" {
					t.Error("API request did not retain its credentials")
				}
			default:
				t.Fatal("API backend did not receive the request")
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
