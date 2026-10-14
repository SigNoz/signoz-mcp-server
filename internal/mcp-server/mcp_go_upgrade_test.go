package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	official "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKLoggerBoundsPersistentAttrs(t *testing.T) {
	var logs bytes.Buffer
	logger := newBufferedLogger(&logs, slog.LevelDebug)
	sdkLogger := slog.New(&sdkLogHandler{next: logger.Handler()}).With(
		slog.String("session_id", strings.Repeat("x", 8*1024)),
	)
	sdkLogger.Error("transport rejection")

	record, _ := logRecordByMessage(t, &logs, "transport rejection")
	value, ok := record["session_id"].(string)
	if !ok || len(value) > 4*1024 || !strings.HasSuffix(value, "...(truncated)") {
		t.Fatalf("persistent SDK attr = %T len=%d, want bounded string", record["session_id"], len(value))
	}
}

func TestInputMismatchServedWithNoticeThroughProductionPipeline(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := logpkg.New("error")
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, noopanalytics.New(), nil)
	s := m.newSDKServer()

	called := false
	h.AddTool(s, mcp.NewTool("notice_probe", mcp.WithString("value", mcp.Required())), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	response := callToolForTest(t, s, "notice_probe", json.RawMessage(`{"value":42}`))
	if !called {
		t.Fatal("input mismatch must be served best-effort, never rejected")
	}
	b, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"isError":true`)) {
		t.Fatalf("mismatched input must not produce an error result: %s", b)
	}
	if !bytes.Contains(b, []byte(`"ok"`)) {
		t.Fatalf("handler result must be preserved: %s", b)
	}
	// The appended notice tells self-correcting agents what to fix.
	for _, want := range []string{"input validation notice", `parameter \"value\"`, "best-effort", "re-call"} {
		if !bytes.Contains(bytes.ToLower(b), []byte(want)) {
			t.Fatalf("notice is not actionable (missing %q): %s", want, b)
		}
	}
}

func TestProductionOutputMismatchPassesOriginalThrough(t *testing.T) {
	cfg := &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute}
	logger := logpkg.New("error")
	h := tools.NewHandler(logger, cfg)
	m := NewMCPServer(logger, h, cfg, noopanalytics.New(), nil)
	s := m.newSDKServer()
	tool := mcp.NewTool("probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.AddTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"count": "wrong"}, `{"count":"wrong"}`), nil
	})
	response := callToolForTest(t, s, "probe", json.RawMessage(`{}`))
	b, _ := json.Marshal(response)
	if bytes.Contains(b, []byte(`"isError":true`)) || !bytes.Contains(b, []byte(`"count":"wrong"`)) {
		t.Fatalf("output mismatch must pass the original result through: %s", b)
	}
}

func callToolForTest(t *testing.T, server *official.Server, name string, arguments json.RawMessage) *official.CallToolResult {
	t.Helper()
	client, err := newIntegrationClient(t, server)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.client.CallTool(context.Background(), &official.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil
	}
	return result
}
