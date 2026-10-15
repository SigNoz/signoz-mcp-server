package tools

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

// Current-generation QB v5 envelope: bare summary in error.message, per-term
// detail in error.errors[].
const keyNotFoundEnvelopeBody = `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"Found 1 errors while parsing the search expression.","url":"https://signoz.io/docs/userguide/search-troubleshooting/","errors":[{"message":"key ` + "`service.name`" + ` not found","suggestions":[]}],"suggestions":[]}}`

// Older-generation envelope: the detail is inlined in a string error field.
const keyNotFoundLegacyBody = `{"status":"error","errorType":"invalid_input","error":"while parsing the search expression: key ` + "`service.name`" + ` not found"}`

func keyNotFound400(body string) *signozclient.HTTPStatusError {
	return &signozclient.HTTPStatusError{StatusCode: http.StatusBadRequest, Body: body}
}

func legacyErrorBody(errorType, message string) string {
	body, _ := json.Marshal(map[string]any{
		"status":    "error",
		"errorType": errorType,
		"error":     message,
	})
	return string(body)
}

func TestMissingFilterKeys(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "new envelope with errors[] detail",
			err:  keyNotFound400(keyNotFoundEnvelopeBody),
			want: []string{"service.name"},
		},
		{
			name: "legacy inline message",
			err:  keyNotFound400(keyNotFoundLegacyBody),
			want: []string{"service.name"},
		},
		{
			name: "multiple keys deduped in order",
			err: keyNotFound400(legacyErrorBody("invalid_input", "Found 3 errors while parsing the search expression: "+
				"key `service.name` not found; key `env` not found; key `service.name` not found")),
			want: []string{"service.name", "env"},
		},
		{
			name: "wrapped error chain still detected",
			err:  errWrap(keyNotFound400(keyNotFoundEnvelopeBody)),
			want: []string{"service.name"},
		},
		{
			name: "recognized 400 without the key-not-found wording",
			err:  keyNotFound400(`{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"bad step interval","errors":[],"suggestions":[]}}`),
			want: nil,
		},
		{
			name: "unrecognized plain 400 cannot trigger recovery",
			err:  keyNotFound400("key `service.name` not found"),
			want: nil,
		},
		{
			name: "unrecognized proxy 400 cannot trigger recovery",
			err:  keyNotFound400(`{"status":"error","message":"key ` + "`service.name`" + ` not found"}`),
			want: nil,
		},
		{
			name: "non-400 status is ignored",
			err:  &signozclient.HTTPStatusError{StatusCode: http.StatusInternalServerError, Body: "key `service.name` not found"},
			want: nil,
		},
		{
			name: "non-HTTP error is ignored",
			err:  errors.New("key `service.name` not found"),
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := missingFilterKeys(tc.err); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("missingFilterKeys = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func errWrap(err error) error {
	return &upstreamFetchError{err: err}
}

func TestMissingFilterKeys_CapsSurfacedKeys(t *testing.T) {
	var b strings.Builder
	for r := 'a'; r < 'a'+20; r++ {
		b.WriteString("key `" + string(r) + ".name` not found; ")
	}
	got := missingFilterKeys(keyNotFound400(legacyErrorBody("invalid_input", b.String())))
	if len(got) != missingFilterKeysLimit {
		t.Fatalf("len(missingFilterKeys) = %d, want cap %d", len(got), missingFilterKeysLimit)
	}
}

// TestMissingFilterKeys_DropsOversizedKeys pins the per-key length bound: the 400
// body is upstream-controlled, so an enormous captured "key" must not flow into
// guidance text or log attributes.
func TestMissingFilterKeys_DropsOversizedKeys(t *testing.T) {
	body := "key `" + strings.Repeat("x", missingFilterKeyMaxLen+1) + "` not found; key `service.name` not found"
	got := missingFilterKeys(keyNotFound400(legacyErrorBody("invalid_input", body)))
	if !reflect.DeepEqual(got, []string{"service.name"}) {
		t.Fatalf("missingFilterKeys = %#v, want oversized key dropped", got)
	}
	if only := missingFilterKeys(keyNotFound400(legacyErrorBody("invalid_input", "key `"+strings.Repeat("x", missingFilterKeyMaxLen+1)+"` not found"))); only != nil {
		t.Fatalf("missingFilterKeys = %#v, want nil when every key is oversized", only)
	}
}

// Parsed guidance is bounded before this helper scans it: a key beyond the
// window fails open, while one at the beginning remains actionable.
func TestMissingFilterKeys_ParsedGuidanceScanBounded(t *testing.T) {
	padding := strings.Repeat("x", missingFilterKeyScanBytes)
	if got := missingFilterKeys(keyNotFound400(legacyErrorBody("invalid_input", padding+"key `service.name` not found"))); got != nil {
		t.Fatalf("missingFilterKeys = %#v, want nil for a match beyond the scan window", got)
	}
	got := missingFilterKeys(keyNotFound400(legacyErrorBody("invalid_input", "key `service.name` not found"+padding)))
	if !reflect.DeepEqual(got, []string{"service.name"}) {
		t.Fatalf("missingFilterKeys = %#v, want detection inside the scan window of an oversized body", got)
	}
}

func TestUpstreamQueryError_LogsGuidanceAndStructuredKeys(t *testing.T) {
	res := upstreamQueryError(keyNotFound400(keyNotFoundEnvelopeBody), "logs")

	text := resultText(t, res)
	for _, want := range []string{"Found 1 errors while parsing the search expression.", "key `service.name` not found"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text should keep upstream summary/detail %q, got %q", want, text)
		}
	}
	for _, want := range []string{
		"`service.name`, which does not exist in this workspace's logs data",
		"no spec-mandated resource attributes",
		`signoz_get_field_keys (signal="logs"`,
		"k8s.deployment.name",
		"remove the failing condition",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("guidance missing %q in %q", want, text)
		}
	}

	structured := resultStructuredMap(t, res)
	if got := structured["code"]; got != CodeValidationFailed {
		t.Fatalf("code = %v, want %s", got, CodeValidationFailed)
	}
	if got := structured["missingKeys"]; !reflect.DeepEqual(got, []string{"service.name"}) {
		t.Fatalf("missingKeys = %#v, want [service.name]", got)
	}
}

func TestUpstreamQueryError_TracesAndGenericSignalWording(t *testing.T) {
	tracesText := resultText(t, upstreamQueryError(keyNotFound400(keyNotFoundLegacyBody), "traces"))
	if !strings.Contains(tracesText, "this workspace's traces data") {
		t.Fatalf("traces guidance missing signal noun: %q", tracesText)
	}
	if strings.Contains(tracesText, "spec-mandated") || strings.Contains(tracesText, "k8s.deployment.name") {
		t.Fatalf("traces guidance leaked logs-specific wording: %q", tracesText)
	}

	genericText := resultText(t, upstreamQueryError(keyNotFound400(keyNotFoundLegacyBody), ""))
	if !strings.Contains(genericText, "signoz_get_field_keys for the queried signal") {
		t.Fatalf("generic guidance missing signal-agnostic discovery hint: %q", genericText)
	}
}

func TestUpstreamQueryError_NoMissingKeyIsPlainUpstreamError(t *testing.T) {
	err := keyNotFound400(`{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"bad step interval","errors":[],"suggestions":[]}}`)

	got := upstreamQueryError(err, "logs")
	want := upstreamError(err)

	if resultText(t, got) != resultText(t, want) {
		t.Fatalf("text diverged without a missing key: %q vs %q", resultText(t, got), resultText(t, want))
	}
	if _, ok := resultStructuredMap(t, got)["missingKeys"]; ok {
		t.Fatalf("unexpected missingKeys without a key-not-found body")
	}
}
