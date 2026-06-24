package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/mark3labs/mcp-go/mcp"
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

// TestParseParamsClamped pins the uniform per-page clamp for the summary list
// tools, accepting number-or-string limits.
func TestParseParamsClamped(t *testing.T) {
	limit, _, clamped := paginate.ParseParamsClamped(map[string]any{"limit": "999999"})
	if limit != paginate.MaxLimit || !clamped {
		t.Fatalf("over-cap: limit=%d clamped=%v, want %d true", limit, clamped, paginate.MaxLimit)
	}

	limit, _, clamped = paginate.ParseParamsClamped(map[string]any{"limit": float64(10)})
	if limit != 10 || clamped {
		t.Fatalf("number under-cap: limit=%d clamped=%v, want 10 false", limit, clamped)
	}

	limit, offset, clamped := paginate.ParseParamsClamped(map[string]any{})
	if limit != paginate.DefaultLimit || offset != paginate.DefaultOffset || clamped {
		t.Fatalf("defaults: limit=%d offset=%d clamped=%v", limit, offset, clamped)
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
