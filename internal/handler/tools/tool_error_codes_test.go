package tools

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
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

	t.Run("typed structured object", func(t *testing.T) {
		type structuredDetail struct {
			Detail string `json:"detail"`
			Count  int64  `json:"count"`
		}
		bare := mcp.NewToolResultError("boom")
		bare.StructuredContent = structuredDetail{Detail: "kept", Count: 42}
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return bare, nil
		}
		result, err := h.errorCodeDecorator("test_tool", next)(context.Background(), request)
		if err != nil {
			t.Fatalf("decorator returned Go error: %v", err)
		}
		structured := resultStructuredMap(t, result)
		if got := structured["detail"]; got != "kept" {
			t.Fatalf("fallback detail = %#v, want kept", got)
		}
		if got := structured["count"]; got != json.Number("42") {
			t.Fatalf("fallback count = %#v, want json.Number(42)", got)
		}
		if got := structured["code"]; got != CodeInternalError {
			t.Fatalf("fallback code = %#v, want %s", got, CodeInternalError)
		}
	})

	t.Run("typed string map", func(t *testing.T) {
		bare := mcp.NewToolResultError("boom")
		bare.StructuredContent = map[string]string{"detail": "kept"}
		next := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return bare, nil
		}
		result, err := h.errorCodeDecorator("test_tool", next)(context.Background(), request)
		if err != nil {
			t.Fatalf("decorator returned Go error: %v", err)
		}
		structured := resultStructuredMap(t, result)
		if got := structured["detail"]; got != "kept" {
			t.Fatalf("fallback detail = %#v, want kept", got)
		}
		if got := structured["code"]; got != CodeInternalError {
			t.Fatalf("fallback code = %#v, want %s", got, CodeInternalError)
		}
	})
}

func TestProductionToolErrorsUseCodedHelpers(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob tool sources: %v", err)
	}
	fileset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		mcpAlias := ""
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, `"`) != "github.com/mark3labs/mcp-go/mcp" {
				continue
			}
			mcpAlias = "mcp"
			if imported.Name != nil {
				mcpAlias = imported.Name.Name
			}
			break
		}
		if mcpAlias == "" || mcpAlias == "." || mcpAlias == "_" {
			continue
		}

		isDirectConstructor := func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return false
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "NewToolResultError" {
				return false
			}
			pkg, ok := selector.X.(*ast.Ident)
			return ok && pkg.Name == mcpAlias
		}

		allowed := map[token.Pos]bool{}
		if path == "errs.go" {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name != "errorWithStructuredContent" {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					if isDirectConstructor(node) {
						allowed[node.Pos()] = true
					}
					return true
				})
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if !isDirectConstructor(node) || allowed[node.Pos()] {
				return true
			}
			position := fileset.Position(node.Pos())
			t.Errorf("%s calls mcp.NewToolResultError directly; use a coded helper", position)
			return true
		})
	}
}
