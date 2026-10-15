package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
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
// StructuredContent ({"code": ...}). This is a contract: an MCP client branches
// on the code (retry vs fix args), so it must always be set.
func resultCode(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	m := resultStructuredMap(t, r)
	code, ok := m["code"].(string)
	if !ok {
		t.Fatalf("StructuredContent has no string \"code\": %#v", r.StructuredContent)
	}
	return code
}

func resultStructuredMap(t *testing.T, r *mcp.CallToolResult) map[string]any {
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
	return m
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

func TestUpstreamError_PreservesCompleteRendererGuidance(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusBadRequest,
		Body:       `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"query is invalid","url":"https://signoz.io/docs/query-errors","suggestions":["narrow the query"],"errors":[{"message":"unknown field","suggestions":["use a discovered key"]}],"retry":{"delay":5000000000}}}`,
	})

	structured := resultStructuredMap(t, res)
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"code":                CodeValidationFailed,
		"status":              float64(http.StatusBadRequest),
		"upstreamCode":        "invalid_input",
		"upstreamType":        "invalid-input",
		"upstreamMessage":     "query is invalid",
		"upstreamURL":         "https://signoz.io/docs/query-errors",
		"upstreamSuggestions": []any{"narrow the query"},
		"upstreamDetails": []any{map[string]any{
			"message":     "unknown field",
			"suggestions": []any{"use a discovered key"},
		}},
		"upstreamRetry": map[string]any{"delay": float64(5_000_000_000)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("structured guidance = %#v, want %#v", got, want)
	}

	text := resultText(t, res)
	for _, guidance := range []string{
		"query is invalid",
		"unknown field",
		"https://signoz.io/docs/query-errors",
		"narrow the query",
		"use a discovered key",
		"5s",
	} {
		if !strings.Contains(text, guidance) {
			t.Fatalf("error text omitted renderer guidance %q: %q", guidance, text)
		}
	}
}

func TestUpstreamError_UsesFilteredRendererGuidance(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusBadRequest,
		Body:       `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJoYW5kbGVyLWNhbmFyeSJ9.signature","url":"https://signoz.io/docs/query-errors?token=sk_live_urlcanary123456","suggestions":["<script>sk_live_suggestioncanary123456</script>","api_key: handler-colon-canary123456","authorization: auth123handler-canary123456"],"errors":[{"message":"password=sk_live_detailcanary123456","suggestions":["Bearer sk_live_detailhintcanary123456","client_secret: detail-colon-canary123456"]}]}}`,
	})

	wire, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJoYW5kbGVyLWNhbmFyeSJ9.signature",
		"sk_live_urlcanary123456",
		"sk_live_suggestioncanary123456",
		"handler-colon-canary123456",
		"auth123handler-canary123456",
		"sk_live_detailcanary123456",
		"sk_live_detailhintcanary123456",
		"detail-colon-canary123456",
	} {
		if strings.Contains(string(wire), canary) {
			t.Fatalf("filtered renderer guidance leaked %q: %s", canary, wire)
		}
	}
	if strings.Contains(string(wire), "<script>") {
		t.Fatalf("active renderer markup reached the tool result: %s", wire)
	}
}

func TestUpstreamError_HTTPStatusPreservesWrapperContext(t *testing.T) {
	statusErr := &signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `{"status":"error","error":{"type":"forbidden","code":"authz_forbidden","message":"only editors/admins can access this resource","errors":[],"suggestions":[]}}`,
	}
	wrapped := fmt.Errorf("failed to auto-fetch metadata for formula query %q (%s): %w", "A", "cpu.usage", statusErr)

	res := upstreamError(wrapped)

	text := resultText(t, res)
	want := `SigNoz API error: failed to auto-fetch metadata for formula query "A" (cpu.usage): unexpected status 403: only editors/admins can access this resource`
	if text != want {
		t.Fatalf("text = %q, want wrapper context preserved", text)
	}
	if strings.Contains(text, `"code":"authz_forbidden"`) {
		t.Fatalf("text leaked raw upstream JSON: %s", text)
	}
	if code := resultCode(t, res); code != CodePermissionDenied {
		t.Fatalf("code = %q, want %q", code, CodePermissionDenied)
	}
}

func TestUpstreamError_UnrecognizedEnvelopeUsesBodyFreeFallback(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantText string
	}{
		{
			name:     "incomplete renderer envelope",
			status:   http.StatusForbidden,
			body:     `{"status":"error","error":{"type":"forbidden","code":"forbidden"}}`,
			wantCode: CodePermissionDenied,
			wantText: "SigNoz API error: unexpected status 403: permission denied",
		},
		{
			name:     "generic proxy envelope",
			status:   http.StatusInternalServerError,
			body:     `{"status":"error","message":"temporary outage"}`,
			wantCode: CodeUpstreamError,
			wantText: "SigNoz API error: unexpected status 500",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := upstreamError(&signozclient.HTTPStatusError{StatusCode: tc.status, Body: tc.body})

			if text := resultText(t, res); text != tc.wantText {
				t.Fatalf("text = %q, want body-free fallback %q", text, tc.wantText)
			}
			structured := resultStructuredMap(t, res)
			if got := structured["code"]; got != tc.wantCode {
				t.Fatalf("code = %v, want %s", got, tc.wantCode)
			}
			if got := structured["status"]; got != tc.status {
				t.Fatalf("status = %v, want %d", got, tc.status)
			}
			for _, field := range []string{"upstreamCode", "upstreamType", "upstreamMessage", "upstreamAuth"} {
				if _, ok := structured[field]; ok {
					t.Fatalf("unrecognized envelope exposed %s: %#v", field, structured)
				}
			}
		})
	}
}

func TestUpstreamError_UnauthorizedHTTPStatus(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusUnauthorized,
		Body:       `{"status":"error","error":{"type":"unauthorized","code":"unauthenticated","message":"invalid token","errors":[],"suggestions":[]}}`,
	})

	if code := resultCode(t, res); code != CodeUnauthorized {
		t.Fatalf("code = %q, want %q", code, CodeUnauthorized)
	}
	if got := resultStructuredMap(t, res)["status"]; got != http.StatusUnauthorized {
		t.Fatalf("status = %v, want %d", got, http.StatusUnauthorized)
	}
	structured := resultStructuredMap(t, res)
	upstreamAuth, ok := structured["upstreamAuth"].(map[string]string)
	if !ok {
		t.Fatalf("missing upstreamAuth classifier bridge: %#v", structured)
	}
	if got := upstreamAuth["code"]; got != "unauthenticated" {
		t.Fatalf("upstreamAuth.code = %q, want unauthenticated", got)
	}
	payload, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"code":"unauthenticated"`) {
		t.Fatalf("structured content no longer carries assistant-compatible auth code: %s", payload)
	}

	// The bridge is 401-only: a 403 carrying an auth-looking code is a permission
	// denial, not a request to re-authenticate.
	forbidden := resultStructuredMap(t, upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `{"status":"error","error":{"type":"forbidden","code":"forbidden","message":"permission denied","errors":[],"suggestions":[]}}`,
	}))
	if _, ok := forbidden["upstreamAuth"]; ok {
		t.Fatalf("unexpected upstreamAuth for authorization denial: %#v", forbidden)
	}
}

func TestUpstreamError_StatusDerivedCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "bad request",
			status: http.StatusBadRequest,
			body:   `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"bad request"}}`,
			want:   CodeValidationFailed,
		},
		{
			name:   "forbidden",
			status: http.StatusForbidden,
			body:   `{"status":"error","error":{"type":"forbidden","code":"authz_forbidden","message":"only editors/admins can access this resource","errors":[],"suggestions":[]}}`,
			want:   CodePermissionDenied,
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			body:   `{"status":"error","error":{"type":"not-found","code":"not_found","message":"rule does not exist","errors":[],"suggestions":[]}}`,
			want:   CodeNotFound,
		},
		{
			name:   "conflict",
			status: http.StatusConflict,
			body:   `{"status":"error","error":{"type":"already-exists","code":"already_exists","message":"already exists"}}`,
			want:   CodeConflict,
		},
		{
			name:   "rate limited",
			status: http.StatusTooManyRequests,
			body:   `{"status":"error","error":{"type":"too-many-requests","code":"too_many_requests","message":"slow down"}}`,
			want:   CodeRateLimited,
		},
		{
			name:   "unsupported",
			status: http.StatusNotImplemented,
			body:   `{"status":"error","error":{"type":"unsupported","code":"unsupported","message":"not supported"}}`,
			want:   CodeUnsupported,
		},
		{
			name:   "license unavailable",
			status: http.StatusUnavailableForLegalReasons,
			body:   `{"status":"error","error":{"type":"license-unavailable","code":"license_unavailable","message":"license unavailable"}}`,
			want:   CodeLicenseUnavailable,
		},
		{
			name:   "client closed",
			status: statusClientClosedConnection,
			body:   `{"status":"error","error":{"type":"canceled","code":"canceled","message":"client closed"}}`,
			want:   CodeCanceled,
		},
		{
			name:   "gateway timeout",
			status: http.StatusGatewayTimeout,
			body:   `{"status":"error","error":{"type":"timeout","code":"timeout","message":"timed out"}}`,
			want:   CodeTimeout,
		},
		{
			name:   "internal remains upstream error",
			status: http.StatusInternalServerError,
			body:   `{"status":"error","error":{"type":"internal","code":"internal","message":"boom"}}`,
			want:   CodeUpstreamError,
		},
		{
			name:   "legacy execution error remains upstream error",
			status: http.StatusUnprocessableEntity,
			body:   `{"status":"error","errorType":"execution","error":"query execution failed"}`,
			want:   CodeUpstreamError,
		},
		{
			name:   "unrendered method-not-allowed remains upstream error",
			status: http.StatusMethodNotAllowed,
			body:   `{"status":"error","error":{"type":"method-not-allowed","code":"method_not_allowed","message":"wrong method"}}`,
			want:   CodeUpstreamError,
		},
		{
			name:   "request timeout remains upstream error",
			status: http.StatusRequestTimeout,
			body:   `{"status":"error","error":{"type":"timeout","code":"timeout","message":"timed out"}}`,
			want:   CodeUpstreamError,
		},
		{
			name:   "legacy timeout service unavailable",
			status: http.StatusServiceUnavailable,
			body:   `{"status":"error","errorType":"timeout","error":"query timed out"}`,
			want:   CodeTimeout,
		},
		{
			name:   "legacy timeout with empty guidance",
			status: http.StatusServiceUnavailable,
			body:   `{"status":"error","errorType":"timeout","error":""}`,
			want:   CodeTimeout,
		},
		{
			name:   "legacy canceled service unavailable",
			status: http.StatusServiceUnavailable,
			body:   `{"status":"error","errorType":"canceled","error":"query canceled"}`,
			want:   CodeCanceled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := upstreamError(&signozclient.HTTPStatusError{StatusCode: tc.status, Body: tc.body})
			structured := resultStructuredMap(t, res)
			if got := structured["code"]; got != tc.want {
				t.Fatalf("code = %v, want %s", got, tc.want)
			}
			if got := structured["status"]; got != tc.status {
				t.Fatalf("status = %v, want %d", got, tc.status)
			}
			if strings.Contains(tc.body, `"code"`) {
				got, ok := structured["upstreamCode"].(string)
				if !ok || got == "" {
					t.Fatalf("expected upstreamCode to be preserved: %#v", structured)
				}
			}
		})
	}
}

func TestUpstreamError_NonHTTPErrorsKeepCancellationAndDeadlineCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"canceled", fmt.Errorf("request stopped: %w", context.Canceled), CodeCanceled},
		{"deadline", fmt.Errorf("request stopped: %w", context.DeadlineExceeded), CodeTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resultCode(t, upstreamError(tc.err)); got != tc.want {
				t.Fatalf("code = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorWithCause(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		fallback string
		want     string
	}{
		{"canceled", context.Canceled, CodeInternalError, CodeCanceled},
		{"deadline", context.DeadlineExceeded, CodeInternalError, CodeTimeout},
		{"fallback", errors.New("ordinary"), CodeValidationFailed, CodeValidationFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resultCode(t, errorWithCause(tc.err, tc.fallback, tc.err.Error())); got != tc.want {
				t.Fatalf("code = %q, want %q", got, tc.want)
			}
		})
	}
}
