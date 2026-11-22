package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// rowSearchTools are the tools that page their rows by result size.
var rowSearchTools = []string{"signoz_search_logs", "signoz_search_traces"}

// budgetHarness registers every tool against an upstream that answers each
// query_range call with body. serverDefault is the operator's
// MCP_MAX_RESULT_BYTES (0 keeps the built-in default).
type budgetHarness struct {
	server *mcp.Server
	logs   *bytes.Buffer
	reader *sdkmetric.ManualReader
}

func newBudgetHarness(t *testing.T, body string, serverDefault int) budgetHarness {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	reader := sdkmetric.NewManualReader()
	meters, err := otelpkg.NewMeters(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)
	logs := &bytes.Buffer{}
	h := &Handler{
		logger:         slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		config:         &config.Config{MaxResultBytes: serverDefault},
		meters:         meters,
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	return budgetHarness{server: server, logs: logs, reader: reader}
}

// budgetEvents returns mcp.tool.result.budget.events by event for one tool.
func (b budgetHarness) budgetEvents(t *testing.T, tool string) map[string]int64 {
	t.Helper()
	var metrics metricdata.ResourceMetrics
	require.NoError(t, b.reader.Collect(context.Background(), &metrics))
	events := map[string]int64{}
	sum, _ := oteltest.FindInt64SumMetric(metrics, "mcp.tool.result.budget.events")
	for _, point := range sum.DataPoints {
		if name, _ := point.Attributes.Value(otelpkg.GenAIToolNameKey); name.AsString() != tool {
			continue
		}
		event, _ := point.Attributes.Value(attribute.Key(otelpkg.MCPToolResultBudgetEventKey))
		events[event.AsString()] += point.Value
	}
	return events
}

// queryRangeBody wraps rows in a raw query_range response whose single result
// carries a nextCursor.
func queryRangeBody(rows []string) string {
	return `{"status":"success","data":{"type":"raw","meta":{"rowsScanned":42},"data":{"results":[{"queryName":"A","nextCursor":"cursor-past-the-page","rows":[` +
		strings.Join(rows, ",") + `]}]}}}`
}

// sampleRows returns count rows in the tool's row shape, each carrying an
// integer above 2^53 and a body of bodyBytes bytes.
func sampleRows(tool string, count, bodyBytes int) []string {
	rows := make([]string, count)
	for i := range rows {
		body := fmt.Sprintf("row %03d %s", i, strings.Repeat("y", max(0, bodyBytes-8)))
		if tool == "signoz_search_traces" {
			rows[i] = fmt.Sprintf(`{"timestamp":"2026-11-21T10:00:%02dZ","data":{"trace_id":"trace-%03d","span_id":"span-%03d","duration_nano":9007199254740993,"name":%q}}`, i%60, i, i, body)
		} else {
			rows[i] = fmt.Sprintf(`{"timestamp":"2026-11-21T10:00:%02dZ","data":{"id":"log-%03d","body":%q,"attributes_number":{"bytes":9007199254740993}}}`, i%60, i, body)
		}
	}
	return rows
}

// deliveredPage is block 0 of a raw search result: its rows, kept as sent,
// and the server's pagination object.
type deliveredPage struct {
	rows           []json.RawMessage
	cursor         *string
	pagination     map[string]any
	paginationJSON string
}

func readDeliveredPage(t *testing.T, result *mcp.CallToolResult) deliveredPage {
	t.Helper()
	var body struct {
		Data struct {
			Data struct {
				Results []struct {
					NextCursor *string           `json:"nextCursor"`
					Rows       []json.RawMessage `json:"rows"`
				} `json:"results"`
			} `json:"data"`
		} `json:"data"`
		Pagination json.RawMessage `json:"pagination"`
	}
	block0 := textContent(t, result)
	require.NoError(t, json.Unmarshal([]byte(block0), &body), "block 0 must stay valid JSON: %s", block0)
	require.Len(t, body.Data.Data.Results, 1, block0)
	page := deliveredPage{rows: body.Data.Data.Results[0].Rows, cursor: body.Data.Data.Results[0].NextCursor, paginationJSON: string(body.Pagination)}
	require.NoError(t, json.Unmarshal(body.Pagination, &page.pagination), "block 0 has no pagination object: %s", block0)
	return page
}

// The rows of a result that fits come back as SigNoz sent them; block 0 only
// gains the pagination object, whose hasMore keeps the row-limit inference.
func TestRowSearchWithinBudgetReturnsTheBodyWithPagination(t *testing.T) {
	for _, tool := range rowSearchTools {
		body := queryRangeBody(sampleRows(tool, 3, 40))
		harness := newBudgetHarness(t, body, 0)
		for _, tc := range []struct {
			limit      int
			pagination string
		}{
			{limit: 3, pagination: `{"returned":3,"limit":3,"offset":10,"hasMore":true,"nextOffset":13,"truncated":false}`},
			{limit: 20, pagination: `{"returned":3,"limit":20,"offset":10,"hasMore":false,"nextOffset":-1,"truncated":false}`},
		} {
			t.Run(fmt.Sprintf("%s limit %d", tool, tc.limit), func(t *testing.T) {
				result := callRegisteredTool(t, harness.server, tool, fmt.Sprintf(`{"limit":%d,"offset":10}`, tc.limit))
				require.False(t, result.IsError, allTextBlocks(result))

				want := strings.TrimSuffix(body, "}") + `,"pagination":` + tc.pagination + `}`
				require.Equal(t, want, textContent(t, result), "block 0 must be SigNoz's body byte for byte plus pagination")
				require.Empty(t, harness.budgetEvents(t, tool), "a result that fits records no budget event")
			})
		}
	}
}

// Over budget, the page keeps the leading rows that fit, byte for byte, and
// says where the next page starts. The server default applies without
// maxResultBytes; the upstream cursor goes, since it points past dropped rows.
func TestRowSearchOverBudgetEndsThePageOnAWholeRow(t *testing.T) {
	for _, tool := range rowSearchTools {
		rows := sampleRows(tool, 20, 400)
		for _, tc := range []struct {
			name          string
			serverDefault int
			arguments     string
			budget        int
		}{
			{name: "per-call budget", arguments: `{"limit":20,"offset":40,"maxResultBytes":4000}`, budget: 4000},
			{name: "server default", serverDefault: 3000, arguments: `{"limit":20,"offset":40}`, budget: 3000},
		} {
			t.Run(tool+" "+tc.name, func(t *testing.T) {
				harness := newBudgetHarness(t, queryRangeBody(rows), tc.serverDefault)
				result := callRegisteredTool(t, harness.server, tool, tc.arguments)
				require.False(t, result.IsError, allTextBlocks(result))

				delivered := textBytes(result)
				require.LessOrEqual(t, delivered, tc.budget, "the result's text blocks must fit the budget: %s", allTextBlocks(result))
				page := readDeliveredPage(t, result)
				returned := len(page.rows)
				require.True(t, returned >= 1 && returned < len(rows), "want a partial page, got %d of %d rows", returned, len(rows))
				for i, row := range page.rows {
					require.Equal(t, rows[i], string(row), "row %d must be the upstream row byte for byte", i)
				}
				require.Greater(t, delivered+len(rows[returned])+1, tc.budget, "row %d would have fit; the page must keep every leading row that fits", returned)
				require.JSONEq(t, fmt.Sprintf(`{"returned":%d,"limit":20,"offset":40,"hasMore":true,"nextOffset":%d,"truncated":true,"maxResultBytes":%d}`, returned, 40+returned, tc.budget), page.paginationJSON)
				require.Nil(t, page.cursor, "nextCursor points past rows this page dropped and must be removed")
				require.Contains(t, textContent(t, result), `"meta":{"rowsScanned":42}`, "the rest of the body stays")

				notes := allTextBlocks(result)
				require.Contains(t, notes, fmt.Sprintf("result size budget of %d bytes ended this page", tc.budget))
				require.Contains(t, notes, fmt.Sprintf("offset=%d, and pass the start and end from the window note", 40+returned))
				require.Contains(t, notes, "selectFields")
				require.NotContains(t, strings.Join(trailingTextBlocks(result), "\n"), "maxResultBytes", "a cut note must not point at the override: a model cannot know what its client can show")
				require.Equal(t, map[string]int64{budgetEventRowsDropped: 1}, harness.budgetEvents(t, tool))
			})
		}
	}
}

// A first row larger than the budget still comes back, with its long strings
// shortened and marked, because a page with no rows and hasMore would loop.
// Short strings, numbers, and webUrl links keep their bytes.
func TestRowSearchShortensAFirstRowThatAloneIsOverBudget(t *testing.T) {
	const bodyRunes = 50000
	bigBody := strings.Repeat("é", bodyRunes)
	// The link is longer than the strings get cut to, so only its key keeps it whole.
	link := "https://signoz.example.com/trace/" + strings.Repeat("a", 1000)
	bigRow := func(id string) string {
		return `{"timestamp":"2026-11-21T10:00:00Z","data":{"id":"` + id + `","body":"` + bigBody + `","attributes_number":{"bytes":9007199254740993},"webUrl":"` + link + `"}}`
	}
	for _, tool := range rowSearchTools {
		for _, tc := range []struct {
			name       string
			rows       []string
			pagination string
		}{
			{name: "only row", rows: []string{bigRow("log-1")},
				pagination: `{"returned":1,"limit":20,"offset":0,"hasMore":false,"nextOffset":-1,"truncated":false,"maxResultBytes":3000,"shortenedRows":1}`},
			{name: "first of two", rows: []string{bigRow("log-1"), bigRow("log-2")},
				pagination: `{"returned":1,"limit":20,"offset":0,"hasMore":true,"nextOffset":1,"truncated":true,"maxResultBytes":3000,"shortenedRows":1}`},
		} {
			t.Run(tool+" "+tc.name, func(t *testing.T) {
				harness := newBudgetHarness(t, queryRangeBody(tc.rows), 0)
				result := callRegisteredTool(t, harness.server, tool, `{"limit":20,"maxResultBytes":3000}`)
				require.False(t, result.IsError, allTextBlocks(result))

				require.LessOrEqual(t, textBytes(result), 3000, allTextBlocks(result))
				page := readDeliveredPage(t, result)
				require.Len(t, page.rows, 1)
				require.JSONEq(t, tc.pagination, page.paginationJSON)
				row := string(page.rows[0])
				require.Contains(t, row, `"id":"log-1"`)
				require.Contains(t, row, `"bytes":9007199254740993`, "numbers keep their exact digits")
				require.Contains(t, row, `"webUrl":"`+link+`"`, "a shortened link would be broken")

				var decoded struct {
					Data struct {
						Body string `json:"body"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(page.rows[0], &decoded))
				kept, removed, ok := strings.Cut(decoded.Data.Body, "…(+")
				require.True(t, ok, "the shortened body must end in …(+N chars): %.80q", decoded.Data.Body)
				require.Equal(t, strings.Repeat("é", utf8.RuneCountInString(kept)), kept, "the kept text is the body's prefix")
				require.Equal(t, fmt.Sprintf("%d chars)", bodyRunes-utf8.RuneCountInString(kept)), removed, "N counts the characters removed")
				require.Contains(t, allTextBlocks(result), "its long string values were shortened")
				events := harness.budgetEvents(t, tool)
				require.Equal(t, int64(1), events[budgetEventRowShortened])
				require.Zero(t, events[budgetEventOverBudget])
			})
		}
	}
}

// When even the shortened first row is over budget, it still comes back, and
// the miss is logged and counted rather than silent.
func TestRowSearchReturnsAnOverBudgetRowWithAWarning(t *testing.T) {
	fields := make([]string, 60)
	for i := range fields {
		fields[i] = fmt.Sprintf(`"field_%02d":"%s"`, i, strings.Repeat("z", 200))
	}
	row := `{"timestamp":"2026-11-21T10:00:00Z","data":{` + strings.Join(fields, ",") + `}}`
	for _, tool := range rowSearchTools {
		t.Run(tool, func(t *testing.T) {
			harness := newBudgetHarness(t, queryRangeBody([]string{row}), 0)
			result := callRegisteredTool(t, harness.server, tool, `{"maxResultBytes":1000}`)
			require.False(t, result.IsError, allTextBlocks(result))

			page := readDeliveredPage(t, result)
			require.Len(t, page.rows, 1, "one row always comes back")
			require.Contains(t, string(page.rows[0]), `"field_59":"…(+200 chars)"`, "long strings are cut as far as they go")
			require.Contains(t, string(page.rows[0]), `"timestamp":"2026-11-21T10:00:00Z"`, "timestamps and IDs stay whole")
			require.Greater(t, textBytes(result), 1000)
			require.Contains(t, harness.logs.String(), "tool result is over its size budget after fitting rows")
			require.Equal(t, int64(1), harness.budgetEvents(t, tool)[budgetEventOverBudget])
		})
	}
}

// A maxResultBytes outside [1000, 500000] is clamped, and the result says so in
// pagination.maxResultBytes and a note. A server default above 500000 raises
// the ceiling to itself.
func TestRowSearchClampsMaxResultBytesAndSaysSo(t *testing.T) {
	for _, tool := range rowSearchTools {
		for _, tc := range []struct {
			serverDefault int
			arguments     string
			applied       int
			note          string
		}{
			{arguments: `{"maxResultBytes":"10"}`, applied: 1000, note: "note: maxResultBytes 10 is below the minimum of 1000, so this result used 1000."},
			{arguments: `{"maxResultBytes":9999999}`, applied: 500000, note: "note: maxResultBytes 9999999 is above the maximum of 500000, so this result used 500000."},
			{serverDefault: 600000, arguments: `{"maxResultBytes":700000}`, applied: 600000, note: "note: maxResultBytes 700000 is above the maximum of 600000, so this result used 600000."},
		} {
			t.Run(tool+" "+tc.arguments, func(t *testing.T) {
				harness := newBudgetHarness(t, queryRangeBody(sampleRows(tool, 1, 40)), tc.serverDefault)
				result := callRegisteredTool(t, harness.server, tool, tc.arguments)
				require.False(t, result.IsError, allTextBlocks(result))

				page := readDeliveredPage(t, result)
				require.Equal(t, float64(tc.applied), page.pagination["maxResultBytes"], page.paginationJSON)
				require.Equal(t, false, page.pagination["truncated"], page.paginationJSON)
				require.Contains(t, trailingTextBlocks(result), tc.note)
			})
		}
	}
}

// The validation decorator's notices count toward the budget: the page is
// cut with them in place, not before they are appended.
func TestRowSearchBudgetCountsDecoratorNotices(t *testing.T) {
	var stray []string
	for i := range 10 {
		stray = append(stray, fmt.Sprintf(`"unknown_argument_with_a_long_name_%02d":"x"`, i))
	}
	for _, tool := range rowSearchTools {
		t.Run(tool, func(t *testing.T) {
			rows := sampleRows(tool, 20, 400)
			harness := newBudgetHarness(t, queryRangeBody(rows), 0)
			result := callRegisteredTool(t, harness.server, tool, `{"maxResultBytes":4000,`+strings.Join(stray, ",")+`}`)
			require.False(t, result.IsError, allTextBlocks(result))

			notes := trailingTextBlocks(result)
			require.Contains(t, notes[len(notes)-1], "this tool ignored the arguments", "the ignored-arguments notice is the last block")
			require.LessOrEqual(t, textBytes(result), 4000, "the delivered result, notices included, must fit: %s", allTextBlocks(result))
			page := readDeliveredPage(t, result)
			for i, row := range page.rows {
				require.Equal(t, rows[i], string(row))
			}
			require.Equal(t, float64(len(page.rows)), page.pagination["returned"])
		})
	}
}

// Rows are cut after enrichment, so each kept span row carries its webUrl and
// the budget counts it.
func TestSearchTracesBudgetCountsEnrichedWebURLs(t *testing.T) {
	rows := sampleRows("signoz_search_traces", 20, 300)
	h, _ := queryRangeRecorder(t, queryRangeBody(rows))
	ctx := util.SetSigNozURL(testCtx(), "https://signoz.example.com")
	result, err := h.handleSearchTraces(ctx, makeToolRequest("signoz_search_traces", map[string]any{"maxResultBytes": 3000}))
	require.NoError(t, err)
	require.False(t, result.IsError, allTextBlocks(result))

	require.LessOrEqual(t, textBytes(result), 3000)
	page := readDeliveredPage(t, result)
	require.NotEmpty(t, page.rows)
	for i, row := range page.rows {
		require.Contains(t, string(row), fmt.Sprintf(`"webUrl":"https://signoz.example.com/trace/trace-%03d`, i))
	}
}

// A body the budget cannot page, here one that already has a top-level
// pagination field, comes back unchanged with the inferred completeness note
// and a WARN, rather than with a guessed cut.
func TestRowSearchFailsOpenOnABodyItCannotPage(t *testing.T) {
	const body = `{"status":"success","pagination":{"cursor":"upstream"},"data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[{"data":{"body":"a"}},{"data":{"body":"b"}}]}]}}}`
	for _, tool := range rowSearchTools {
		t.Run(tool, func(t *testing.T) {
			harness := newBudgetHarness(t, body, 0)
			result := callRegisteredTool(t, harness.server, tool, `{"limit":20}`)
			require.False(t, result.IsError, allTextBlocks(result))

			require.Equal(t, body, textContent(t, result))
			require.Contains(t, trailingTextBlocks(result), "note: returned 2 rows (limit 20) — all matching results returned (hasMore=false).")
			require.Contains(t, harness.logs.String(), "raw search result left unbounded because its rows could not be paged")
			require.Equal(t, map[string]int64{budgetEventFailOpen: 1}, harness.budgetEvents(t, tool))
		})
	}
}

// A budget that a page fills exactly returns that page: no row that fits is
// dropped, and a row that fits whole is not shortened.
func TestRowSearchFillsABudgetItFitsExactly(t *testing.T) {
	for _, tool := range rowSearchTools {
		rows := sampleRows(tool, 20, 400)
		harness := newBudgetHarness(t, queryRangeBody(rows), 0)
		for _, budget := range []int{1500, 2500, 4000} {
			t.Run(fmt.Sprintf("%s near %d", tool, budget), func(t *testing.T) {
				call := func(maxBytes int) *mcp.CallToolResult {
					// A fixed window keeps the window note, and so the page's size, the same across calls.
					result := callRegisteredTool(t, harness.server, tool, fmt.Sprintf(`{"limit":20,"start":1795000000000,"end":1795003600000,"maxResultBytes":%d}`, maxBytes))
					require.False(t, result.IsError, allTextBlocks(result))
					return result
				}
				loose := call(budget)
				exact := textBytes(loose)
				require.Len(t, fmt.Sprint(exact), len(fmt.Sprint(budget)), "the two budgets must print at the same width for the sizes to match")

				tight := call(exact)
				require.Equal(t, exact, textBytes(tight), "a budget equal to the page's size returns the same page")
				require.Len(t, readDeliveredPage(t, tight).rows, len(readDeliveredPage(t, loose).rows))
				require.NotContains(t, readDeliveredPage(t, tight).pagination, "shortenedRows", "a row that fits whole is not shortened")
			})
		}
	}
}

// A small budget shortens long strings as far as it needs, rather than
// stopping at a fixed length and going over.
func TestRowSearchShortensLongStringsUntilTheRowFits(t *testing.T) {
	emoji := strings.Repeat("😀", 200)
	row := fmt.Sprintf(`{"timestamp":"2026-11-21T10:00:00Z","data":{"a":%q,"b":%q,"c":%q,"d":%q}}`, emoji, emoji, emoji, emoji)
	for _, tool := range rowSearchTools {
		t.Run(tool, func(t *testing.T) {
			harness := newBudgetHarness(t, queryRangeBody([]string{row}), 0)
			result := callRegisteredTool(t, harness.server, tool, `{"maxResultBytes":1000}`)
			require.False(t, result.IsError, allTextBlocks(result))

			require.LessOrEqual(t, textBytes(result), 1000, allTextBlocks(result))
			require.Len(t, readDeliveredPage(t, result).rows, 1)
			require.Zero(t, harness.budgetEvents(t, tool)[budgetEventOverBudget])
		})
	}
}

// selectFields does not apply to trace rows, so their budget notes do not
// suggest it.
func TestSearchTracesBudgetNotesSkipSelectFieldsForTraceRows(t *testing.T) {
	harness := newBudgetHarness(t, queryRangeBody(sampleRows("signoz_search_traces", 20, 400)), 0)
	result := callRegisteredTool(t, harness.server, "signoz_search_traces", `{"requestType":"trace","maxResultBytes":4000}`)
	require.False(t, result.IsError, allTextBlocks(result))

	notes := strings.Join(trailingTextBlocks(result), "\n")
	require.Contains(t, notes, "result size budget of 4000 bytes ended this page")
	require.NotContains(t, notes, "selectFields")
}

// A body the budget cannot page is left whole and counted: two results, which
// one offset cannot continue, and an empty object, whose notes alone can be
// over a small budget.
func TestRowSearchCountsEveryBodyItLeavesWhole(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","data":{"type":"raw","data":{"results":[{"queryName":"A","rows":[{"data":{"body":"a"}}]},{"queryName":"B","rows":[{"data":{"body":"b"}}]}]}}}`,
		`{}`,
	} {
		for _, tool := range rowSearchTools {
			t.Run(tool+" "+body, func(t *testing.T) {
				harness := newBudgetHarness(t, body, 0)
				result := callRegisteredTool(t, harness.server, tool, `{"limit":20}`)
				require.False(t, result.IsError, allTextBlocks(result))

				require.Equal(t, body, textContent(t, result))
				require.Equal(t, map[string]int64{budgetEventFailOpen: 1}, harness.budgetEvents(t, tool))
			})
		}
	}
}
