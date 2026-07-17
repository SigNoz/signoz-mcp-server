package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// Current-generation QB v5 envelope: bare summary in error.message, per-term
// detail in error.errors[].
const keyNotFoundEnvelopeBody = `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"Found 1 errors while parsing the search expression.","url":"https://signoz.io/docs/userguide/search-troubleshooting/","errors":[{"message":"key ` + "`service.name`" + ` not found","suggestions":[]}],"suggestions":[]}}`

// Older-generation envelope: the detail is inlined in a string error field.
const keyNotFoundLegacyBody = `{"status":"error","errorType":"invalid_input","error":"while parsing the search expression: key ` + "`service.name`" + ` not found"}`

func keyNotFound400(body string) *signozclient.HTTPStatusError {
	return &signozclient.HTTPStatusError{StatusCode: http.StatusBadRequest, Body: body}
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
			err: keyNotFound400("Found 3 errors while parsing the search expression: " +
				"key `service.name` not found; key `env` not found; key `service.name` not found"),
			want: []string{"service.name", "env"},
		},
		{
			name: "wrapped error chain still detected",
			err:  errWrap(keyNotFound400(keyNotFoundEnvelopeBody)),
			want: []string{"service.name"},
		},
		{
			name: "400 without the key-not-found wording",
			err:  keyNotFound400(`{"status":"error","error":{"code":"invalid_input","message":"bad step interval"}}`),
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
	got := missingFilterKeys(keyNotFound400(b.String()))
	if len(got) != missingFilterKeysLimit {
		t.Fatalf("len(missingFilterKeys) = %d, want cap %d", len(got), missingFilterKeysLimit)
	}
}

// TestMissingFilterKeys_DropsOversizedKeys pins the per-key length bound: the 400
// body is upstream-controlled, so an enormous captured "key" must not flow into
// guidance text or log attributes.
func TestMissingFilterKeys_DropsOversizedKeys(t *testing.T) {
	body := "key `" + strings.Repeat("x", missingFilterKeyMaxLen+1) + "` not found; key `service.name` not found"
	got := missingFilterKeys(keyNotFound400(body))
	if !reflect.DeepEqual(got, []string{"service.name"}) {
		t.Fatalf("missingFilterKeys = %#v, want oversized key dropped", got)
	}
	if only := missingFilterKeys(keyNotFound400("key `" + strings.Repeat("x", missingFilterKeyMaxLen+1) + "` not found")); only != nil {
		t.Fatalf("missingFilterKeys = %#v, want nil when every key is oversized", only)
	}
}

// TestMissingFilterKeys_OversizedBodyScanBounded pins the byte bound on the
// raw-body scan: the match cap alone would not stop FindAllStringSubmatch from
// walking an arbitrarily large 400 body, so only the first
// missingFilterKeyScanBytes are examined — a phrase beyond the window fails
// open, one inside it is still detected.
func TestMissingFilterKeys_OversizedBodyScanBounded(t *testing.T) {
	padding := strings.Repeat("x", missingFilterKeyScanBytes)
	if got := missingFilterKeys(keyNotFound400(padding + "key `service.name` not found")); got != nil {
		t.Fatalf("missingFilterKeys = %#v, want nil for a match beyond the scan window", got)
	}
	got := missingFilterKeys(keyNotFound400("key `service.name` not found" + padding))
	if !reflect.DeepEqual(got, []string{"service.name"}) {
		t.Fatalf("missingFilterKeys = %#v, want detection inside the scan window of an oversized body", got)
	}
}

func TestMissingKeyGuidance_PluralAgreement(t *testing.T) {
	if got := missingKeyGuidance([]string{"a", "b"}, "logs"); !strings.Contains(got, "which do not exist") {
		t.Fatalf("plural guidance = %q, want 'which do not exist'", got)
	}
	if got := missingKeyGuidance([]string{"a"}, "logs"); !strings.Contains(got, "which does not exist") {
		t.Fatalf("singular guidance = %q, want 'which does not exist'", got)
	}
}

func TestUpstreamQueryError_LogsGuidanceAndStructuredKeys(t *testing.T) {
	res := upstreamQueryError(keyNotFound400(keyNotFoundEnvelopeBody), "logs")

	text := resultText(t, res)
	if !strings.HasPrefix(text, "SigNoz API error: unexpected status 400: Found 1 errors while parsing the search expression. (key `service.name` not found)") {
		t.Fatalf("text should keep the upstream message with folded detail, got %q", text)
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
	err := keyNotFound400(`{"status":"error","error":{"code":"invalid_input","message":"bad step interval"}}`)

	got := upstreamQueryError(err, "logs")
	want := upstreamError(err)

	if resultText(t, got) != resultText(t, want) {
		t.Fatalf("text diverged without a missing key: %q vs %q", resultText(t, got), resultText(t, want))
	}
	if _, ok := resultStructuredMap(t, got)["missingKeys"]; ok {
		t.Fatalf("unexpected missingKeys without a key-not-found body")
	}
}

// TestUpstreamError_FoldsAdditionalErrorDetails pins the envelope-parsing fix:
// newer backends put per-term details in error.errors[] and keep error.message a
// bare summary, so the details must be folded into the surfaced message for every
// tool using upstreamError, not just the QB wrappers.
func TestUpstreamError_FoldsAdditionalErrorDetails(t *testing.T) {
	res := upstreamError(keyNotFound400(keyNotFoundEnvelopeBody))

	text := resultText(t, res)
	if !strings.Contains(text, "Found 1 errors while parsing the search expression. (key `service.name` not found)") {
		t.Fatalf("additional error detail not folded into text: %q", text)
	}
	structured := resultStructuredMap(t, res)
	if got, ok := structured["upstreamMessage"].(string); !ok || !strings.Contains(got, "key `service.name` not found") {
		t.Fatalf("upstreamMessage missing folded detail: %#v", structured["upstreamMessage"])
	}
}

func TestUpstreamError_AdditionalDetailsDedupAndCap(t *testing.T) {
	body := `{"status":"error","error":{"code":"invalid_input","message":"summary","errors":[` +
		`{"message":"summary"},{"message":""},{"message":"d1"},{"message":"d1"},{"message":"d2"},{"message":"d3"},{"message":"d4"},{"message":"d5"},{"message":"d6"}]}}`
	res := upstreamError(keyNotFound400(body))

	text := resultText(t, res)
	if !strings.Contains(text, "summary (d1; d2; d3; d4; d5)") {
		t.Fatalf("details not deduped/capped as expected: %q", text)
	}
	if strings.Contains(text, "d6") {
		t.Fatalf("detail cap exceeded: %q", text)
	}
}

// TestUpstreamError_OversizedDetailArraySkippedNotDecoded pins the input-size
// bound: a non-2xx body can be up to 64 MiB and json.RawMessage copies the field
// bytes during Unmarshal, so an error object beyond maxUpstreamErrorDetailsBytes
// never has errors[] in a decode target — details are dropped (fail open) while
// the independently parsed main fields survive.
func TestUpstreamError_OversizedDetailArraySkippedNotDecoded(t *testing.T) {
	huge := `[{"message":"` + strings.Repeat("x", maxUpstreamErrorDetailsBytes) + `"}]`
	body := `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"summary","errors":` + huge + `}}`

	res := upstreamError(keyNotFound400(body))

	text := resultText(t, res)
	if !strings.Contains(text, "summary") || strings.Contains(text, "summary (") {
		t.Fatalf("oversized details should be skipped, main message kept: %q", text[:min(len(text), 200)])
	}
	structured := resultStructuredMap(t, res)
	if got := structured["upstreamCode"]; got != "invalid_input" {
		t.Fatalf("upstreamCode = %v, want invalid_input preserved for oversized errors[]", got)
	}
}

// TestUpstreamError_AlternativeErrorsShapesKeepMainFields pins the fail-open
// contract of the errors[] decoding: a []string detail array still folds, and a
// detail shape we don't recognize is ignored WITHOUT discarding the independently
// parsed type/code/message (the pre-hardening struct decode failed wholesale).
func TestUpstreamError_AlternativeErrorsShapesKeepMainFields(t *testing.T) {
	stringDetails := `{"status":"error","error":{"code":"invalid_input","message":"summary","errors":["key ` + "`env`" + ` not found"]}}`
	res := upstreamError(keyNotFound400(stringDetails))
	if text := resultText(t, res); !strings.Contains(text, "summary (key `env` not found)") {
		t.Fatalf("[]string details not folded: %q", text)
	}

	malformed := `{"status":"error","error":{"type":"invalid-input","code":"invalid_input","message":"summary","errors":{"weird":"object"}}}`
	res = upstreamError(keyNotFound400(malformed))
	if text := resultText(t, res); !strings.Contains(text, "summary") {
		t.Fatalf("main message lost on malformed errors shape: %q", text)
	}
	structured := resultStructuredMap(t, res)
	if got := structured["upstreamCode"]; got != "invalid_input" {
		t.Fatalf("upstreamCode = %v, want invalid_input preserved despite malformed errors[]", got)
	}
	if got := structured["upstreamMessage"]; got != "summary" {
		t.Fatalf("upstreamMessage = %v, want summary preserved despite malformed errors[]", got)
	}
}

// TestQueryBuilderV5Handlers_KeyNotFoundGuidance pins, end to end through each
// handler, that every QueryBuilderV5 caller routes its error path through
// upstreamQueryError with its signal: the enriched guidance and the structured
// missingKeys field must survive the real handler plumbing, not just the helpers.
func TestQueryBuilderV5Handlers_KeyNotFoundGuidance(t *testing.T) {
	failing := &signozclient.MockClient{
		QueryBuilderV5Fn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return nil, keyNotFound400(keyNotFoundEnvelopeBody)
		},
	}
	h := newTestHandler(failing)

	var builderQuery map[string]any
	payloadJSON, err := json.Marshal(types.BuildLogsQueryPayload(1711123200000, 1711130400000, "service.name = 'checkout'", 10, 0))
	if err != nil {
		t.Fatalf("marshal builder payload: %v", err)
	}
	if err := json.Unmarshal(payloadJSON, &builderQuery); err != nil {
		t.Fatalf("unmarshal builder payload: %v", err)
	}

	cases := []struct {
		tool     string
		wantNoun string
		run      func() (*mcp.CallToolResult, error)
	}{
		{"signoz_search_logs", "this workspace's logs data", func() (*mcp.CallToolResult, error) {
			return h.handleSearchLogs(testCtx(), makeToolRequest("signoz_search_logs", map[string]any{"service": "checkout"}))
		}},
		{"signoz_aggregate_logs", "this workspace's logs data", func() (*mcp.CallToolResult, error) {
			return h.handleAggregateLogs(testCtx(), makeToolRequest("signoz_aggregate_logs", map[string]any{"aggregation": "count", "service": "checkout"}))
		}},
		{"signoz_search_traces", "this workspace's traces data", func() (*mcp.CallToolResult, error) {
			return h.handleSearchTraces(testCtx(), makeToolRequest("signoz_search_traces", map[string]any{"service": "checkout"}))
		}},
		{"signoz_aggregate_traces", "this workspace's traces data", func() (*mcp.CallToolResult, error) {
			return h.handleAggregateTraces(testCtx(), makeToolRequest("signoz_aggregate_traces", map[string]any{"aggregation": "count", "service": "checkout"}))
		}},
		{"signoz_query_metrics", "this workspace's metrics data", func() (*mcp.CallToolResult, error) {
			return h.handleQueryMetrics(testCtx(), makeToolRequest("signoz_query_metrics", map[string]any{"metricName": "system.cpu.time", "metricType": "gauge"}))
		}},
		{"signoz_execute_builder_query", "this workspace's data", func() (*mcp.CallToolResult, error) {
			return h.handleExecuteBuilderQuery(testCtx(), makeToolRequest("signoz_execute_builder_query", map[string]any{"query": builderQuery}))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := tc.run()
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected error result, got success: %v", result.Content)
			}
			text := resultText(t, result)
			if !strings.Contains(text, tc.wantNoun) {
				t.Fatalf("guidance noun %q missing in %q", tc.wantNoun, text)
			}
			if !strings.Contains(text, "signoz_get_field_keys") {
				t.Fatalf("recovery guidance missing in %q", text)
			}
			structured := resultStructuredMap(t, result)
			if got := structured["missingKeys"]; !reflect.DeepEqual(got, []string{"service.name"}) {
				t.Fatalf("missingKeys = %#v, want [service.name]", got)
			}
		})
	}
}

// TestLogQueryFailureLevels pins the severity contract of the QB tools' failure
// logger: key-not-found 400s are expected agent mistakes and log at WARN with the
// missing keys attached — still always emitted — while everything else keeps the
// shared logUpstreamFailure behavior.
func TestLogQueryFailureLevels(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantLevel       string
		wantMsg         string
		wantMissingKeys bool
	}{
		{
			name:            "missing filter key logs warn with keys",
			err:             keyNotFound400(keyNotFoundEnvelopeBody),
			wantLevel:       "WARN",
			wantMsg:         "Failed to search logs (filter references keys missing from workspace field metadata)",
			wantMissingKeys: true,
		},
		{
			name:      "other 400 stays error",
			err:       keyNotFound400(`{"status":"error","error":{"code":"invalid_input","message":"bad step interval"}}`),
			wantLevel: "ERROR",
			wantMsg:   "Failed to search logs",
		},
		{
			name:      "cancellation keeps debug demotion",
			err:       context.Canceled,
			wantLevel: "DEBUG",
			wantMsg:   "Failed to search logs (request cancelled by client)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &Handler{logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}

			h.logQueryFailure(context.Background(), "Failed to search logs", tc.err)

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("expected exactly one emitted log record, got %q: %v", buf.String(), err)
			}
			if rec["level"] != tc.wantLevel {
				t.Fatalf("level = %v, want %s", rec["level"], tc.wantLevel)
			}
			if rec["msg"] != tc.wantMsg {
				t.Fatalf("msg = %v, want %s", rec["msg"], tc.wantMsg)
			}
			if _, ok := rec["missingKeys"]; ok != tc.wantMissingKeys {
				t.Fatalf("missingKeys present = %v, want %v (record: %v)", ok, tc.wantMissingKeys, rec)
			}
		})
	}
}
