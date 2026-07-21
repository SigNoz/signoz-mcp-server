package tools

import (
	"context"
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
}

func TestProductionToolErrorsUseCodedHelpers(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob tool sources: %v", err)
	}
	fileset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || path == "errs.go" {
			continue
		}
		file, err := parser.ParseFile(fileset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "NewToolResultError" {
				return true
			}
			position := fileset.Position(selector.Pos())
			t.Errorf("%s calls mcp.NewToolResultError directly; use a coded helper", position)
			return true
		})
	}
}
