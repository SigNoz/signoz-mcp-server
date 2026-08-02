package tools

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
	"github.com/mark3labs/mcp-go/mcp"
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

func TestErrorCodeDecorator_CodesBareErrorsAndPreservesExistingCodes(t *testing.T) {
	h := &Handler{logger: logpkg.New("error")}
	request := makeToolRequest("test_tool", map[string]any{})

	t.Run("bare error", func(t *testing.T) {
		bare := mcp.NewToolResultError("boom")
		bare.StructuredContent = map[string]any{"detail": "kept"}
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return bare, nil
		}
		result, err := h.errorCodeDecorator("test_tool", nil, next)(context.Background(), request)
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
		result, err := h.errorCodeDecorator("test_tool", nil, next)(context.Background(), request)
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

func TestErrorCodeDecorator_AddsOperationAwareAuthorizationRecovery(t *testing.T) {
	readOnly := true
	write := false
	tests := []struct {
		name         string
		status       int
		code         string
		readOnlyHint *bool
		want         string
	}{
		{
			name:         "unauthorized",
			status:       http.StatusUnauthorized,
			code:         CodeUnauthorized,
			readOnlyHint: &readOnly,
			want:         "Affected operation: `signoz_probe` failed authentication.",
		},
		{
			name:         "forbidden read",
			status:       http.StatusForbidden,
			code:         CodePermissionDenied,
			readOnlyHint: &readOnly,
			want:         "lack access to this read operation (`signoz_probe`)",
		},
		{
			name:         "forbidden write",
			status:       http.StatusForbidden,
			code:         CodePermissionDenied,
			readOnlyHint: &write,
			want:         "lack permission for this write operation (`signoz_probe`)",
		},
		{
			name:   "forbidden unannotated",
			status: http.StatusForbidden,
			code:   CodePermissionDenied,
			want:   "lack permission for this operation (`signoz_probe`)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coded := upstreamError(&signozclient.HTTPStatusError{
				StatusCode: tc.status,
				Body:       "unsafe-auth-body-canary",
			})
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return coded, nil
			}
			h := &Handler{logger: logpkg.New("error")}
			result, err := h.errorCodeDecorator("signoz_probe", tc.readOnlyHint, next)(context.Background(), makeToolRequest("signoz_probe", map[string]any{}))
			if err != nil {
				t.Fatalf("decorator returned Go error: %v", err)
			}
			if got := resultCode(t, result); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
			text := resultText(t, result)
			if !strings.Contains(text, tc.want) {
				t.Fatalf("text = %q, want operation recovery %q", text, tc.want)
			}
			if strings.Contains(text, "unsafe-auth-body-canary") {
				t.Fatalf("text leaked upstream authorization body: %q", text)
			}
			for _, unsupportedInference := range []string{"viewer-level", "editor access", "admin access"} {
				if strings.Contains(strings.ToLower(text), unsupportedInference) {
					t.Fatalf("text inferred an unobserved role: %q", text)
				}
			}
		})
	}

	t.Run("local unauthorized without upstream status is unchanged", func(t *testing.T) {
		coded := clientError(errors.New("missing credentials"))
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return coded, nil
		}
		h := &Handler{logger: logpkg.New("error")}
		result, err := h.errorCodeDecorator("signoz_probe", &readOnly, next)(context.Background(), makeToolRequest("signoz_probe", map[string]any{}))
		if err != nil {
			t.Fatal(err)
		}
		if got := resultText(t, result); got != "missing credentials" {
			t.Fatalf("local unauthorized text = %q, want unchanged", got)
		}
	})

	t.Run("non-authorization upstream status is unchanged", func(t *testing.T) {
		coded := upstreamError(&signozclient.HTTPStatusError{StatusCode: http.StatusInternalServerError, Body: `{"status":"error","message":"proxy canary"}`})
		before := resultText(t, coded)
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return coded, nil
		}
		h := &Handler{logger: logpkg.New("error")}
		result, err := h.errorCodeDecorator("signoz_probe", &readOnly, next)(context.Background(), makeToolRequest("signoz_probe", map[string]any{}))
		if err != nil {
			t.Fatal(err)
		}
		if got := resultText(t, result); got != before || strings.Contains(got, "Affected operation:") {
			t.Fatalf("non-auth error recovery changed: before=%q after=%q", before, got)
		}
	})
}

func TestErrorCodeDecorator_QualifiesRendererRetryByOperationSemantics(t *testing.T) {
	readOnly := true
	write := false
	tests := []struct {
		name         string
		readOnlyHint *bool
		wantSafe     bool
		wantCaution  bool
	}{
		{name: "read", readOnlyHint: &readOnly, wantSafe: true},
		{name: "write", readOnlyHint: &write, wantCaution: true},
		{name: "unannotated", wantCaution: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coded := upstreamError(&signozclient.HTTPStatusError{
				StatusCode: http.StatusServiceUnavailable,
				Body:       `{"status":"error","error":{"type":"unavailable","code":"temporarily_unavailable","message":"try later","retry":{"delay":"2s"}}}`,
			})
			next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return coded, nil
			}
			h := &Handler{logger: logpkg.New("error")}
			result, err := h.errorCodeDecorator("signoz_retry_probe", tc.readOnlyHint, next)(context.Background(), makeToolRequest("signoz_retry_probe", map[string]any{}))
			if err != nil {
				t.Fatal(err)
			}
			structured := resultStructuredMap(t, result)
			if got, ok := structured["retrySafe"].(bool); !ok || got != tc.wantSafe {
				t.Fatalf("retrySafe = %#v, want %t", structured["retrySafe"], tc.wantSafe)
			}
			text := resultText(t, result)
			caution := "may already have taken effect. Verify current state before retrying it."
			if strings.Contains(text, caution) != tc.wantCaution {
				t.Fatalf("caution mismatch: %q", text)
			}
		})
	}
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
			result, err := h.errorCodeDecorator("test_tool", nil, next)(context.Background(), makeToolRequest("test_tool", map[string]any{}))
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

import "github.com/mark3labs/mcp-go/mcp"

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
		if importPath != "github.com/mark3labs/mcp-go/mcp" {
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
