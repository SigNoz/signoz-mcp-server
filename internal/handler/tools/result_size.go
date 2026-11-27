package tools

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

// resultSizeDecorator bounds the text of a successful tool result to
// config.MaxResultChars. MCP clients reject or file away a result above their
// own limit (some save anything over 50,000 characters to a file), so
// an agent that asked for 100 traces would otherwise never see them.
//
// Tools that declare an output schema are left alone: their structured
// content has to stay complete to validate against that schema.
func (h *Handler) resultSizeDecorator(tool mcp.Tool, next mcp.ToolHandlerFunc) mcp.ToolHandlerFunc {
	if tool.OutputSchema != nil {
		return next
	}
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := next(ctx, req)
		if err != nil || result == nil || result.IsError || h.config == nil {
			return result, err
		}
		bounded, originalChars, truncated := truncateToolResult(result, h.config.MaxResultChars)
		if truncated && h.logger != nil {
			h.logger.WarnContext(ctx, "tool result truncated to the configured size cap",
				slog.String("gen_ai.tool.name", tool.Name),
				slog.Int("result.chars", originalChars),
				slog.Int("result.max_chars", h.config.MaxResultChars))
		}
		return bounded, nil
	}
}

// truncateToolResult returns result unchanged when its text blocks total at
// most maxChars characters. Otherwise it returns a copy whose first text block
// is cut to fit, with a note appended that says what was cut. The copy drops
// StructuredContent, which would repeat the full payload. originalChars is the
// text size before truncation.
func truncateToolResult(result *mcp.CallToolResult, maxChars int) (bounded *mcp.CallToolResult, originalChars int, truncated bool) {
	if maxChars <= 0 {
		return result, 0, false
	}
	total := 0
	firstIndex := -1
	for i, content := range result.Content {
		text, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}
		if firstIndex < 0 {
			firstIndex = i
		}
		total += utf8.RuneCountInString(text.Text)
	}
	if firstIndex < 0 || total <= maxChars {
		return result, total, false
	}

	first, _ := mcp.AsTextContent(result.Content[firstIndex])
	note := resultTruncatedNote(total, maxChars)
	others := total - utf8.RuneCountInString(first.Text)
	keep := max(maxChars-others-utf8.RuneCountInString(note), 0)

	cut := *first
	cut.Text = firstRunes(first.Text, keep)
	out := *result
	out.StructuredContent = nil
	out.Content = append([]mcp.Content(nil), result.Content...)
	out.Content[firstIndex] = &cut
	out.Content = append(out.Content, mcp.NewTextContent(note))
	return &out, total, true
}

func resultTruncatedNote(total, maxChars int) string {
	return fmt.Sprintf(
		"note: result truncated at %d of %d characters (%s); the output above is cut off and incomplete. "+
			"Call again with a smaller \"limit\" or a narrower time range, filter or field selection.",
		maxChars, total, config.MaxResultCharsEnv)
}

func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
