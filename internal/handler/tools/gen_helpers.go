package tools

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// withRawSchema attaches a pre-built JSON Schema to an MCP tool and clears
// the default InputSchema.Type. mcp-go fails to marshal a Tool when both
// InputSchema and RawInputSchema are set, so we do the same dance that
// WithInputSchema[T] performs internally — just with the raw bytes we
// already have from the //go:embedded per-tag JSON files.
func withRawSchema(raw json.RawMessage) mcp.ToolOption {
	return func(t *mcp.Tool) {
		t.InputSchema.Type = ""
		t.RawInputSchema = raw
	}
}
