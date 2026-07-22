// Package toolerrors defines the stable error-code taxonomy shared by MCP
// tool result producers and telemetry consumers.
package toolerrors

import (
	"bytes"
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
	_, code := NormalizeStructuredContent(result.StructuredContent)
	return code
}

// NormalizeStructuredContent returns a JSON-object representation and its
// known error code, if present. Integer precision is preserved.
func NormalizeStructuredContent(content any) (map[string]any, string) {
	if content == nil {
		return nil, ""
	}
	object, ok := content.(map[string]any)
	if !ok {
		encoded, err := json.Marshal(content)
		if err != nil {
			return nil, ""
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil || object == nil {
			return nil, ""
		}
	}
	code, _ := object["code"].(string)
	if _, known := knownCodes[code]; !known {
		code = ""
	}
	return object, code
}
