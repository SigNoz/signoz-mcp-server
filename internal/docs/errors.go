package docs

import "github.com/mark3labs/mcp-go/mcp"

const (
	CodeOutOfScopeURL  = "OUT_OF_SCOPE_URL"
	CodeDocNotFound    = "DOC_NOT_FOUND"
	CodeHeadingMissing = "HEADING_NOT_FOUND"
	CodeIndexNotReady  = "INDEX_NOT_READY"
	CodeRateLimited    = "RATE_LIMITED"
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

func RateLimitedError(retryAfterSeconds int) *mcp.CallToolResult {
	return ToolError(CodeRateLimited, "Public docs rate limit exceeded; retry later.", map[string]any{
		"retry_after_seconds": retryAfterSeconds,
	})
}
