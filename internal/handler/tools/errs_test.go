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

func TestRequireArgsMap(t *testing.T) {
	t.Run("object passes through", func(t *testing.T) {
		in := map[string]any{"a": 1}
		got, errResult := requireArgsMap(in)
		if errResult != nil {
			t.Fatalf("unexpected error result: %v", errResult.Content)
		}
		if got == nil || got["a"] != 1 {
			t.Fatalf("got = %#v, want passthrough of input map", got)
		}
	})
	for _, raw := range []any{nil, "string-not-object", 42, []any{"x"}} {
		_, errResult := requireArgsMap(raw)
		if errResult == nil {
			t.Fatalf("requireArgsMap(%#v) = no error, want JSON-object guard", raw)
		}
		if got := resultText(t, errResult); got != "invalid arguments format: expected JSON object" {
			t.Fatalf("requireArgsMap(%#v) text = %q, want JSON-object guard", raw, got)
		}
	}
}

func TestRequireStringField(t *testing.T) {
	cases := []struct {
		name      string
		args      map[string]any
		reason    string
		wantVal   string
		wantErrIs string
	}{
		{name: "valid", args: map[string]any{"type": "slack"}, wantVal: "slack"},
		{
			name:      "wrong type -> must be a string",
			args:      map[string]any{"type": 7},
			wantErrIs: `Parameter validation failed: "type" must be a string`,
		},
		{
			name:      "empty -> is required with reason",
			args:      map[string]any{"type": ""},
			reason:    ". Must be one of: slack, webhook",
			wantErrIs: `Parameter validation failed: "type" is required. Must be one of: slack, webhook`,
		},
		{
			name:      "missing -> is required",
			args:      map[string]any{},
			wantErrIs: `Parameter validation failed: "type" is required`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := requireStringField(tc.args, "type", tc.reason)
			if tc.wantErrIs != "" {
				if err == nil {
					t.Fatalf("expected error, got val=%q", val)
				}
				if err.Error() != tc.wantErrIs {
					t.Fatalf("err = %q, want %q", err.Error(), tc.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
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
