package tools

import (
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// resultText extracts the first text content block of a tool result, asserting
// it is an error result. Used to pin the canonical strings the shared helpers
// produce — the AI assistant branches on these, so they are a contract.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatal("nil result")
	}
	if !r.IsError {
		t.Fatalf("expected an error result, got success: %+v", r.Content)
	}
	if len(r.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := mcp.AsTextContent(r.Content[0])
	if !ok {
		t.Fatal("first content block is not text")
	}
	return tc.Text
}

func TestValidationError_CanonicalForm(t *testing.T) {
	got := resultText(t, validationError("ruleId", "must be a string"))
	want := `Parameter validation failed: "ruleId" must be a string`
	if got != want {
		t.Fatalf("validationError = %q, want %q", got, want)
	}
}

func TestValidationErrorf_InterpolatesReason(t *testing.T) {
	got := resultText(t, validationErrorf("sourcePage", "got %q", "bogus"))
	want := `Parameter validation failed: "sourcePage" got "bogus"`
	if got != want {
		t.Fatalf("validationErrorf = %q, want %q", got, want)
	}
}

func TestRequireStringArg(t *testing.T) {
	cases := []struct {
		name     string
		args     map[string]any
		key      string
		wantVal  string
		wantErr  bool
		wantText string
	}{
		{
			name:    "valid string",
			args:    map[string]any{"id": "abc"},
			key:     "id",
			wantVal: "abc",
		},
		{
			name:     "wrong type",
			args:     map[string]any{"id": 123},
			key:      "id",
			wantErr:  true,
			wantText: `Parameter validation failed: "id" must be a string`,
		},
		{
			name:     "empty string",
			args:     map[string]any{"id": ""},
			key:      "id",
			wantErr:  true,
			wantText: `Parameter validation failed: "id" cannot be empty`,
		},
		{
			name:     "missing key",
			args:     map[string]any{},
			key:      "id",
			wantErr:  true,
			wantText: `Parameter validation failed: "id" cannot be empty`,
		},
		{
			name:     "nil args",
			args:     nil,
			key:      "id",
			wantErr:  true,
			wantText: `Parameter validation failed: "id" cannot be empty`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, errResult := requireStringArg(tc.args, tc.key)
			if tc.wantErr {
				if errResult == nil {
					t.Fatalf("expected an error result, got val=%q", val)
				}
				if got := resultText(t, errResult); got != tc.wantText {
					t.Fatalf("error text = %q, want %q", got, tc.wantText)
				}
				return
			}
			if errResult != nil {
				t.Fatalf("unexpected error result: %v", errResult.Content)
			}
			if val != tc.wantVal {
				t.Fatalf("val = %q, want %q", val, tc.wantVal)
			}
		})
	}
}

func TestNotAJSONObjectError_CanonicalForm(t *testing.T) {
	got := resultText(t, notAJSONObjectError())
	want := "invalid arguments format: expected JSON object"
	if got != want {
		t.Fatalf("notAJSONObjectError = %q, want %q", got, want)
	}
}

func TestNotAConfigObjectError_CanonicalForm(t *testing.T) {
	got := resultText(t, notAConfigObjectError())
	want := `Parameter validation failed: the configuration object is empty or improperly formatted.`
	if got != want {
		t.Fatalf("notAConfigObjectError = %q, want %q", got, want)
	}
}

func TestUpstreamError_UniformPrefix(t *testing.T) {
	got := resultText(t, upstreamError(errors.New("connection refused")))
	want := "SigNoz API error: connection refused"
	if got != want {
		t.Fatalf("upstreamError = %q, want %q", got, want)
	}
}
