package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

func TestInputMismatchServedBestEffortWithNoticeAndNoLeakedValues(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	h := &Handler{logger: logger}
	s := newMCPTestServer()
	tool := mcp.NewTool("shadow_probe")
	tool.InputSchema = map[string]any{
		"type":       "object",
		"properties": map[string]any{"webhook_password": map[string]any{"type": "number"}},
	}
	called := false
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})

	ctx := context.Background()
	response := callTestToolFromJSONRPC(ctx, t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shadow_probe","arguments":{"searchContext":"create a webhook","webhook_password":"super-secret-value"}}}`))
	if !called {
		t.Fatal("input mismatch must not block the handler call")
	}
	callTestToolFromJSONRPC(ctx, t, s, json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shadow_probe","arguments":{"searchContext":"second attempt","webhook_password":"another-secret-value"}}}`))
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	const wantNotice = `Input validation notice: parameter "webhook_password" did not fully match its advertised schema. The call still ran best-effort: mismatched values may have been ignored or replaced with defaults. Adjust the flagged parameter(s) and re-call if the results look off.`
	encodedNotice, err := json.Marshal(wantNotice)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, encodedNotice[1:len(encodedNotice)-1]) {
		t.Fatalf("successful result missing exact in-band validation notice: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"ok"`) {
		t.Fatalf("original handler content must be preserved alongside the notice: %s", encoded)
	}
	if strings.Contains(string(encoded), "super-secret-value") {
		t.Fatalf("validation notice leaked raw argument values: %s", encoded)
	}
	if strings.Contains(logs.String(), "super-secret-value") || strings.Contains(logs.String(), "another-secret-value") {
		t.Fatalf("validation telemetry leaked raw argument values: %s", logs.String())
	}
}

func TestOutputMismatchPassesOriginalResultThrough(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	h := &Handler{logger: logger}
	s := newMCPTestServer()
	tool := mcp.NewTool("output_probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"count": "wrong"}, `{"count":"wrong"}`), nil
	})

	response := callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"output_probe","arguments":{}}}`))
	b, _ := json.Marshal(response)
	if !strings.Contains(string(b), `"count":"wrong"`) || strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("output mismatch did not pass the original result through: %s", b)
	}
}

func TestOutputSchemaSuccessWithoutStructuredContentFailsOpen(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	h := &Handler{logger: logger}
	s := newMCPTestServer()
	tool := mcp.NewTool("nil_output_probe", mcp.WithOutputSchema[struct {
		Count int `json:"count"`
	}]())
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("text-only"), nil
	})

	response := callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nil_output_probe","arguments":{}}}`))
	b, _ := json.Marshal(response)
	if !strings.Contains(string(b), "text-only") || strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("nil StructuredContent should fail open with the original result: %s", b)
	}
}

func TestSchemaCompileFailureRegistersFailOpen(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	h := &Handler{logger: logger}
	s := newMCPTestServer()
	tool := mcp.Tool{
		Name:        "broken_schema_probe",
		Description: "probe",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"$ref":"missing.json"}}}`),
	}
	called := false
	h.addTool(s, tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})
	callTestToolFromJSONRPC(context.Background(), t, s, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"broken_schema_probe","arguments":{"secret":"do-not-log"}}}`))
	if !called {
		t.Fatal("compile failure prevented fail-open handler registration")
	}
	if strings.Contains(logs.String(), "do-not-log") {
		t.Fatalf("compile-failure telemetry leaked arguments: %s", logs.String())
	}
}
