package tools

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
)

func TestToolHandlers_MissingCredentialsAreCoded(t *testing.T) {
	h := &Handler{logger: logpkg.New("error")}
	result, err := h.handleListMetrics(context.Background(), makeToolRequest("signoz_list_metrics", map[string]any{}))
	if err != nil {
		t.Fatalf("handleListMetrics returned Go error: %v", err)
	}
	if got := resultCode(t, result); got != CodeUnauthorized {
		t.Fatalf("missing-credentials code = %q, want %q", got, CodeUnauthorized)
	}
	if got := resultText(t, result); !strings.Contains(got, "missing tenant credentials") {
		t.Fatalf("missing-credentials text = %q", got)
	}
}

// newToolServerWithoutSigNoz registers every tool against an upstream that fails
// the test on any request, for rejections that must happen before SigNoz.
func newToolServerWithoutSigNoz(t *testing.T) *mcp.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid arguments reached SigNoz: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	return server
}

func callRegisteredTool(t *testing.T, server *mcp.Server, tool, arguments string) *mcp.CallToolResult {
	t.Helper()
	message := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
	return callTestToolFromJSONRPC(context.Background(), t, server, json.RawMessage(message))
}

func TestToolHandlers_RejectInvalidArgumentsBeforeCallingSigNoz(t *testing.T) {
	server := newToolServerWithoutSigNoz(t)
	tests := []struct {
		tool      string
		arguments string
		wantText  string
	}{
		{tool: "signoz_get_alert", arguments: `{}`, wantText: `"id" is required`},
		// A wrong-typed legacy alias counts as absent, so the canonical id error wins.
		{tool: "signoz_get_alert", arguments: `{"ruleId":12345}`, wantText: `"id" is required`},
		{tool: "signoz_get_dashboard", arguments: `{}`, wantText: `"id" is required`},
		{tool: "signoz_get_dashboard", arguments: `{"uuid":true}`, wantText: `"uuid" is no longer accepted`},
		{tool: "signoz_get_view", arguments: `{}`, wantText: `"id" is required`},
		// The retired flat update body names the "view" wrapper that replaced it.
		{tool: "signoz_update_view", arguments: `{"id":"019947a7-f200-7000-8000-000000000001","source":"logs","spec":{"displayName":"x"}}`, wantText: `"source" is no longer accepted at the top level; pass the saved view under "view"`},
		{tool: "signoz_update_view", arguments: `{"id":"019947a7-f200-7000-8000-000000000001"}`, wantText: `"view" is required`},
		{tool: "signoz_get_notification_channel", arguments: `{}`, wantText: `"id" is required`},
		{tool: "signoz_execute_builder_query", arguments: `{}`, wantText: `"query" must be a JSON object`},
		{tool: "signoz_get_trace_details", arguments: `{"traceId":"deadbeefdeadbeefdeadbeefdeadbeef","includeSpans":"perhaps"}`, wantText: "includeSpans"},
		{tool: "signoz_aggregate_logs", arguments: `{"aggregation":"count"}`, wantText: `"aggregations"`},
		{tool: "signoz_aggregate_logs", arguments: `{"aggregations":"p99(x)","aggregateOn":"x"}`, wantText: `"aggregations"`},
		{tool: "signoz_aggregate_logs", arguments: `{"groupBy":"service.name"}`, wantText: `"aggregations" is required`},
		{tool: "signoz_aggregate_logs", arguments: `{"aggregations":["count()",7]}`, wantText: `"aggregations" items`},
		{tool: "signoz_aggregate_logs", arguments: `{"aggregations":"count()","functions":"cutOffMin"}`, wantText: `"functions"`},
		{tool: "signoz_aggregate_logs", arguments: `{"aggregations":"count()","functions":[{"args":[]}]}`, wantText: `"functions"`},
		{tool: "signoz_aggregate_logs", arguments: `{"aggregations":"count()","groupBy":[{"key":"service.name"}]}`, wantText: `"groupBy"`},
		{tool: "signoz_query_metrics", arguments: `{"metricName":"x","formulaQueries":"{\"name\":\"B\"}"}`, wantText: `"formulaQueries"`},
		{tool: "signoz_query_metrics", arguments: `{"metricName":"x","formulaQueries":[{"name":"B","metricName":"y","metricType":"sum","isMonotonic":"tru"}]}`, wantText: `"isMonotonic"`},
		{tool: "signoz_search_logs", arguments: `{"selectFields":["attribute.body"]}`, wantText: `"attribute.body"`},
		{tool: "signoz_query_metrics", arguments: `{"metricName":"m","groupBy":[{"key":"service.name"}]}`, wantText: `"groupBy"`},
		{tool: "signoz_aggregate_traces", arguments: `{"aggregations":"count()","groupBy":"[\"http.route\","}`, wantText: `"groupBy" value: not a valid JSON array`},
		{tool: "signoz_aggregate_traces", arguments: `{"aggregations":"count()","groupBy":"[\"http.route\", null]"}`, wantText: `"groupBy" item`},
		{tool: "signoz_query_metrics", arguments: `{"metricName":"x","formulaQueries":[{"name":"B","metricName":"y","groupBy":"[\"host.name\""}]}`, wantText: `formulaQueries[0]`},
		{tool: "signoz_search_logs", arguments: `{"filter":"{\"expression\":\"service.name = 'api'\"}"}`, wantText: `not {"expression": ...}`},
		{tool: "signoz_list_alert_rules", arguments: `{"states":["firing",3]}`, wantText: `"states" item 3: each item must be a state string`},
	}
	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.arguments, func(t *testing.T) {
			result := callRegisteredTool(t, server, tt.tool, tt.arguments)
			if got := resultCode(t, result); got != CodeValidationFailed {
				t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
			}
			text := resultText(t, result)
			if !strings.HasPrefix(text, "Parameter validation failed:") || !strings.Contains(text, tt.wantText) {
				t.Fatalf("text = %q, want the validation prefix and %q", text, tt.wantText)
			}
		})
	}
}

func TestErrorCodeDecorator_CodesBareErrorsAndPreservesExistingCodes(t *testing.T) {
	h := &Handler{logger: logpkg.New("error")}
	request := makeToolRequest("test_tool", map[string]any{})

	t.Run("bare error", func(t *testing.T) {
		bare := mcp.NewToolResultError("boom")
		bare.StructuredContent = map[string]any{"detail": "kept"}
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return bare, nil
		}
		result, err := h.errorCodeDecorator("test_tool", next)(context.Background(), request)
		if err != nil {
			t.Fatalf("decorator returned Go error: %v", err)
		}
		if got := resultCode(t, result); got != CodeInternalError {
			t.Fatalf("fallback code = %q, want %q", got, CodeInternalError)
		}
		if got := resultText(t, result); got != "boom" {
			t.Fatalf("fallback text = %q, want %q", got, "boom")
		}
		if got := resultStructuredMap(t, result)["detail"]; got != "kept" {
			t.Fatalf("fallback detail = %#v, want kept", got)
		}
	})

	t.Run("known code", func(t *testing.T) {
		coded := validationError("id", "is required")
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return coded, nil
		}
		result, err := h.errorCodeDecorator("test_tool", next)(context.Background(), request)
		if err != nil {
			t.Fatalf("decorator returned Go error: %v", err)
		}
		if result != coded {
			t.Fatal("decorator replaced an already-coded result")
		}
		if got := resultCode(t, result); got != CodeValidationFailed {
			t.Fatalf("preserved code = %q, want %q", got, CodeValidationFailed)
		}
	})

}

func TestErrorCodeDecorator_AddsOperationSpecificAuthorizationRecovery(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantCode   string
		wantText   string
	}{
		{name: "unauthorized", statusCode: 401, wantCode: CodeUnauthorized, wantText: "Re-authenticate with valid SigNoz credentials"},
		{name: "forbidden", statusCode: 403, wantCode: CodePermissionDenied, wantText: "Request access to this SigNoz operation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{logger: logpkg.New("error")}
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return upstreamError(&signozclient.HTTPStatusError{StatusCode: tt.statusCode, Body: "secret-canary"}), nil
			}
			result, err := h.errorCodeDecorator("signoz_test_operation", next)(context.Background(), makeToolRequest("signoz_test_operation", map[string]any{}))
			if err != nil {
				t.Fatalf("decorator returned Go error: %v", err)
			}
			if got := resultCode(t, result); got != tt.wantCode {
				t.Fatalf("code = %q, want %q", got, tt.wantCode)
			}
			text := resultText(t, result)
			if !strings.Contains(text, "`signoz_test_operation`") || !strings.Contains(text, tt.wantText) {
				t.Fatalf("text = %q, want operation-specific recovery", text)
			}
			if strings.Contains(text, "secret-canary") {
				t.Fatalf("unparseable body leaked into text: %q", text)
			}
		})
	}

	t.Run("renderer guidance survives the recovery", func(t *testing.T) {
		h := &Handler{logger: logpkg.New("error")}
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return upstreamError(&signozclient.HTTPStatusError{
				StatusCode: 401,
				Body:       `{"status":"error","error":{"type":"unauthorized","code":"unauthenticated","message":"session expired","errors":[],"suggestions":["sign in again"]}}`,
			}), nil
		}
		result, err := h.errorCodeDecorator("signoz_test_operation", next)(context.Background(), makeToolRequest("signoz_test_operation", map[string]any{}))
		if err != nil {
			t.Fatalf("decorator returned Go error: %v", err)
		}
		text := resultText(t, result)
		for _, want := range []string{"session expired", "sign in again", "`signoz_test_operation`", "Re-authenticate"} {
			if !strings.Contains(text, want) {
				t.Fatalf("composed authorization text omitted %q: %q", want, text)
			}
		}
		structured := resultStructuredMap(t, result)
		if got := structured["upstreamSuggestions"]; !reflect.DeepEqual(got, []string{"sign in again"}) {
			t.Fatalf("authorization recovery changed backend suggestions: %#v", got)
		}
		if _, ok := structured["upstreamAuth"]; !ok {
			t.Fatalf("authorization bridge missing after recovery: %#v", structured)
		}
	})

	t.Run("local unauthorized result is unchanged", func(t *testing.T) {
		h := &Handler{logger: logpkg.New("error")}
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return errorWithCode(CodeUnauthorized, "missing tenant credentials"), nil
		}
		result, err := h.errorCodeDecorator("signoz_test_operation", next)(context.Background(), makeToolRequest("signoz_test_operation", map[string]any{}))
		if err != nil {
			t.Fatalf("decorator returned Go error: %v", err)
		}
		if got := resultText(t, result); got != "missing tenant credentials" {
			t.Fatalf("text = %q, want local error unchanged", got)
		}
	})
}

func TestErrorCodeDecorator_TypedStructuredContent(t *testing.T) {
	type structuredError struct {
		Code   string `json:"code,omitempty"`
		Detail string `json:"detail"`
		Count  int64  `json:"count,omitempty"`
	}
	tests := []struct {
		name               string
		content            any
		wantCode           string
		wantCount          any
		preserveTypedShape bool
	}{
		{name: "uncoded struct", content: structuredError{Detail: "kept", Count: 42}, wantCode: CodeInternalError, wantCount: json.Number("42")},
		{name: "uncoded typed map", content: map[string]string{"detail": "kept"}, wantCode: CodeInternalError},
		{name: "coded struct", content: structuredError{Code: CodeValidationFailed, Detail: "kept"}, wantCode: CodeValidationFailed, preserveTypedShape: true},
		{name: "coded typed map", content: map[string]string{"code": CodeUnauthorized, "detail": "kept"}, wantCode: CodeUnauthorized, preserveTypedShape: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{logger: logpkg.New("error")}
			bare := mcp.NewToolResultError("boom")
			bare.StructuredContent = tt.content
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return bare, nil
			}
			result, err := h.errorCodeDecorator("test_tool", next)(context.Background(), makeToolRequest("test_tool", map[string]any{}))
			if err != nil {
				t.Fatalf("decorator returned Go error: %v", err)
			}
			if got := toolerrors.Code(result); got != tt.wantCode {
				t.Fatalf("code = %q, want %q", got, tt.wantCode)
			}
			structured, _ := toolerrors.NormalizeStructuredContent(result.StructuredContent)
			if structured == nil {
				t.Fatalf("structured content is not a JSON object: %#v", result.StructuredContent)
			}
			if got := structured["detail"]; got != "kept" {
				t.Fatalf("detail = %#v, want kept", got)
			}
			if tt.wantCount != nil && structured["count"] != tt.wantCount {
				t.Fatalf("count = %#v, want %#v", structured["count"], tt.wantCount)
			}
			if tt.preserveTypedShape && reflect.TypeOf(result.StructuredContent) != reflect.TypeOf(tt.content) {
				t.Fatalf("structured content type = %T, want %T", result.StructuredContent, tt.content)
			}
		})
	}
}

func TestGuardrail_ProductionToolErrorsUseCodedHelpers(t *testing.T) {
	t.Run("detects method-value bypasses", func(t *testing.T) {
		fileset := token.NewFileSet()
		file, err := parser.ParseFile(fileset, "method_value.go", `package tools

import mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

func bypass() {
	newError := mcp.NewToolResultError
	_ = newError
}
`, 0)
		if err != nil {
			t.Fatalf("parse method-value probe: %v", err)
		}
		bypasses, err := uncodedToolErrorConstructorUses(fileset, file, "method_value.go")
		if err != nil {
			t.Fatalf("scan method-value probe: %v", err)
		}
		if len(bypasses) != 1 {
			t.Fatalf("method-value bypasses = %d, want 1", len(bypasses))
		}
	})

	fileset := token.NewFileSet()
	for _, directory := range []string{".", filepath.Join("..", "..", "docs")} {
		paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
		if err != nil {
			t.Fatalf("glob production error sources in %s: %v", directory, err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fileset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			bypasses, err := uncodedToolErrorConstructorUses(fileset, file, path)
			if err != nil {
				t.Fatalf("scan %s: %v", path, err)
			}
			for _, position := range bypasses {
				t.Errorf("MCP bare-error constructor bypasses coded helpers at %s", position)
			}
		}
	}
}

func uncodedToolErrorConstructorUses(fileset *token.FileSet, file *ast.File, path string) ([]token.Position, error) {
	mcpAlias := ""
	var mcpImport token.Pos
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return nil, err
		}
		if importPath != "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract" {
			continue
		}
		mcpAlias = "mcp"
		mcpImport = imported.Pos()
		if imported.Name != nil {
			mcpAlias = imported.Name.Name
		}
		break
	}
	if mcpAlias == "." {
		return []token.Position{fileset.Position(mcpImport)}, nil
	}
	if mcpAlias == "" || mcpAlias == "_" {
		return nil, nil
	}

	bareConstructors := map[string]bool{
		"NewToolResultError":        true,
		"NewToolResultErrorFromErr": true,
		"NewToolResultErrorf":       true,
	}
	constructorName := func(node ast.Node) (string, bool) {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !bareConstructors[selector.Sel.Name] {
			return "", false
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != mcpAlias {
			return "", false
		}
		return selector.Sel.Name, true
	}

	allowed := map[token.Pos]bool{}
	if filepath.Base(path) == "errs.go" {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "errorWithStructuredContent" {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if name, ok := constructorName(call.Fun); ok && name == "NewToolResultError" {
					allowed[call.Fun.Pos()] = true
				}
				return true
			})
		}
	}

	var bypasses []token.Position
	ast.Inspect(file, func(node ast.Node) bool {
		if _, ok := constructorName(node); !ok || allowed[node.Pos()] {
			return true
		}
		bypasses = append(bypasses, fileset.Position(node.Pos()))
		return true
	})
	return bypasses, nil
}
