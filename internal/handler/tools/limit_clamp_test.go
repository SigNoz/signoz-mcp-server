package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestParseSearchLogsArgs_LimitClamped(t *testing.T) {
	over, err := parseSearchLogsArgs(map[string]any{"limit": "50000", "timeRange": "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if over.Limit != MaxRawResultLimit || !over.LimitClamped {
		t.Fatalf("over-cap: Limit=%d Clamped=%v, want %d true", over.Limit, over.LimitClamped, MaxRawResultLimit)
	}

	under, err := parseSearchLogsArgs(map[string]any{"limit": "500", "timeRange": "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if under.Limit != 500 || under.LimitClamped {
		t.Fatalf("under-cap: Limit=%d Clamped=%v, want 500 false", under.Limit, under.LimitClamped)
	}
}

func TestParseSearchTracesArgs_LimitClamped(t *testing.T) {
	over, err := parseSearchTracesArgs(map[string]any{"limit": "50000", "timeRange": "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if over.Limit != MaxRawResultLimit || !over.LimitClamped {
		t.Fatalf("over-cap: Limit=%d Clamped=%v, want %d true", over.Limit, over.LimitClamped, MaxRawResultLimit)
	}

	under, err := parseSearchTracesArgs(map[string]any{"limit": "500", "timeRange": "1h"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if under.Limit != 500 || under.LimitClamped {
		t.Fatalf("under-cap: Limit=%d Clamped=%v, want 500 false", under.Limit, under.LimitClamped)
	}
}

// TestRawSearchResult_NoteIsSeparateBlock guards the contract that the raw JSON
// payload is always the first, independently-parseable content block, and the
// clamp note (when present) is a separate trailing block — never prepended into
// the JSON.
func TestRawSearchResult_NoteIsSeparateBlock(t *testing.T) {
	payload := []byte(`{"status":"success","data":[]}`)

	notClamped := rawSearchResult(payload, false)
	if len(notClamped.Content) != 1 {
		t.Fatalf("not-clamped: want 1 content block, got %d", len(notClamped.Content))
	}

	clamped := rawSearchResult(payload, true)
	if len(clamped.Content) != 2 {
		t.Fatalf("clamped: want 2 content blocks, got %d", len(clamped.Content))
	}
	block0, ok := clamped.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("clamped: block 0 is %T, want mcp.TextContent", clamped.Content[0])
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(block0.Text), &parsed); err != nil {
		t.Fatalf("clamped: block 0 must be valid JSON, got %q (err: %v)", block0.Text, err)
	}
	block1, ok := clamped.Content[1].(mcp.TextContent)
	if !ok || !strings.Contains(block1.Text, "result limited to") {
		t.Fatalf("clamped: block 1 should be the pagination note, got %#v", clamped.Content[1])
	}
}

// TestClampedResultWrappers_SurfaceSeparateNote guards that aggregateResult and
// builderQueryResult follow the same contract — parseable JSON as block 0, a
// mode-appropriate note as a separate block 1 only when clamped.
func TestClampedResultWrappers_SurfaceSeparateNote(t *testing.T) {
	payload := []byte(`{"status":"success","data":[]}`)

	cases := []struct {
		name    string
		res     *mcp.CallToolResult
		wantSub string
	}{
		{"aggregate", aggregateResult(payload, true), "groups"},
		{"builder", builderQueryResult(payload, true), "limit"},
	}
	for _, tc := range cases {
		if len(tc.res.Content) != 2 {
			t.Fatalf("%s: want 2 content blocks, got %d", tc.name, len(tc.res.Content))
		}
		block0, ok := tc.res.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("%s: block 0 is %T, want mcp.TextContent", tc.name, tc.res.Content[0])
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(block0.Text), &parsed); err != nil {
			t.Fatalf("%s: block 0 must be valid JSON, got %q (err: %v)", tc.name, block0.Text, err)
		}
		block1, ok := tc.res.Content[1].(mcp.TextContent)
		if !ok || !strings.Contains(block1.Text, tc.wantSub) {
			t.Fatalf("%s: block 1 note %#v should contain %q", tc.name, tc.res.Content[1], tc.wantSub)
		}
	}

	// Not clamped -> single block, no note.
	if n := len(aggregateResult(payload, false).Content); n != 1 {
		t.Fatalf("not-clamped aggregate: want 1 content block, got %d", n)
	}
	if n := len(builderQueryResult(payload, false).Content); n != 1 {
		t.Fatalf("not-clamped builder: want 1 content block, got %d", n)
	}
}
