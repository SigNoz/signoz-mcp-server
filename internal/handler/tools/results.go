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

// rawSearchRequest is what a raw row tool's result needs from its arguments.
type rawSearchRequest struct {
	limit        int
	offset       int
	limitClamped bool
	budget       resultBudget
	// emptyNote, when not blank, follows the notes of an empty first page.
	emptyNote string
	// ignoresSelectFields is true when selectFields does not apply to the rows.
	ignoresSelectFields bool
}

// rawSearchResult is the result of the raw row tools (signoz_search_logs,
// signoz_search_traces), which page with offset. Its rows fit the result size
// budget, and its JSON gains a top-level pagination object with hasMore and
// nextOffset. A completeness note repeats them so callers never mistake a
// partial page for the whole result. A payload whose rows it cannot walk is
// returned unchanged with the inferred note, and a WARN.
func (h *Handler) rawSearchResult(ctx context.Context, toolName string, payload []byte, req rawSearchRequest, extraNotes ...string) *mcp.CallToolResult {
	notes := append([]string(nil), extraNotes...)
	if req.limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d rows to bound server memory; paginate with \"offset\" (or narrow the time range/filters) for more.",
			MaxRawResultLimit))
	}
	notes = append(notes, req.budget.clampNote())
	warnings := extractBackendWarningMessages(payload)
	warnBackendWarnings(ctx, h.logger, toolName, warnings)
	warnUnparsedWarningEnvelope(ctx, h.logger, toolName, payload, len(warnings))

	rows, err := parseRowPage(payload)
	if err != nil {
		// Fail open: an unwalkable payload keeps today's shape and note.
		returnedRows, rowsKnown := countQueryRangeRows(payload)
		warnRowCountUnknown(ctx, h.logger, toolName, payload, rowsKnown)
		if rowsKnown {
			h.warnLogger().WarnContext(ctx, "raw search result left unbounded because its rows could not be paged",
				slog.String("gen_ai.tool.name", toolName),
				logpkg.ErrAttr(err))
		}
		// Counted even for an empty body, which the WARNs skip: its notes alone
		// can be over a small budget.
		h.countBudgetEvent(ctx, toolName, budgetEventFailOpen)
		notes = append(notes, completenessNote(returnedRows, req.limit, req.offset, rowsKnown))
		if rowsKnown && returnedRows == 0 && req.offset == 0 {
			notes = append(notes, req.emptyNote)
		}
		return resultWithNotes(payload, notes...)
	}

	page := &rawSearchPage{
		toolName:  toolName,
		limit:     req.limit,
		offset:    req.offset,
		budget:    req.budget,
		leadNotes: notes,
		emptyNote: req.emptyNote,
		rows:      rows,

		ignoresSelectFields: req.ignoresSelectFields,
	}
	return deliverFitted(ctx, page.fit, func(outcome pageOutcome) {
		h.recordBudget(ctx, toolName, page.budget.bytes, len(rows.rows), outcome)
	})
}
