package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestGuardrail_DuplicateRegistrationsRejectedBeforeSDKOverwrite(t *testing.T) {
	resourceHandler := func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) { return nil, nil }
	templateHandler := func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) { return nil, nil }
	promptHandler := func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) { return nil, nil }
	toolHandler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	tests := []struct {
		name string
		kind registrationKind
		add  func(*Handler, *server.MCPServer)
	}{
		{
			name: "tool",
			kind: registrationTool,
			add: func(h *Handler, s *server.MCPServer) {
				h.AddTool(s, mcp.NewTool("duplicate_probe"), toolHandler)
			},
		},
		{
			name: "resource",
			kind: registrationResource,
			add: func(h *Handler, s *server.MCPServer) {
				h.addResource(s, mcp.NewResource("signoz://duplicate/probe", "Duplicate Probe"), resourceHandler)
			},
		},
		{
			name: "resource template",
			kind: registrationResourceTemplate,
			add: func(h *Handler, s *server.MCPServer) {
				h.addResourceTemplate(s, mcp.NewResourceTemplate("signoz://duplicate/{id}", "Duplicate Probe"), templateHandler)
			},
		},
		{
			name: "prompt",
			kind: registrationPrompt,
			add: func(h *Handler, s *server.MCPServer) {
				h.RegisterPrompt(s, mcp.NewPrompt("duplicate_probe"), promptHandler)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(logpkg.New("error"), &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute})
			s := server.NewMCPServer("registration-test", "0.0.0")
			tt.add(h, s)

			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatalf("second %s registration did not fail", tt.kind)
				}
				message := fmt.Sprint(recovered)
				if !strings.Contains(message, "duplicate MCP "+string(tt.kind)+" registration") {
					t.Fatalf("unexpected panic: %s", message)
				}
			}()
			tt.add(h, s)
		})
	}
}

func TestGuardrail_RegistrationStateScopedPerSDKServer(t *testing.T) {
	h := NewHandler(logpkg.New("error"), &config.Config{ClientCacheSize: 1, ClientCacheTTL: time.Minute})
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	h.AddTool(server.NewMCPServer("one", "0.0.0"), mcp.NewTool("same_name"), handler)
	h.AddTool(server.NewMCPServer("two", "0.0.0"), mcp.NewTool("same_name"), handler)
}

func TestGuardrail_ProductionRegistrationsUseCheckedHelpers(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate registration test")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	schemaCompatPath := filepath.Join(repoRoot, "internal", "handler", "tools", "schema_compat.go")
	directSDKMethods := map[string]struct{}{
		"AddTool":             {},
		"AddResource":         {},
		"AddResourceTemplate": {},
		"AddPrompt":           {},
	}

	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != repoRoot && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "registration.go" {
			return nil
		}

		fileSet := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, guardedMethod := directSDKMethods[selector.Sel.Name]; !guardedMethod {
				return true
			}
			// jsonschema.Compiler.AddResource is unrelated to MCP registration.
			if path == schemaCompatPath && selector.Sel.Name == "AddResource" {
				if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "compiler" {
					return true
				}
			}
			position := fileSet.Position(call.Pos())
			t.Errorf("direct MCP registration bypasses checked helpers at %s:%d", position.Filename, position.Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan production registrations: %v", err)
	}
}
