package tools

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

func textBlocks(result *mcp.CallToolResult) []string {
	var out []string
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			out = append(out, text.Text)
		}
	}
	return out
}

func TestTruncateToolResultKeepsWholeCharactersAndLaterNotes(t *testing.T) {
	original := mcp.NewToolResultStructured(map[string]any{"k": "v"}, strings.Repeat("é", 400))
	original.Content = append(original.Content, mcp.NewTextContent("note: more rows may follow."))

	bounded, originalChars, truncated := truncateToolResult(original, 300)
	if !truncated {
		t.Fatal("a 427-character result was not truncated at 300")
	}
	if want := 400 + len("note: more rows may follow."); originalChars != want {
		t.Fatalf("originalChars = %d, want %d", originalChars, want)
	}
	blocks := textBlocks(bounded)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want payload, the earlier note and the truncation note", len(blocks))
	}
	total := 0
	for _, block := range blocks {
		if !utf8.ValidString(block) {
			t.Fatalf("block is not valid UTF-8 after the cut: %q", block)
		}
		total += utf8.RuneCountInString(block)
	}
	if total > 300 {
		t.Fatalf("truncated text is %d characters, want at most 300", total)
	}
	if blocks[1] != "note: more rows may follow." {
		t.Fatalf("an existing note was changed: %q", blocks[1])
	}
	if bounded.StructuredContent != nil {
		t.Fatal("structured content must be dropped with the cut payload")
	}
	if original.StructuredContent == nil || utf8.RuneCountInString(textBlocks(original)[0]) != 400 {
		t.Fatal("truncation mutated the handler's own result")
	}
}

func TestTruncateToolResultLeavesSmallAndDisabledResultsAlone(t *testing.T) {
	small := mcp.NewToolResultText(strings.Repeat("x", 300))
	if got, _, truncated := truncateToolResult(small, 300); truncated || got != small {
		t.Fatal("a result exactly at the cap must pass through untouched")
	}
	large := mcp.NewToolResultText(strings.Repeat("x", 301))
	if got, _, truncated := truncateToolResult(large, 0); truncated || got != large {
		t.Fatal("a non-positive cap disables truncation")
	}
}

func TestResultSizeDecoratorSkipsErrorsAndSchemaBoundTools(t *testing.T) {
	h := &Handler{config: &config.Config{MaxResultChars: 100}}
	big := strings.Repeat("x", 500)
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(big), nil
	}

	plain := h.resultSizeDecorator(mcp.NewTool("plain"), handler)
	result, err := plain(context.Background(), mcp.CallToolRequest{})
	if err != nil || utf8.RuneCountInString(textBlocks(result)[0]) >= 500 {
		t.Fatalf("a tool without an output schema was not bounded (err=%v)", err)
	}

	bound := mcp.NewTool("bound")
	bound.OutputSchema = map[string]any{"type": "object"}
	result, err = h.resultSizeDecorator(bound, handler)(context.Background(), mcp.CallToolRequest{})
	if err != nil || textBlocks(result)[0] != big {
		t.Fatalf("a tool with an output schema must keep its full result (err=%v)", err)
	}

	failing := h.resultSizeDecorator(mcp.NewTool("failing"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError(big), nil
	})
	result, err = failing(context.Background(), mcp.CallToolRequest{})
	if err != nil || textBlocks(result)[0] != big {
		t.Fatalf("an error result must not be cut (err=%v)", err)
	}
}
