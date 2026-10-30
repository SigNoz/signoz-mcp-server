package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

// resultWithNotes wraps a raw JSON payload as a tool result. The JSON is always
// the first (parseable) content block; notes are appended as separate blocks
// rather than prepended into the JSON.
func resultWithNotes(payload []byte, notes ...string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(string(payload))
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res
}

// structuredResultWithNotes is structuredResult plus trailing advisory note
// blocks (as resultWithNotes appends them). For code-controlled tools that must
// surface a human-readable warning (e.g. a post-create test-send failure)
// without dropping the stable StructuredContent shape.
func structuredResultWithNotes(payload []byte, notes ...string) *mcp.CallToolResult {
	res := structuredResult(payload)
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res
}

// rawSearchResult is the result wrapper for raw row tools (search_logs /
// search_traces), which support offset pagination. It appends a completeness
// note (hasMore + nextOffset) inferred from the returned row count so callers
// never silently assume a truncated page is complete.
func rawSearchResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limit, offset int, limitClamped bool, extraNotes ...string) *mcp.CallToolResult {
	notes := append([]string(nil), extraNotes...)
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d rows to bound server memory; paginate with \"offset\" (or narrow the time range/filters) for more.",
			MaxRawResultLimit))
	}
	returnedRows, rowsKnown := countQueryRangeRows(payload)
	warnRowCountUnknown(ctx, logger, toolName, payload, rowsKnown)
	notes = append(notes, completenessNote(returnedRows, limit, offset, rowsKnown))
	warnings := extractBackendWarningMessages(payload)
	warnBackendWarnings(ctx, logger, toolName, warnings)
	warnUnparsedWarningEnvelope(ctx, logger, toolName, payload, len(warnings))
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(payload, notes...)
}
