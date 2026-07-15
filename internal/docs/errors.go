package docs

import (
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	CodeOutOfScopeURL  = toolerrors.CodeOutOfScopeURL
	CodeDocNotFound    = toolerrors.CodeDocNotFound
	CodeHeadingMissing = toolerrors.CodeHeadingMissing
	CodeIndexNotReady  = toolerrors.CodeIndexNotReady
)

func ToolError(code, message string, extra map[string]any) *mcp.CallToolResult {
	structured := map[string]any{"code": code}
	for k, v := range extra {
		structured[k] = v
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: message},
		},
		StructuredContent: structured,
	}
}

func OutOfScopeURLError(url string) *mcp.CallToolResult {
	return ToolError(CodeOutOfScopeURL, "URL is outside https://signoz.io/docs/ scope: "+url, nil)
}

func DocNotFoundError(url string) *mcp.CallToolResult {
	return ToolError(CodeDocNotFound, "Document is valid but not present in the docs index: "+url, nil)
}

func HeadingNotFoundError(heading string, headings []Heading) *mcp.CallToolResult {
	return ToolError(CodeHeadingMissing, "Heading not found in document: "+heading, map[string]any{
		"available_headings": headings,
	})
}

func IndexNotReadyError() *mcp.CallToolResult {
	return ToolError(CodeIndexNotReady, "Docs index is not ready yet; retry shortly.", map[string]any{
		"retry_after_seconds": 5,
	})
}
