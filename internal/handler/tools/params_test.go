package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
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

// TestListResult_ClampNoteSeparateBlock pins that the clamp note is a separate
// trailing block and the JSON payload is content block 0.
func TestListResult_ClampNoteSeparateBlock(t *testing.T) {
	payload := []byte(`{"data":[],"pagination":{}}`)

	if n := len(listResult(payload, false).Content); n != 1 {
		t.Fatalf("not-clamped: want 1 content block, got %d", n)
	}

	clamped := listResult(payload, true)
	if len(clamped.Content) != 2 {
		t.Fatalf("clamped: want 2 content blocks, got %d", len(clamped.Content))
	}
	// Block 0 must remain parseable JSON.
	block0, ok := mcp.AsTextContent(clamped.Content[0])
	if !ok {
		t.Fatalf("clamped: block 0 is not text")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(block0.Text), &parsed); err != nil {
		t.Fatalf("clamped: block 0 must be valid JSON: %v", err)
	}
	block1, ok := mcp.AsTextContent(clamped.Content[1])
	if !ok || !strings.Contains(block1.Text, "clamped") {
		t.Fatalf("clamped: block 1 should be the clamp note, got %#v", clamped.Content[1])
	}
}

func TestResolveTimestampsEndOnlyUsesDefaultRange(t *testing.T) {
	start, end, err := resolveTimestamps(map[string]any{
		"end": "1711130400000",
	}, "1h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if start >= end {
		t.Fatalf("start = %d, end = %d, want non-inverted default window", start, end)
	}
	if delta := end - start; delta < int64(59*time.Minute/time.Millisecond) || delta > int64(61*time.Minute/time.Millisecond) {
		t.Fatalf("delta = %dms, want about 1h", delta)
	}
}

// TestResolveTimestampsMalformedStartErrorsLoudly pins that present-but-malformed
// timestamp inputs propagate an error out of resolveTimestamps instead of
// silently falling back to the default window (the silent-failure anti-pattern).
func TestResolveTimestampsMalformedStartErrorsLoudly(t *testing.T) {
	if _, _, err := resolveTimestamps(map[string]any{"start": "yesterday"}, "1h"); err == nil {
		t.Fatal("resolveTimestamps with malformed start = nil error, want loud validation error")
	}
}

// TestResolveTimestampsEmptyOrAbsentUsesDefaultRange pins that empty-string / absent
// time input — including a present-but-empty start, the case GetTimestampsWithDefaults
// treats as absent — injects the tool's advertised default window (1h) rather than
// silently falling through to the generic 6h fallback. The present-but-empty start
// case is the one that regressed when the guard checked raw key presence.
func TestResolveTimestampsEmptyOrAbsentUsesDefaultRange(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"empty-string timeRange", map[string]any{"timeRange": ""}},
		{"empty timeRange and empty start", map[string]any{"timeRange": "", "start": ""}},
		{"empty start only", map[string]any{"start": ""}},
		{"absent", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := resolveTimestamps(tc.args, "1h")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			delta := end - start
			if delta < int64(59*time.Minute/time.Millisecond) || delta > int64(61*time.Minute/time.Millisecond) {
				t.Fatalf("delta = %dms, want about 1h (the tool default), not the generic 6h fallback", delta)
			}
		})
	}
}
