package mcp_server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestInitializeDoesNotAdvertiseResourceSubscribe pins the capability
// contract: this server registers resources but never implements
// resources/subscribe, so the initialize result must not advertise
// resources.subscribe — well-behaved clients should never attempt it.
func TestInitializeDoesNotAdvertiseResourceSubscribe(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(logpkg.New("error"), handler, cfg, noopanalytics.New(), nil)

	s := mcpServer.newSDKServer()
	// Register at least one resource so the resources capability is present
	// at all — the load-bearing assertion is that subscribe stays unset even
	// when resources are advertised.
	handler.RegisterQueryBuilderV5Handlers(s)

	client, err := newIntegrationClient(t, s)
	if err != nil {
		t.Fatal(err)
	}
	result := client.client.InitializeResult()
	if result.Capabilities.Resources == nil {
		t.Fatal("resources capability missing from initialize result")
	}
	if result.Capabilities.Resources.Subscribe {
		t.Fatal("initialize result advertises resources.subscribe")
	}
}

// TestMethodErrorLogLevel pins the hook-level severity classification:
// rejections of unadvertised optional capabilities (server.ErrUnsupported,
// e.g. resources/subscribe) and client cancellations log at DEBUG, while
// deadline-exceeded and generic errors stay ERROR.
func TestMethodErrorLogLevel(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want slog.Level
	}{
		{"resources subscribe not supported", &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "unsupported"}, slog.LevelDebug},
		{"client canceled", fmt.Errorf(`Post "https://tenant.signoz.cloud/api/v5/query_range": %w`, context.Canceled), slog.LevelDebug},
		{"deadline exceeded", fmt.Errorf("query: %w", context.DeadlineExceeded), slog.LevelError},
		{"generic", errors.New("boom"), slog.LevelError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := methodErrorLogLevel(tc.err); got != tc.want {
				t.Fatalf("methodErrorLogLevel(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestBuildHooks_ErrorLogSeverityClassification exercises the OnError hook
// end-to-end through a debug-level buffered logger and asserts that expected
// protocol noise (resources/subscribe rejections, client cancellations) is
// still emitted — fail open, never fail silent — but below ERROR, while
// deadline-exceeded and generic failures remain ERROR.
func TestReceivingMiddleware_ErrorLogSeverityClassification(t *testing.T) {
	var buf lockedBuffer
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(newBufferedLogger(&buf, slog.LevelDebug), handler, cfg, noopanalytics.New(), nil)
	middleware := mcpServer.receivingMiddleware(func(string) bool { return false })
	fail := func(method, id string, err error) {
		ctx := newAnalyticsTestContext(context.Background(), "sess-"+id)
		handler := middleware(func(context.Context, string, mcp.Request) (mcp.Result, error) { return nil, err })
		_, _ = handler(ctx, method, nil)
	}

	fail("resources/subscribe", "req-sub", &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "unsupported"})
	fail("resources/read", "req-cancel", fmt.Errorf("read: %w", context.Canceled))
	fail("resources/list", "req-deadline", fmt.Errorf("list: %w", context.DeadlineExceeded))
	fail("prompts/list", "req-generic", errors.New("boom"))

	levels := map[string]string{}
	for _, rec := range parseJSONLogLines(t, &buf) {
		if rec["msg"] != "mcp error" {
			continue
		}
		method, _ := rec["mcp.method.name"].(string)
		level, _ := rec["level"].(string)
		levels[method] = level
	}

	want := map[string]string{
		"resources/subscribe": "DEBUG",
		"resources/read":      "DEBUG",
		"resources/list":      "ERROR",
		"prompts/list":        "ERROR",
	}
	for method, wantLevel := range want {
		got, ok := levels[method]
		if !ok {
			t.Fatalf("no 'mcp error' record emitted for %s — expected level was %s; downgraded logs must still be emitted", method, wantLevel)
		}
		if got != wantLevel {
			t.Fatalf("'mcp error' level for %s = %s, want %s", method, got, wantLevel)
		}
	}
}
