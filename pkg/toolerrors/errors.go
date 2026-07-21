// Package toolerrors defines the stable error-code taxonomy shared by MCP
// tool result producers and telemetry consumers.
package toolerrors

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	CodeValidationFailed   = "VALIDATION_FAILED"
	CodeUpstreamError      = "UPSTREAM_ERROR"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodePermissionDenied   = "PERMISSION_DENIED"
	CodeNotFound           = "NOT_FOUND"
	CodeConflict           = "CONFLICT"
	CodeRateLimited        = "RATE_LIMITED"
	CodeUnsupported        = "UNSUPPORTED"
	CodeLicenseUnavailable = "LICENSE_UNAVAILABLE"
	CodeCanceled           = "CANCELED"
	CodeTimeout            = "TIMEOUT"
	CodeInternalError      = "INTERNAL_ERROR"

	CodeOutOfScopeURL  = "OUT_OF_SCOPE_URL"
	CodeDocNotFound    = "DOC_NOT_FOUND"
	CodeHeadingMissing = "HEADING_NOT_FOUND"
	CodeIndexNotReady  = "INDEX_NOT_READY"
)

var knownCodes = map[string]struct{}{
	CodeValidationFailed:   {},
	CodeUpstreamError:      {},
	CodeUnauthorized:       {},
	CodePermissionDenied:   {},
	CodeNotFound:           {},
	CodeConflict:           {},
	CodeRateLimited:        {},
	CodeUnsupported:        {},
	CodeLicenseUnavailable: {},
	CodeCanceled:           {},
	CodeTimeout:            {},
	CodeInternalError:      {},
	CodeOutOfScopeURL:      {},
	CodeDocNotFound:        {},
	CodeHeadingMissing:     {},
	CodeIndexNotReady:      {},
}

// Code extracts a known structured code from an MCP tool error result.
func Code(result *mcp.CallToolResult) string {
	if result == nil || !result.IsError {
		return ""
	}
	code := structuredCode(result.StructuredContent)
	if _, ok := knownCodes[code]; !ok {
		return ""
	}
	return code
}

func structuredCode(content any) string {
	switch value := content.(type) {
	case map[string]any:
		code, _ := value["code"].(string)
		return code
	case map[string]string:
		return value["code"]
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return ""
	}
	return envelope.Code
}
