package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

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

func TestUpstreamError_ForbiddenHTTPStatus(t *testing.T) {
	statusErr := &signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `{"status":"error","error":{"type":"forbidden","code":"authz_forbidden","message":"only editors/admins can access this resource","errors":[],"suggestions":[]}}`,
	}

	res := upstreamError(statusErr)

	if got := resultText(t, res); got != "SigNoz API error: unexpected status 403: only editors/admins can access this resource" {
		t.Fatalf("upstreamError text = %q, want message-only status error", got)
	}
	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodePermissionDenied {
		t.Fatalf("code = %v, want %s", got, CodePermissionDenied)
	}
	if got := structured["status"]; got != http.StatusForbidden {
		t.Fatalf("status = %v, want %d", got, http.StatusForbidden)
	}
	if got := structured["upstreamCode"]; got != "authz_forbidden" {
		t.Fatalf("upstreamCode = %v, want authz_forbidden", got)
	}
	if got := structured["upstreamMessage"]; got != "only editors/admins can access this resource" {
		t.Fatalf("upstreamMessage = %v, want backend message", got)
	}
	if got := structured["upstreamType"]; got != "forbidden" {
		t.Fatalf("upstreamType = %v, want forbidden", got)
	}
}

func TestUpstreamError_HTTPStatusPreservesWrapperContext(t *testing.T) {
	statusErr := &signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `{"status":"error","error":{"type":"forbidden","code":"authz_forbidden","message":"only editors/admins can access this resource"}}`,
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

func TestUpstreamError_ForbiddenGenericCodeDoesNotLeakAuthEnvelopeInText(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `{"status":"error","error":{"type":"forbidden","code":"forbidden","message":"permission denied"}}`,
	})

	text := resultText(t, res)
	if strings.Contains(text, `"code":"forbidden"`) || strings.Contains(text, `"code": "forbidden"`) {
		t.Fatalf("text leaked raw auth-looking upstream code: %s", text)
	}
	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodePermissionDenied {
		t.Fatalf("code = %v, want %s", got, CodePermissionDenied)
	}
	if got := structured["upstreamCode"]; got != "forbidden" {
		t.Fatalf("upstreamCode = %v, want forbidden", got)
	}
	if got := structured["upstreamMessage"]; got != "permission denied" {
		t.Fatalf("upstreamMessage = %v, want permission denied", got)
	}
	if _, ok := structured["upstreamAuth"]; ok {
		t.Fatalf("unexpected upstreamAuth for authorization denial: %#v", structured)
	}
}

func TestUpstreamError_ParseableEnvelopeWithoutMessageDoesNotLeakRawJSON(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `{"status":"error","error":{"type":"forbidden","code":"forbidden"}}`,
	})

	text := resultText(t, res)
	if text != "SigNoz API error: unexpected status 403" {
		t.Fatalf("text = %q, want status-only text for message-less parseable envelope", text)
	}
	if strings.Contains(text, `"code":"forbidden"`) || strings.Contains(text, `"code": "forbidden"`) {
		t.Fatalf("text leaked raw auth-looking upstream code: %s", text)
	}
	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodePermissionDenied {
		t.Fatalf("code = %v, want %s", got, CodePermissionDenied)
	}
	if got := structured["upstreamCode"]; got != "forbidden" {
		t.Fatalf("upstreamCode = %v, want forbidden", got)
	}
	if got := structured["upstreamType"]; got != "forbidden" {
		t.Fatalf("upstreamType = %v, want forbidden", got)
	}
	if _, ok := structured["upstreamMessage"]; ok {
		t.Fatalf("unexpected upstreamMessage for message-less envelope: %#v", structured)
	}
	if _, ok := structured["upstreamAuth"]; ok {
		t.Fatalf("unexpected upstreamAuth for 403: %#v", structured)
	}
}

func TestUpstreamError_HTTPErrorTextIsBounded(t *testing.T) {
	longMessage := strings.Repeat("x", 5000) + "tail"
	bodyBytes, err := json.Marshal(map[string]any{
		"status": "error",
		"error": map[string]any{
			"type":    "forbidden",
			"code":    "authz_forbidden",
			"message": longMessage,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       string(bodyBytes),
	})

	text := resultText(t, res)
	if !strings.Contains(text, "...(truncated)") {
		t.Fatalf("text = %q, want truncated marker", text)
	}
	if strings.Contains(text, "tail") {
		t.Fatalf("text leaked end of overlarge upstream message: %s", text)
	}
	structured := resultStructuredMap(t, res)
	upstreamMessage, ok := structured["upstreamMessage"].(string)
	if !ok {
		t.Fatalf("missing upstreamMessage: %#v", structured)
	}
	if !strings.Contains(upstreamMessage, "...(truncated)") {
		t.Fatalf("upstreamMessage = %q, want truncated marker", upstreamMessage)
	}
	if strings.Contains(upstreamMessage, "tail") {
		t.Fatalf("upstreamMessage leaked end of overlarge upstream message")
	}

	unparseable := strings.Repeat("<html>", 1000) + "tail"
	res = upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusBadGateway,
		Body:       unparseable,
	})
	text = resultText(t, res)
	if !strings.Contains(text, "...(truncated)") {
		t.Fatalf("unparseable text = %q, want truncated marker", text)
	}
	if strings.Contains(text, "tail") {
		t.Fatalf("unparseable text leaked end of overlarge upstream body")
	}
}

func TestUpstreamError_ForbiddenHTTPStatusWithUnparseableBody(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusForbidden,
		Body:       `permission denied`,
	})

	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodePermissionDenied {
		t.Fatalf("code = %v, want %s", got, CodePermissionDenied)
	}
	if got := structured["status"]; got != http.StatusForbidden {
		t.Fatalf("status = %v, want %d", got, http.StatusForbidden)
	}
	if _, ok := structured["upstreamCode"]; ok {
		t.Fatalf("unexpected upstreamCode for unparseable body: %#v", structured)
	}
	if text := resultText(t, res); text != "SigNoz API error: unexpected status 403: permission denied" {
		t.Fatalf("text = %q, want preserved status body", text)
	}
}

func TestUpstreamError_UnauthorizedHTTPStatus(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusUnauthorized,
		Body:       `{"status":"error","error":{"type":"unauthorized","code":"unauthenticated","message":"invalid token"}}`,
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
}

func TestUpstreamError_GenericHTTPStatusKeepsUpstreamCode(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusInternalServerError,
		Body:       `{"status":"error","message":"temporary outage"}`,
	})

	if code := resultCode(t, res); code != CodeUpstreamError {
		t.Fatalf("code = %q, want %q", code, CodeUpstreamError)
	}
	if got := resultStructuredMap(t, res)["status"]; got != http.StatusInternalServerError {
		t.Fatalf("status = %v, want %d", got, http.StatusInternalServerError)
	}
}

func TestUpstreamError_LegacyErrorBody(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       `{"status":"error","errorType":"exec","error":"query execution failed"}`,
	})

	if got := resultText(t, res); got != "SigNoz API error: unexpected status 422: query execution failed" {
		t.Fatalf("text = %q, want legacy error message", got)
	}
	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodeUpstreamError {
		t.Fatalf("code = %v, want %s", got, CodeUpstreamError)
	}
	if got := structured["status"]; got != http.StatusUnprocessableEntity {
		t.Fatalf("status = %v, want %d", got, http.StatusUnprocessableEntity)
	}
	if got := structured["upstreamType"]; got != "exec" {
		t.Fatalf("upstreamType = %v, want exec", got)
	}
	if got := structured["upstreamMessage"]; got != "query execution failed" {
		t.Fatalf("upstreamMessage = %v, want query execution failed", got)
	}
	if _, ok := structured["upstreamCode"]; ok {
		t.Fatalf("unexpected upstreamCode for legacy body: %#v", structured)
	}
}

func TestUpstreamError_NotFoundHTTPStatus(t *testing.T) {
	res := upstreamError(&signozclient.HTTPStatusError{
		StatusCode: http.StatusNotFound,
		Body:       `{"status":"error","error":{"type":"not-found","code":"not_found","message":"rule does not exist"}}`,
	})

	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodeNotFound {
		t.Fatalf("code = %v, want %s", got, CodeNotFound)
	}
	if got := structured["status"]; got != http.StatusNotFound {
		t.Fatalf("status = %v, want %d", got, http.StatusNotFound)
	}
	if got := structured["upstreamCode"]; got != "not_found" {
		t.Fatalf("upstreamCode = %v, want not_found", got)
	}
	if got := structured["upstreamMessage"]; got != "rule does not exist" {
		t.Fatalf("upstreamMessage = %v, want backend message", got)
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
			body:   `{"status":"error","errorType":"exec","error":"query execution failed"}`,
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
			if got := structured["upstreamCode"]; strings.Contains(tc.body, `"code"`) && got == "" {
				t.Fatalf("expected upstreamCode to be preserved: %#v", structured)
			}
		})
	}
}

// TestErrorHelpers_StructuredCodes pins the full code taxonomy each helper
// emits in StructuredContent. Text behavior is covered by the per-helper tests
// above; this asserts the machine-readable code.
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
		{"clientError", clientError(errors.New("missing credentials")), CodeUnauthorized},
		{"InternalErrorResult", InternalErrorResult("marshal failed"), CodeInternalError},
		{"upstreamResponseError", upstreamResponseError("malformed response"), CodeUpstreamError},
		{"validationResult", validationResult("invalid configuration"), CodeValidationFailed},
		{"upstreamError", upstreamError(errors.New("boom")), CodeUpstreamError},
		{"upstreamError-unauthorized", upstreamError(&signozclient.HTTPStatusError{StatusCode: http.StatusUnauthorized, Body: `{}`}), CodeUnauthorized},
		{"upstreamError-forbidden", upstreamError(&signozclient.HTTPStatusError{StatusCode: http.StatusForbidden, Body: `{}`}), CodePermissionDenied},
		{"upstreamError-not-found", upstreamError(&signozclient.HTTPStatusError{StatusCode: http.StatusNotFound, Body: `{}`}), CodeNotFound},
		{"upstreamError-conflict", upstreamError(&signozclient.HTTPStatusError{StatusCode: http.StatusConflict, Body: `{}`}), CodeConflict},
		{"upstreamError-rate-limited", upstreamError(&signozclient.HTTPStatusError{StatusCode: http.StatusTooManyRequests, Body: `{}`}), CodeRateLimited},
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

// mustErr unwraps the (value, *CallToolResult) return of requireStringArg,
// asserting the error result is non-nil so it can be used inline in a table.
func mustErr(_ string, r *mcp.CallToolResult) *mcp.CallToolResult {
	if r == nil {
		panic("expected a non-nil error result")
	}
	return r
}
