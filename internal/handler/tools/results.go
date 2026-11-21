package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// resultNotesKey is the StructuredContent field that carries a structured
// result's notes beside its payload fields.
const resultNotesKey = "notes"

// resultNotesDescription documents resultNotesKey on every object output schema.
const resultNotesDescription = "Advisory notes about this result, such as a clamped limit or an argument the tool ignored. Omitted when there are none."

// resultWithNotes wraps a raw JSON payload as a tool result. The JSON is always
// the first (parseable) content block; notes are appended as separate blocks
// rather than prepended into the JSON.
func resultWithNotes(payload []byte, notes ...string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(string(payload))
	// A text-only result has no StructuredContent, so appendNotes never logs.
	appendNotes(context.Background(), nil, res, notes...)
	return res
}

// structuredResultWithNotes is structuredResult plus notes. For code-controlled
// tools that must surface a human-readable warning (e.g. a post-create
// test-send failure) without dropping the stable StructuredContent shape.
func structuredResultWithNotes(ctx context.Context, logger *slog.Logger, payload []byte, notes ...string) *mcp.CallToolResult {
	res := structuredResult(payload)
	appendNotes(ctx, logger, res, notes...)
	return res
}

// appendNotes is the only place a tool result gains blocks after its first.
// Each non-blank note becomes a trailing text block. A successful structured
// result also lists the notes in its StructuredContent "notes" array, with
// block 0 re-serialized to match, because some clients (Claude Code) show the
// model only StructuredContent and drop every text block.
func appendNotes(ctx context.Context, logger *slog.Logger, res *mcp.CallToolResult, notes ...string) {
	kept := make([]string, 0, len(notes))
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		kept = append(kept, note)
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	if len(kept) == 0 || res.IsError || res.StructuredContent == nil {
		return
	}
	if err := addStructuredNotes(res, kept); err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		// Fail open: the notes still reach clients that read the text blocks.
		logger.WarnContext(ctx, "Structured result keeps its notes only as text blocks", logpkg.ErrAttr(err))
	}
}

// addStructuredNotes adds notes to the result's StructuredContent object and
// rewrites block 0 as that object's JSON. Numbers stay json.Number so large
// integers keep their exact digits in both representations.
func addStructuredNotes(res *mcp.CallToolResult, notes []string) error {
	block0, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		return errors.New("block 0 of a structured result is not text")
	}
	// Only a []string notes value was added here; a payload's own "notes" field
	// decodes as []any or another type and is never overwritten.
	prior, _ := res.StructuredContent.(map[string]any)
	priorNotes, ours := prior[resultNotesKey].([]string)

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return fmt.Errorf("encode structured content: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var object map[string]any
	if err := dec.Decode(&object); err != nil || object == nil {
		return errors.New("structured content is not a JSON object")
	}
	if _, taken := object[resultNotesKey]; taken && !ours {
		return fmt.Errorf("structured content already has its own %q field", resultNotesKey)
	}
	object[resultNotesKey] = append(append([]string(nil), priorNotes...), notes...)

	var text bytes.Buffer
	enc := json.NewEncoder(&text)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(object); err != nil {
		return fmt.Errorf("encode structured content with notes: %w", err)
	}
	replaced := *block0
	replaced.Text = strings.TrimSuffix(text.String(), "\n")
	res.Content[0] = &replaced
	res.StructuredContent = object
	return nil
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
	return resultWithNotes(payload, notes...)
}
