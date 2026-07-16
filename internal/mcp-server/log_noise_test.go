package mcp_server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"

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

	ctx := newAnalyticsTestContext(context.Background(), "sess-init-cap")
	resp := s.HandleMessage(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"cap-test","version":"0.0.1"}}}`))

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal initialize response: %v", err)
	}

	var envelope struct {
		Result struct {
			Capabilities struct {
				Resources *struct {
					Subscribe bool `json:"subscribe"`
				} `json:"resources"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal initialize response %q: %v", raw, err)
	}
	if envelope.Result.Capabilities.Resources == nil {
		t.Fatalf("resources capability missing from initialize result %q; expected it advertised (without subscribe) once resources are registered", raw)
	}
	if envelope.Result.Capabilities.Resources.Subscribe {
		t.Fatalf("resources.subscribe advertised as true in %q; this server does not implement resources/subscribe", raw)
	}
	if strings.Contains(string(raw), `"subscribe":true`) {
		t.Fatalf("initialize result advertises subscribe support: %q", raw)
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
		{"resources subscribe not supported", fmt.Errorf("resources subscribe %w", mcpgoserver.ErrUnsupported), slog.LevelDebug},
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
func TestBuildHooks_ErrorLogSeverityClassification(t *testing.T) {
	var buf lockedBuffer
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	handler := tools.NewHandler(logpkg.New("error"), cfg)
	mcpServer := NewMCPServer(newBufferedLogger(&buf, slog.LevelDebug), handler, cfg, noopanalytics.New(), nil)
	hooks := mcpServer.buildHooks()

	fail := func(method mcp.MCPMethod, id string, err error) {
		ctx := newAnalyticsTestContext(context.Background(), "sess-"+id)
		req := &mcp.SubscribeRequest{}
		singleHook(t, hooks.OnBeforeAny, "OnBeforeAny")(ctx, id, method, req)
		singleHook(t, hooks.OnError, "OnError")(ctx, id, method, req, err)
	}

	fail(mcp.MethodResourcesSubscribe, "req-sub", fmt.Errorf("resources subscribe %w", mcpgoserver.ErrUnsupported))
	fail(mcp.MethodResourcesRead, "req-cancel", fmt.Errorf("read: %w", context.Canceled))
	fail(mcp.MethodResourcesList, "req-deadline", fmt.Errorf("list: %w", context.DeadlineExceeded))
	fail(mcp.MethodPromptsList, "req-generic", errors.New("boom"))

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
		string(mcp.MethodResourcesSubscribe): "DEBUG",
		string(mcp.MethodResourcesRead):      "DEBUG",
		string(mcp.MethodResourcesList):      "ERROR",
		string(mcp.MethodPromptsList):        "ERROR",
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
