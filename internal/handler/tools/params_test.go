package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// TestParseLimit_NumberAndString pins that the shared docs-limit parser accepts
// both a JSON number and a string (so flipping the search_docs schema from
// WithNumber→WithString never silently drops a numeric caller's value).
func TestParseLimit_NumberAndString(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
	}{
		{"string", "15", 15},
		{"float64 (JSON number)", float64(15), 15},
		{"int", 15, 15},
		{"json.Number", json.Number("15"), 15},
		{"empty string falls back", "", 10},
		{"nil falls back", nil, 10},
		{"unparseable falls back", "abc", 10},
		{"zero falls back", "0", 10},
		{"negative falls back", "-5", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLimit(tt.in, 10); got != tt.want {
				t.Fatalf("parseLimit(%#v, 10) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestIntArg_NumberOrString pins the shared loose int parser used by limit/offset.
func TestIntArg_NumberOrString(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		def     int
		want    int
		wantErr bool
	}{
		{"string", map[string]any{"limit": "25"}, 10, 25, false},
		{"number", map[string]any{"limit": float64(25)}, 10, 25, false},
		{"missing -> default", map[string]any{}, 10, 10, false},
		{"empty -> default", map[string]any{"limit": ""}, 10, 10, false},
		{"non-positive -> default", map[string]any{"limit": "0"}, 10, 10, false},
		{"unparseable -> error", map[string]any{"limit": "xyz"}, 10, 0, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := intArg(tt.args, "limit", tt.def)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("intArg = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestValidateRequestType pins that unknown requestType values are rejected and
// the two valid values (plus empty, meaning "use default") pass.
func TestValidateRequestType(t *testing.T) {
	for _, v := range []string{"", "scalar", "time_series"} {
		if err := validateRequestType(v); err != nil {
			t.Fatalf("validateRequestType(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"raw", "Scalar", "timeseries", "table"} {
		if err := validateRequestType(v); err == nil {
			t.Fatalf("validateRequestType(%q) = nil, want error", v)
		}
	}
}

// A client may show the model only StructuredContent, so the clamp note on a
// list result travels in its "notes" field as well as in a trailing text block.
func TestListToolCarriesTheClampNoteInStructuredContent(t *testing.T) {
	const clampNote = `note: limit clamped to 1000 per page to bound server memory; use "offset" to page through more results.`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[{"serviceName":"frontend"}]}`))
	}))
	t.Cleanup(upstream.Close)
	h := newTestHandler(signozclient.NewClient(logpkg.New("error"), upstream.URL, "k", "SIGNOZ-API-KEY", nil))
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)

	clamped := callRegisteredTool(t, server, "signoz_list_services", `{"limit":5000}`)
	require.False(t, clamped.IsError, allTextBlocks(clamped))
	require.Equal(t, []string{clampNote}, structuredNotes(t, clamped), "the clamp note must reach clients that read only StructuredContent")
	require.Equal(t, []string{clampNote}, trailingTextBlocks(clamped), "clients that read content keep the note as its own block")
	require.True(t, sameJSONValue(t, []byte(textContent(t, clamped)), clamped.StructuredContent),
		"block 0 must carry the same JSON as StructuredContent, notes included: %s", textContent(t, clamped))

	unclamped := callRegisteredTool(t, server, "signoz_list_services", `{"limit":50}`)
	require.False(t, unclamped.IsError, allTextBlocks(unclamped))
	require.NotContains(t, unclamped.StructuredContent, "notes", "a result without notes has no notes field")
	require.Empty(t, trailingTextBlocks(unclamped))
}

// structuredNotes returns the "notes" field of a result's StructuredContent.
func structuredNotes(t *testing.T, result *mcp.CallToolResult) []string {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var structured struct {
		Notes []string `json:"notes"`
	}
	require.NoError(t, json.Unmarshal(raw, &structured), "StructuredContent: %s", raw)
	return structured.Notes
}

// trailingTextBlocks returns the text of every content block after block 0.
func trailingTextBlocks(result *mcp.CallToolResult) []string {
	var texts []string
	for _, block := range result.Content[1:] {
		if text, ok := mcp.AsTextContent(block); ok {
			texts = append(texts, text.Text)
		}
	}
	return texts
}
