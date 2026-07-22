package tools

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

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
