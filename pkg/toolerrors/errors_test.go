package toolerrors

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestCode(t *testing.T) {
	type typedError struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	tests := []struct {
		name   string
		result *mcp.CallToolResult
		want   string
	}{
		{name: "nil"},
		{name: "success", result: &mcp.CallToolResult{StructuredContent: map[string]any{"code": CodeTimeout}}},
		{name: "unknown", result: &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": "CALLER_VALUE"}}},
		{name: "wrong shape", result: &mcp.CallToolResult{IsError: true, StructuredContent: []string{CodeTimeout}}},
		{name: "handler code", result: &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": CodePermissionDenied}}, want: CodePermissionDenied},
		{name: "internal code", result: &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": CodeInternalError}}, want: CodeInternalError},
		{name: "docs code", result: &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": CodeIndexNotReady}}, want: CodeIndexNotReady},
		{name: "typed map code", result: &mcp.CallToolResult{IsError: true, StructuredContent: map[string]string{"code": CodeUnauthorized}}, want: CodeUnauthorized},
		{name: "typed struct code", result: &mcp.CallToolResult{IsError: true, StructuredContent: typedError{Code: CodeValidationFailed, Detail: "kept"}}, want: CodeValidationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.result); got != tt.want {
				t.Fatalf("Code() = %q, want %q", got, tt.want)
			}
		})
	}
}
