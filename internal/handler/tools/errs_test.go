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

// resultCode extracts the machine-readable error code from an error result's
// StructuredContent ({"code": ...}). Family C (#365) makes this a contract: an
// MCP client branches on the code (retry vs fix args), so it must always be set.
func resultCode(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatal("nil result")
	}
	if r.StructuredContent == nil {
		t.Fatal("error result is missing StructuredContent")
	}
	m, ok := r.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent is %T, want map[string]any", r.StructuredContent)
	}
	code, ok := m["code"].(string)
	if !ok {
		t.Fatalf("StructuredContent has no string \"code\": %#v", r.StructuredContent)
	}
	return code
}

func TestValidationError_CanonicalForm(t *testing.T) {
	res := validationError("ruleId", "must be a string")
	got := resultText(t, res)
	want := `Parameter validation failed: "ruleId" must be a string`
	if got != want {
		t.Fatalf("validationError = %q, want %q", got, want)
	}
	if code := resultCode(t, res); code != CodeValidationFailed {
		t.Fatalf("validationError code = %q, want %q", code, CodeValidationFailed)
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
	t.Run("nil payload becomes an empty map (no error)", func(t *testing.T) {
		// A nil/untyped-nil payload means "no arguments object" — the common
		// case of an omitted required param or a no-args call to an all-optional
		// tool. It must NOT be the JSON-object guard: downstream per-field checks
		// own the specific diagnosis (e.g. `"ruleId" cannot be empty`).
		got, errResult := requireArgsMap(nil)
		if errResult != nil {
			t.Fatalf("requireArgsMap(nil) = error %v, want empty map + no error", errResult.Content)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("requireArgsMap(nil) = %#v, want empty non-nil map", got)
		}
	})
	// A genuinely malformed payload — non-nil and not an object — still returns
	// the shared JSON-object guard, since no per-field check can run against it.
	for _, raw := range []any{"string-not-object", 42, []any{"x"}} {
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
	res := upstreamError(errors.New("connection refused"))
	got := resultText(t, res)
	want := "SigNoz API error: connection refused"
	if got != want {
		t.Fatalf("upstreamError = %q, want %q", got, want)
	}
	if code := resultCode(t, res); code != CodeUpstreamError {
		t.Fatalf("upstreamError code = %q, want %q", code, CodeUpstreamError)
	}
}

// TestErrorHelpers_StructuredCodes pins the full code taxonomy each helper
// emits in StructuredContent. The text block stays unchanged (covered by the
// per-helper tests above); this asserts the additive machine-readable code.
func TestErrorHelpers_StructuredCodes(t *testing.T) {
	cases := []struct {
		name string
		res  *mcp.CallToolResult
		want string
	}{
		{"validationError", validationError("f", "is bad"), CodeValidationFailed},
		{"validationErrorf", validationErrorf("f", "got %q", "x"), CodeValidationFailed},
		{"requireStringArg-wrong-type", mustErr(requireStringArg(map[string]any{"id": 1}, "id")), CodeValidationFailed},
		{"requireStringArg-empty", mustErr(requireStringArg(map[string]any{}, "id")), CodeValidationFailed},
		{"notAJSONObjectError", notAJSONObjectError(), CodeValidationFailed},
		{"notAConfigObjectError", notAConfigObjectError(), CodeValidationFailed},
		{"upstreamError", upstreamError(errors.New("boom")), CodeUpstreamError},
		{"notFoundError", notFoundError("no such alert"), CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resultCode(t, tc.res); got != tc.want {
				t.Fatalf("%s code = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// mustErr unwraps the (value, *CallToolResult) return of requireStringArg,
// asserting the error result is non-nil so it can be used inline in a table.
func mustErr(_ string, r *mcp.CallToolResult) *mcp.CallToolResult {
	if r == nil {
		panic("expected a non-nil error result")
	}
	return r
}
