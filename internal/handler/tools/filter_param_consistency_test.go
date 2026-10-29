package tools

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

func TestParseFilterExpressionParam_CanonicalFilterContract(t *testing.T) {
	type parserCase struct {
		name  string
		parse func(map[string]any) (string, error)
		base  map[string]any
	}

	parsers := []parserCase{
		{
			name: "search_logs",
			parse: func(args map[string]any) (string, error) {
				req, err := parseSearchLogsArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"timeRange": "1h"},
		},
		{
			name: "search_traces",
			parse: func(args map[string]any) (string, error) {
				req, err := parseSearchTracesArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"timeRange": "1h"},
		},
		{
			name: "aggregate_logs",
			parse: func(args map[string]any) (string, error) {
				req, err := parseAggregateLogsArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"aggregations": "count()", "timeRange": "1h"},
		},
		{
			name: "aggregate_traces",
			parse: func(args map[string]any) (string, error) {
				req, err := parseAggregateTracesArgs(args)
				if err != nil {
					return "", err
				}
				return req.FilterExpression, nil
			},
			base: map[string]any{"aggregations": "count()", "timeRange": "1h"},
		},
		{
			name: "query_metrics",
			parse: func(args map[string]any) (string, error) {
				req, err := parseMetricsQueryArgs(args)
				if err != nil {
					return "", err
				}
				return req.Filter, nil
			},
			base: map[string]any{"metricName": "system.cpu.time"},
		},
	}

	t.Run("filter is canonical for every parser", func(t *testing.T) {
		for _, parser := range parsers {
			t.Run(parser.name, func(t *testing.T) {
				got, err := parser.parse(mergeArgs(parser.base, map[string]any{
					"filter": " service.name = 'checkout' ",
				}))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != " service.name = 'checkout' " {
					t.Fatalf("filter expression = %q, want raw filter bytes", got)
				}
			})
		}
	})

	// Every supplied query form is rejected at the parser layer
	// (TestReadLogFilterExpr); each query tool only has to route through it.
	t.Run("every query tool rejects the retired query alias", func(t *testing.T) {
		for parser, want := range map[*parserCase]string{
			&parsers[0]: logsLegacyQueryAliasError,
			&parsers[1]: tracesLegacyQueryAliasError,
			&parsers[2]: logsLegacyQueryAliasError,
			&parsers[3]: tracesLegacyQueryAliasError,
			&parsers[4]: metricsLegacyQueryAliasError,
		} {
			t.Run(parser.name, func(t *testing.T) {
				_, err := parser.parse(mergeArgs(parser.base, map[string]any{"query": "service.name = 'checkout'"}))
				if err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
			})
		}
	})
}

func TestExtractBackendWarningMessages(t *testing.T) {
	tests := []struct {
		name string
		body json.RawMessage
		want []string
	}{
		{
			name: "malformed body",
			body: json.RawMessage(`{not json`),
		},
		{
			name: "valid success without warning field",
			body: json.RawMessage(`{"status":"success","data":{"data":{"results":[]}}}`),
		},
		{
			name: "blank warning dropped",
			body: json.RawMessage(`{"status":"success","data":{"warning":{"warnings":[{"message":"   "},{"message":"keep me"}]}}}`),
			want: []string{"keep me"},
		},
		{
			name: "multiple warnings preserve order",
			body: json.RawMessage(`{"status":"success","data":{"warning":{"warnings":[{"message":"first"},{"message":"second"}]}}}`),
			want: []string{"first", "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBackendWarningMessages(tt.body)
			if tt.want == nil && got != nil {
				t.Fatalf("warnings = %#v, want nil", got)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("warning count = %d (%#v), want %d (%#v)", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("warning[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// The raw JSON payload is always the first, independently parseable content
// block; the clamp note appears only when clamped and is never prepended into
// the JSON.
func TestRawSearchResult_KeepsRawJSONFirstAndAppendsNoteBlocks(t *testing.T) {
	payload := []byte(`{"status":"success","data":{"warning":{"warnings":[{"message":"ambiguous key"}]},"data":{"results":[]}}}`)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	unclamped := rawSearchResult(testCtx(), logger, "signoz_search_logs", payload, 100, 0, false)
	if len(unclamped.Content) != 3 {
		t.Fatalf("unclamped content block count = %d, want raw JSON + completeness note + warning note", len(unclamped.Content))
	}
	if note := noteText(t, unclamped, 1); strings.Contains(note, "result limited to") {
		t.Fatalf("unclamped block 1 = %q, want no clamp note", note)
	}

	result := rawSearchResult(testCtx(), logger, "signoz_search_logs", payload, 100, 0, true)
	if len(result.Content) != 4 {
		t.Fatalf("content block count = %d, want raw JSON + clamp note + completeness note + warning note", len(result.Content))
	}
	if block0 := noteText(t, result, 0); block0 != string(payload) {
		t.Fatalf("block 0 = %q, want raw response unchanged", block0)
	}
	if clampNote := noteText(t, result, 1); !strings.Contains(clampNote, "result limited to") {
		t.Fatalf("block 1 = %q, want clamp note", clampNote)
	}
	if completenessNote := noteText(t, result, 2); !strings.Contains(completenessNote, "hasMore") {
		t.Fatalf("block 2 = %q, want completeness note", completenessNote)
	}
	if warningNote := noteText(t, result, 3); !strings.Contains(warningNote, "ambiguous key") {
		t.Fatalf("block 3 = %q, want warning note", warningNote)
	}
}

func mergeArgs(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

func noteText(t *testing.T, result *mcp.CallToolResult, index int) string {
	t.Helper()
	if len(result.Content) <= index {
		t.Fatalf("result has %d content blocks, want index %d", len(result.Content), index)
	}
	text, ok := mcp.AsTextContent(result.Content[index])
	if !ok {
		t.Fatalf("content block %d is %T, want text", index, result.Content[index])
	}
	return text.Text
}
