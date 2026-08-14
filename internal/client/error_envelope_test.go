package client

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUpstreamErrorBody_CurrentRendererFixture(t *testing.T) {
	// Source-derived from SigNoz abf60c0: pkg/errors/http.go defines the
	// envelope, pkg/http/render/render.go wraps it, and render_test.go pins the
	// time.Duration-as-nanoseconds wire encoding.
	body, err := os.ReadFile("testdata/current-renderer-error.json")
	require.NoError(t, err)

	got := ParseUpstreamErrorBody(string(body))
	require.True(t, got.Recognized)
	assert.True(t, got.StatusError)
	assert.Empty(t, got.DriftFields)
	assert.Equal(t, "invalid_input", got.Code)
	assert.Equal(t, "invalid-input", got.Type)
	assert.Equal(t, "The query is invalid.", got.Message)
	assert.Equal(t, "https://signoz.io/docs/userguide/search-troubleshooting/", got.URL)
	assert.Equal(t, []string{"Use an existing field key."}, got.Suggestions)
	require.Equal(t, []UpstreamErrorDetail{{
		Message:     "field `service.nam` was not found",
		Suggestions: []string{"did you mean: `service.name`"},
	}}, got.Details)
	require.NotNil(t, got.Retry)
	assert.Equal(t, int64(5_000_000_000), got.Retry.Delay)
	assert.Contains(t, got.FoldedMessage(), "field `service.nam` was not found")
	for _, want := range []string{
		"Documentation: https://signoz.io/docs/userguide/search-troubleshooting/",
		"Suggestions: Use an existing field key.",
		"Suggestions for \"field `service.nam` was not found\": did you mean: `service.name`",
		"Retry delay: 5s (5000000000 ns)",
	} {
		assert.Contains(t, got.ClientSafeText(), want)
	}
}

func TestParseUpstreamErrorBody_HistoricalAndLegacyShapes(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
		wantType string
		wantText string
	}{
		{
			name:     "historical nested renderer without type or guidance",
			body:     `{"status":"error","error":{"code":"invalid_input","message":"bad query","errors":[{"message":"bad field"}]}}`,
			wantCode: "invalid_input",
			wantText: "bad query (bad field)",
		},
		{
			name:     "legacy query service",
			body:     `{"status":"error","errorType":"execution","error":"query execution failed"}`,
			wantType: "execution",
			wantText: "query execution failed",
		},
		{
			name:     "observed older query builder type",
			body:     `{"status":"error","errorType":"invalid_input","error":"key not found"}`,
			wantType: "invalid_input",
			wantText: "key not found",
		},
		{
			name:     "legacy type with empty guidance",
			body:     `{"status":"error","errorType":"timeout","error":""}`,
			wantType: "timeout",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseUpstreamErrorBody(tc.body)
			require.True(t, got.Recognized)
			assert.True(t, got.StatusError)
			assert.Empty(t, got.DriftFields)
			assert.Equal(t, tc.wantCode, got.Code)
			assert.Equal(t, tc.wantType, got.Type)
			assert.Equal(t, tc.wantText, got.FoldedMessage())
		})
	}
}

func TestParseUpstreamErrorBody_PositiveRecognitionBoundary(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		statusError bool
		drift       []string
	}{
		{name: "top-level renderer tuple is not verified", body: `{"status":"error","type":"invalid-input","code":"invalid_input","message":"bad"}`, statusError: true, drift: []string{"envelope"}},
		{name: "generic proxy object", body: `{"status":"error","message":"bad"}`, statusError: true, drift: []string{"envelope"}},
		{name: "nested message missing", body: `{"status":"error","error":{"code":"invalid_input"}}`, statusError: true, drift: []string{"message"}},
		{name: "nested code wrong type", body: `{"status":"error","error":{"code":42,"message":"bad"}}`, statusError: true, drift: []string{"code"}},
		{name: "legacy error type missing", body: `{"status":"error","error":"bad"}`, statusError: true, drift: []string{"envelope"}},
		{name: "wrong status", body: `{"status":"fail","error":{"code":"invalid_input","message":"bad"}}`},
		{name: "missing status", body: `{"error":{"code":"invalid_input","message":"bad"}}`},
		{name: "array", body: `["bad"]`},
		{name: "unrelated malformed", body: `{"other":`},
		{name: "truncated status error", body: `{"status":"error"`, statusError: true, drift: []string{"envelope"}},
		{name: "status error with trailing JSON", body: `{"status":"error","error":{"code":"invalid_input","message":"bad"}} trailing`, statusError: true, drift: []string{"envelope"}},
		{name: "oversized", body: strings.Repeat(" ", maxErrorEnvelopeBytes+1)},
		{
			name:        "oversized status error with intervening field is value-free drift",
			body:        `{"status":"error","requestId":"req-1","error":{"code":"invalid_input","message":"untrusted-canary` + strings.Repeat("x", maxErrorEnvelopeBytes) + `"}}`,
			statusError: true,
			drift:       []string{"envelope"},
		},
		{
			name: "oversized body with status later is not trusted",
			body: `{"padding":"` + strings.Repeat("x", maxErrorEnvelopeBytes) +
				`","status":"error","error":{"code":"invalid_input","message":"bad"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseUpstreamErrorBody(tc.body)
			assert.False(t, got.Recognized)
			assert.Equal(t, tc.statusError, got.StatusError)
			assert.Equal(t, tc.drift, got.DriftFields)
			assert.Empty(t, got.ClientSafeText())
		})
	}
}

func TestParseUpstreamErrorBody_OptionalFieldsFailOpenIndependently(t *testing.T) {
	body := `{"status":"error","error":{` +
		`"type":42,"code":"invalid_input","message":"summary",` +
		`"url":7,` +
		`"suggestions":["keep top",false],` +
		`"errors":[{"message":"keep detail","suggestions":["keep detail suggestion",9]},{"future":true}],` +
		`"retry":{"delay":"5s"}` +
		`}}`

	got := ParseUpstreamErrorBody(body)
	require.True(t, got.Recognized)
	assert.Equal(t, "invalid_input", got.Code)
	assert.Equal(t, "summary", got.Message)
	assert.Empty(t, got.Type)
	assert.Empty(t, got.URL)
	assert.Equal(t, []string{"keep top"}, got.Suggestions)
	require.Len(t, got.Details, 1)
	assert.Equal(t, "keep detail", got.Details[0].Message)
	assert.Equal(t, []string{"keep detail suggestion"}, got.Details[0].Suggestions)
	assert.Nil(t, got.Retry)
	assert.Equal(t, []string{"type", "url", "suggestions", "errors", "retry"}, got.DriftFields)
}

func TestParseUpstreamErrorBody_BoundsCollectionsAndMessages(t *testing.T) {
	longMessage := strings.Repeat("m", 5000) + "tail-canary"
	body := `{"status":"error","error":{"code":"invalid_input","message":"` + longMessage + `",` +
		`"suggestions":["s1","s2","s3","s4","s5","s6"],` +
		`"errors":[{"message":"d1"},{"message":"d2"},{"message":"d3"},{"message":"d4"},{"message":"d5"},{"message":"d6"}]}}`

	got := ParseUpstreamErrorBody(body)
	require.True(t, got.Recognized)
	assert.Len(t, got.Suggestions, maxErrorArrayItems)
	assert.Len(t, got.Details, maxErrorArrayItems)
	assert.Contains(t, got.Message, "...(truncated)")
	assert.NotContains(t, got.Message, "tail-canary")
	assert.Empty(t, got.DriftFields)

	oversizedArray := `["` + strings.Repeat("x", maxErrorArrayBytes) + `"]`
	got = ParseUpstreamErrorBody(`{"status":"error","error":{"code":"invalid_input","message":"summary","suggestions":` + oversizedArray + `}}`)
	require.True(t, got.Recognized)
	assert.Empty(t, got.Suggestions)
	assert.Equal(t, []string{"suggestions"}, got.DriftFields)
}

func TestParseUpstreamErrorBody_DeduplicatesButStillChecksLateDrift(t *testing.T) {
	body := `{"status":"error","error":{"code":"invalid_input","message":"summary",` +
		`"suggestions":["same","same","s2","s3","s4","s5",false],` +
		`"errors":[{"message":"summary"},{"message":"same detail"},{"message":"same detail"},` +
		`{"message":"d2"},{"message":"d3"},{"message":"d4"},{"message":"d5"},42]}}`

	got := ParseUpstreamErrorBody(body)
	require.True(t, got.Recognized)
	assert.Equal(t, []string{"same", "s2", "s3", "s4", "s5"}, got.Suggestions)
	assert.Equal(t, []UpstreamErrorDetail{
		{Message: "same detail"},
		{Message: "d2"},
		{Message: "d3"},
		{Message: "d4"},
		{Message: "d5"},
	}, got.Details)
	assert.Equal(t, []string{"suggestions", "errors"}, got.DriftFields, "malformed entries after the output cap must remain detectable")
}

func TestParseUpstreamErrorBody_DropsUnsafeValuesWhole(t *testing.T) {
	body := `{"status":"error","error":{` +
		`"type":"access.token:type-secret-canary","code":"api.key:code-secret-canary",` +
		`"message":"SIGNOZ_API_KEY=message-secret-canary",` +
		`"url":"https://signoz.io/docs?token=url-secret-canary",` +
		`"suggestions":["client_secret=suggestion-secret-canary","client_secret: colon-suggestion-secret-canary","refresh token: refresh-colon-secret-canary","authorization: auth123secretcanary","authorization: AWS4-HMAC-SHA256 aws123secretcanary","Bearer abcdefghijk1234","authorization: request editor access","authorization: permission denied","authorization: AWS4-HMAC-SHA256 missing","authorization: AWS4-HMAC-SHA256.","token: signature mismatch"],` +
		`"errors":[{"message":"<script>detail-canary</script>","suggestions":["![x](https://attacker.test/canary)","keep detail guidance"]},{"message":"api key with key: backend-colon-secret-canary doesn't exist.","suggestions":[]}]` +
		`}}`

	got := ParseUpstreamErrorBody(body)
	require.True(t, got.Recognized)
	assert.Empty(t, got.Code)
	assert.Empty(t, got.Type)
	assert.Empty(t, got.Message)
	assert.Empty(t, got.URL)
	assert.Equal(t, []string{"authorization: request editor access", "authorization: permission denied", "authorization: AWS4-HMAC-SHA256 missing", "authorization: AWS4-HMAC-SHA256.", "token: signature mismatch"}, got.Suggestions)
	require.Len(t, got.Details, 1)
	assert.Empty(t, got.Details[0].Message)
	assert.Equal(t, []string{"keep detail guidance"}, got.Details[0].Suggestions)
	assert.Equal(t, []string{"code", "message", "type", "url", "suggestions", "errors"}, got.DriftFields)

	wire := got.ClientSafeText()
	for _, secret := range []string{"code-secret-canary", "type-secret-canary", "message-secret-canary", "url-secret-canary", "suggestion-secret-canary", "colon-suggestion-secret-canary", "refresh-colon-secret-canary", "auth123secretcanary", "aws123secretcanary", "backend-colon-secret-canary", "abcdefghijk1234", "detail-canary", "attacker.test"} {
		assert.NotContains(t, wire, secret)
	}
	assert.Contains(t, wire, "authorization: request editor access")
	assert.Contains(t, wire, "authorization: permission denied")
	assert.Contains(t, wire, "authorization: AWS4-HMAC-SHA256 missing")
	assert.Contains(t, wire, "authorization: AWS4-HMAC-SHA256.")
	assert.Contains(t, wire, "token: signature mismatch")
	assert.Contains(t, wire, "keep detail guidance")
}

func TestParseUpstreamErrorBody_PreservesBenignGuidanceAndURL(t *testing.T) {
	suggestions := []string{
		"search() runs across all fields and can be slow and expensive. Prefer a specific field, e.g. `<context>.<field_key>:<type>`",
		"body searches default to `body.message:string`. Use `body.<key>` to search a different field inside body",
		"telemetry selector must be <query_type>",
		"panel must reference <key>",
		"replace <placeholder> with a valid value",
	}
	body, err := json.Marshal(map[string]any{
		"status": "error",
		"error": map[string]any{
			"type":        "invalid-input",
			"code":        "invalid_input",
			"message":     "invalid token: signature mismatch; authorization: user lacks role editor. Token authentication failed. Bearer authentication is required. Bearer misconfiguration caused this error. session_id = 'x'; token = 'x'; secret = 'x'; cookie = 'x'",
			"url":         "https://signoz.io/docs/search?source=mcp#missing-keys",
			"suggestions": suggestions,
		},
	})
	require.NoError(t, err)

	got := ParseUpstreamErrorBody(string(body))
	require.True(t, got.Recognized)
	assert.Contains(t, got.Message, "Token authentication failed")
	assert.Contains(t, got.Message, "Bearer authentication is required")
	assert.Contains(t, got.Message, "Bearer misconfiguration caused this error")
	assert.Contains(t, got.Message, "session_id = 'x'")
	assert.Contains(t, got.Message, "token = 'x'; secret = 'x'; cookie = 'x'")
	assert.Equal(t, "https://signoz.io/docs/search?source=mcp#missing-keys", got.URL)
	assert.Equal(t, suggestions, got.Suggestions)
	assert.Empty(t, got.DriftFields)
}

func TestUpstreamErrorBody_ClientSafeTextBoundsAndPreservesEveryGuidanceSection(t *testing.T) {
	got := (UpstreamErrorBody{
		Message:     "summary-canary " + strings.Repeat("m", 5000),
		URL:         "https://signoz.io/docs/recovery-url-canary",
		Suggestions: []string{"top-suggestion-canary " + strings.Repeat("s", 5000)},
		Details: []UpstreamErrorDetail{{
			Message:     "detail context",
			Suggestions: []string{"detail-suggestion-canary " + strings.Repeat("d", 5000)},
		}},
		Retry: &UpstreamErrorRetry{Delay: int64(time.Second)},
	}).ClientSafeText()

	assert.LessOrEqual(t, len(got), 4*1024)
	assert.Contains(t, got, "...(truncated)")
	for _, want := range []string{
		"summary-canary",
		"recovery-url-canary",
		"top-suggestion-canary",
		"detail-suggestion-canary",
		"Retry delay: 1s",
	} {
		assert.Contains(t, got, want)
	}
}

func TestParseUpstreamErrorBody_RetryRequiresIntegerNanoseconds(t *testing.T) {
	tests := []struct {
		name      string
		delayJSON string
		wantDelay *int64
	}{
		{name: "integer", delayJSON: `5000000000`, wantDelay: int64Pointer(5_000_000_000)},
		{name: "zero", delayJSON: `0`, wantDelay: int64Pointer(0)},
		{name: "duration string", delayJSON: `"5s"`},
		{name: "decimal", delayJSON: `1.5`},
		{name: "exponent", delayJSON: `5e9`},
		{name: "negative", delayJSON: `-1`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseUpstreamErrorBody(`{"status":"error","error":{"code":"timeout","message":"slow","retry":{"delay":` + tc.delayJSON + `}}}`)
			require.True(t, got.Recognized)
			if tc.wantDelay == nil {
				assert.Nil(t, got.Retry)
				assert.Equal(t, []string{"retry"}, got.DriftFields)
				return
			}
			require.NotNil(t, got.Retry)
			assert.Equal(t, *tc.wantDelay, got.Retry.Delay)
			assert.Empty(t, got.DriftFields)
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }
