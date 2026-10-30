package docs

import (
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
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
			&mcp.TextContent{Text: message},
		},
		StructuredContent: structured,
	}
}

func OutOfScopeURLError(url string) *mcp.CallToolResult {
	message := "URL is outside https://signoz.io/docs/ scope: " + url
	if strings.HasPrefix(url, "signoz://") {
		message += ". signoz:// URIs are MCP resources: read them with resources/read, not signoz_fetch_doc. To find a docs page, call signoz_search_docs and pass one of its URLs here."
	}
	return ToolError(CodeOutOfScopeURL, message, nil)
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
