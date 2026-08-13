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
	fail := func(method string, err error) {
		ctx := context.Background()
		handler := middleware(func(context.Context, string, mcp.Request) (mcp.Result, error) { return nil, err })
		_, _ = handler(ctx, method, nil)
	}

	fail("resources/subscribe", &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "unsupported"})
	fail("resources/read", fmt.Errorf("read: %w", context.Canceled))
	fail("resources/list", fmt.Errorf("list: %w", context.DeadlineExceeded))
	fail("prompts/list", errors.New("boom"))

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
