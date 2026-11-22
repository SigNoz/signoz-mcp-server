package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
)

// listBudget is the server default the list budget tests run under.
const listBudget = 4000

// budgetedListTool is a list tool whose page the result size budget bounds,
// with how SigNoz sends its items. Item i carries the marker item-NNN in a
// padded string, so a delivered page shows which items it kept.
type budgetedListTool struct {
	tool string
	// path is the upstream route; paged is true when SigNoz pages the list
	// itself, so the stub honors the limit and offset it is sent.
	path  string
	paged bool
	// arguments are the tool's own arguments besides limit and offset.
	arguments string
	item      func(i int, padding string) string
	body      func(items []string, total int) string
	// itemsAt is the dotted path to the items in block 0.
	itemsAt string
	// windowed tools report the window they queried, which the next page repeats.
	windowed bool
}

var budgetedListTools = []budgetedListTool{
	{
		tool: "signoz_list_services", path: "/api/v2/services", itemsAt: "data", windowed: true,
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"serviceName":"svc-%03d","p99":9007199254740993,"callRate":1.5,"errorRate":0,"tag":"item-%03d %s"}`, i, i, padding)
		},
		body: func(items []string, _ int) string {
			return `{"status":"success","data":[` + strings.Join(items, ",") + `]}`
		},
	},
	{
		tool: "signoz_get_service_map", path: "/api/v1/dependency_graph", itemsAt: "data", windowed: true,
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"parent":"svc-%03d","child":"redis","callCount":3,"p99":9007199254740993,"tag":"item-%03d %s"}`, i, i, padding)
		},
		body: func(items []string, _ int) string { return `[` + strings.Join(items, ",") + `]` },
	},
	{
		tool: "signoz_list_alerts", path: "/api/v1/alerts", itemsAt: "data",
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"labels":{"alertname":"item-%03d %s","ruleId":"rule-%03d","severity":"critical"},"status":{"state":"active"},"startsAt":"2026-11-22T10:00:00Z","endsAt":"2026-11-22T11:00:00Z"}`, i, padding, i)
		},
		body: func(items []string, _ int) string {
			return `{"status":"success","data":[` + strings.Join(items, ",") + `]}`
		},
	},
	{
		tool: "signoz_list_alert_rules", path: "/api/v3/rules", paged: true, itemsAt: "data",
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"id":"rule-%03d","alert":"Rule %03d","alertType":"METRIC_BASED_ALERT","ruleType":"threshold_rule","state":"inactive","description":"item-%03d %s"}`, i, i, i, padding)
		},
		body: func(items []string, total int) string {
			return fmt.Sprintf(`{"status":"success","data":{"rules":[%s],"total":%d}}`, strings.Join(items, ","), total)
		},
	},
	{
		tool: "signoz_list_notification_channels", path: "/api/v2/notification_channels", paged: true, itemsAt: "channels",
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"id":"019947a7-f200-7000-8000-%012d","name":"channel-%03d","displayName":"item-%03d %s","kind":"email","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}`, i, i, i, padding)
		},
		body: func(items []string, total int) string {
			return fmt.Sprintf(`{"status":"success","data":{"channels":[%s],"total":%d}}`, strings.Join(items, ","), total)
		},
	},
	{
		tool: "signoz_list_dashboards", path: "/api/v2/dashboards", paged: true, itemsAt: "data.dashboards",
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"id":"dash-%03d","name":"Dashboard %03d","description":"item-%03d %s","tags":[],"source":"user"}`, i, i, i, padding)
		},
		body: func(items []string, total int) string {
			return fmt.Sprintf(`{"status":"success","data":{"dashboards":[%s],"reservedKeywords":["name"],"tags":[],"total":%d}}`, strings.Join(items, ","), total)
		},
	},
}

// items returns count items of the tool, each padded by paddingBytes.
func (l budgetedListTool) items(count, paddingBytes int) []string {
	items := make([]string, count)
	for i := range items {
		items[i] = l.item(i, strings.Repeat("p", paddingBytes))
	}
	return items
}

// call runs the registered tool with limit, offset, and extra arguments.
func (l budgetedListTool) call(t *testing.T, server *mcp.Server, limit, offset int, extra ...string) *mcp.CallToolResult {
	t.Helper()
	arguments := []string{fmt.Sprintf(`"limit":%d,"offset":%d`, limit, offset)}
	if l.arguments != "" {
		arguments = append(arguments, l.arguments)
	}
	arguments = append(arguments, extra...)
	result := callRegisteredTool(t, server, l.tool, "{"+strings.Join(arguments, ",")+"}")
	require.False(t, result.IsError, allTextBlocks(result))
	return result
}

// newListBudgetHarness registers every tool against a SigNoz stub that
// answers the tool's route with its items; the server budget is listBudget.
func newListBudgetHarness(t *testing.T, list budgetedListTool, items []string) budgetHarness {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != list.path {
			http.NotFound(w, r)
			return
		}
		page := items
		if list.paged {
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			page = items[min(offset, len(items)):min(offset+limit, len(items))]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(list.body(page, len(items))))
	}))
	t.Cleanup(upstream.Close)
	reader := sdkmetric.NewManualReader()
	meters, err := otelpkg.NewMeters(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)
	logs := &bytes.Buffer{}
	h := &Handler{
		logger:         slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		config:         &config.Config{MaxResultBytes: listBudget},
		meters:         meters,
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	return budgetHarness{server: server, logs: logs, reader: reader}
}

// deliveredList is block 0 of a list result: its items as delivered, its
// pagination object, and the whole decoded object.
type deliveredList struct {
	items          []json.RawMessage
	paginationJSON string
	object         map[string]json.RawMessage
}

func readDeliveredList(t *testing.T, result *mcp.CallToolResult, itemsAt string) deliveredList {
	t.Helper()
	block0 := textContent(t, result)
	var list deliveredList
	require.NoError(t, json.Unmarshal([]byte(block0), &list.object), "block 0 must stay a JSON object: %.300s", block0)
	list.paginationJSON = string(list.object[paginationKey])
	holder := list.object
	keys := strings.Split(itemsAt, ".")
	for _, key := range keys[:len(keys)-1] {
		require.NoError(t, json.Unmarshal(holder[key], &holder), "block 0 has no %s object: %.300s", key, block0)
	}
	require.NoError(t, json.Unmarshal(holder[keys[len(keys)-1]], &list.items), "block 0 has no %s array: %.300s", itemsAt, block0)
	return list
}

// cutPagination is the pagination object a list tool reports after the budget
// ended its page at offset after returned of total items, with limit 20.
func (l budgetedListTool) cutPagination(offset, returned, total int) string {
	if l.tool == "signoz_list_notification_channels" {
		return fmt.Sprintf(`{"count":%d,"hasMore":true,"limit":20,"nextOffset":%d,"offset":%d,"truncated":true,"maxResultBytes":%d}`, returned, offset+returned, offset, listBudget)
	}
	return fmt.Sprintf(`{"total":%d,"offset":%d,"limit":20,"hasMore":true,"nextOffset":%d,"truncated":true,"maxResultBytes":%d}`, total, offset, offset+returned, listBudget)
}

// unorderedListTools page with offset over a list SigNoz returns in no fixed
// order, so the budget does not cut them.
var unorderedListTools = []budgetedListTool{
	{
		tool: "signoz_list_views", path: "/api/v2/saved_views", itemsAt: "data",
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"id":"view-%03d","name":"item-%03d %s","sourcePage":"logs"}`, i, i, padding)
		},
		body: func(items []string, _ int) string {
			return `{"status":"success","data":[` + strings.Join(items, ",") + `]}`
		},
	},
	{
		tool: "signoz_get_ai_pricing", path: "/api/v1/llm_pricing_rules/unmapped_models", itemsAt: "data", arguments: `"view":"unpriced_models"`,
		item: func(i int, padding string) string {
			return fmt.Sprintf(`{"modelName":"item-%03d %s","provider":"openai","spanCount":9007199254740993}`, i, padding)
		},
		body: func(items []string, _ int) string {
			return `{"status":"success","data":{"items":[` + strings.Join(items, ",") + `]}}`
		},
	},
}

// SigNoz returns saved views and AI pricing items in no fixed order. A cut
// would make the caller continue from an offset, where an item that moved
// between the two calls comes back twice or not at all, so a page over the
// budget is returned whole, as before.
func TestListToolsWithoutAFixedOrderReturnAnOverBudgetPageWhole(t *testing.T) {
	for _, list := range unorderedListTools {
		t.Run(list.tool, func(t *testing.T) {
			harness := newListBudgetHarness(t, list, list.items(40, 400))
			result := list.call(t, harness.server, 20, 10)

			require.Greater(t, textBytes(result), listBudget, "the page must be over the budget for this test to mean anything")
			delivered := readDeliveredList(t, result, list.itemsAt)
			require.Len(t, delivered.items, 20)
			require.JSONEq(t, `{"total":40,"offset":10,"limit":20,"hasMore":true,"nextOffset":30}`, delivered.paginationJSON)
			require.NotContains(t, allTextBlocks(result), "result size budget")
			require.Empty(t, harness.budgetEvents(t, list.tool), "an uncut page records no budget event")
		})
	}
}

// A page that fits the budget is returned as before: its pagination object
// has no budget fields, no note mentions the budget, and SigNoz's dashboard
// list is its body byte for byte, with no pagination object added.
func TestListToolWithinBudgetReturnsThePageAsBuilt(t *testing.T) {
	for _, list := range budgetedListTools {
		t.Run(list.tool, func(t *testing.T) {
			items := list.items(3, 20)
			harness := newListBudgetHarness(t, list, items)
			result := list.call(t, harness.server, 20, 0)

			delivered := readDeliveredList(t, result, list.itemsAt)
			require.Len(t, delivered.items, 3)
			switch list.tool {
			case "signoz_list_dashboards":
				require.Equal(t, list.body(items, 3), textContent(t, result), "a dashboard page that fits is SigNoz's body byte for byte")
			case "signoz_list_notification_channels":
				require.JSONEq(t, `{"count":3,"hasMore":false,"limit":20,"nextOffset":null,"offset":0}`, delivered.paginationJSON)
			default:
				require.JSONEq(t, `{"total":3,"offset":0,"limit":20,"hasMore":false,"nextOffset":-1}`, delivered.paginationJSON)
			}
			require.NotContains(t, allTextBlocks(result), "result size budget")
			require.Empty(t, harness.budgetEvents(t, list.tool), "a result that fits records no budget event")
		})
	}
}

// Over budget, a list page keeps its leading whole items that fit and says
// where the next page starts, in pagination and in a note; total stays.
func TestListToolOverBudgetEndsThePageOnAWholeItem(t *testing.T) {
	for _, list := range budgetedListTools {
		t.Run(list.tool, func(t *testing.T) {
			harness := newListBudgetHarness(t, list, list.items(40, 400))
			result := list.call(t, harness.server, 20, 10)

			size := textBytes(result)
			require.LessOrEqual(t, size, listBudget, "the result's text blocks must fit the budget: %s", allTextBlocks(result))
			delivered := readDeliveredList(t, result, list.itemsAt)
			returned := len(delivered.items)
			require.True(t, returned >= 1 && returned < 20, "want a partial page, got %d of 20 items", returned)
			for i, item := range delivered.items {
				require.Contains(t, string(item), fmt.Sprintf(`item-%03d `, 10+i), "item %d must be upstream item %d, whole", i, 10+i)
			}
			require.Greater(t, size+len(delivered.items[returned-1])+1, listBudget, "another item would have fit; the page must keep every leading item that fits")
			require.JSONEq(t, list.cutPagination(10, returned, 40), delivered.paginationJSON)
			if list.tool == "signoz_list_dashboards" {
				var data struct {
					Total int `json:"total"`
				}
				require.NoError(t, json.Unmarshal(delivered.object["data"], &data))
				require.Equal(t, 40, data.Total, "SigNoz's own total stays")
			}

			note := fmt.Sprintf("note: returned %d items (limit 20); the result size budget of %d bytes ended this page, so more results exist (hasMore=true). Fetch the next page with offset=%d", returned, listBudget, 10+returned)
			if list.windowed {
				note += ", and pass window.startMs and window.endMs as start and end so the page does not shift"
			}
			require.Contains(t, trailingTextBlocks(result), note+".")
			require.Equal(t, map[string]int64{budgetEventRowsDropped: 1}, harness.budgetEvents(t, list.tool))
		})
	}
}

// One item always comes back, whole and unchanged, because a caller may write
// it back through an update tool. An item larger than the budget is logged
// and counted rather than shortened.
func TestListToolReturnsAnOversizedFirstItemWhole(t *testing.T) {
	padding := strings.Repeat("p", 6000)
	for _, list := range budgetedListTools {
		for _, tc := range []struct {
			name   string
			items  int
			events map[string]int64
		}{
			{name: "only item", items: 1, events: map[string]int64{budgetEventOverBudget: 1}},
			{name: "first of two", items: 2, events: map[string]int64{budgetEventRowsDropped: 1, budgetEventOverBudget: 1}},
		} {
			t.Run(list.tool+" "+tc.name, func(t *testing.T) {
				harness := newListBudgetHarness(t, list, list.items(tc.items, 6000))
				result := list.call(t, harness.server, 20, 0)

				delivered := readDeliveredList(t, result, list.itemsAt)
				require.Len(t, delivered.items, 1, "one item always comes back")
				require.Contains(t, string(delivered.items[0]), "item-000 "+padding, "the item keeps every byte of its long string")
				require.Greater(t, textBytes(result), listBudget)
				if tc.items > 1 {
					require.JSONEq(t, list.cutPagination(0, 1, 2), delivered.paginationJSON)
				} else {
					require.NotContains(t, delivered.paginationJSON, truncatedKey, "a page the budget did not cut keeps its pagination")
				}
				require.Contains(t, harness.logs.String(), "tool result is over its size budget after fitting rows")
				require.Equal(t, tc.events, harness.budgetEvents(t, list.tool))
			})
		}
	}
}

// The validation decorator's notices count toward the budget: the page is
// cut with them in place, not before they are appended.
func TestListToolBudgetCountsDecoratorNotices(t *testing.T) {
	var stray []string
	for i := range 10 {
		stray = append(stray, fmt.Sprintf(`"unknown_argument_with_a_long_name_%02d":"x"`, i))
	}
	for _, list := range budgetedListTools {
		if list.tool == "signoz_list_notification_channels" {
			continue // it rejects undeclared arguments instead of serving them with a notice
		}
		t.Run(list.tool, func(t *testing.T) {
			harness := newListBudgetHarness(t, list, list.items(40, 400))
			result := list.call(t, harness.server, 20, 0, stray...)

			notes := trailingTextBlocks(result)
			require.Contains(t, notes[len(notes)-1], "this tool ignored the arguments", "the ignored-arguments notice is the last block")
			require.LessOrEqual(t, textBytes(result), listBudget, "the delivered result, notices included, must fit: %s", allTextBlocks(result))
			delivered := readDeliveredList(t, result, list.itemsAt)
			for i, item := range delivered.items {
				require.Contains(t, string(item), fmt.Sprintf(`item-%03d `, i))
			}
			require.Contains(t, delivered.paginationJSON, fmt.Sprintf(`"nextOffset":%d`, len(delivered.items)))
		})
	}
}

// After a cut, text block 0 and structuredContent carry the same page, and
// every note reaches structuredContent.notes as well as its own text block.
func TestListToolCutPageKeepsTextAndStructuredContentAlike(t *testing.T) {
	for _, list := range budgetedListTools {
		t.Run(list.tool, func(t *testing.T) {
			harness := newListBudgetHarness(t, list, list.items(40, 400))
			result := list.call(t, harness.server, 20, 0)
			require.NotNil(t, result.StructuredContent)

			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, string(structured), textContent(t, result))
			var page struct {
				Notes []string `json:"notes"`
			}
			require.NoError(t, json.Unmarshal(structured, &page))
			require.Equal(t, trailingTextBlocks(result), page.Notes)
			require.Contains(t, strings.Join(page.Notes, "\n"), "result size budget")
			require.Contains(t, string(structured), `"truncated":true`)
		})
	}
}

// A cut page of a tool with an output schema still validates against it.
func TestListToolCutPageMatchesItsOutputSchema(t *testing.T) {
	for _, list := range budgetedListTools {
		if list.tool != "signoz_list_alerts" && list.tool != "signoz_list_alert_rules" {
			continue // the other list tools advertise no output schema
		}
		t.Run(list.tool, func(t *testing.T) {
			harness := newListBudgetHarness(t, list, list.items(40, 400))
			result := list.call(t, harness.server, 20, 0)
			require.Contains(t, readDeliveredList(t, result, list.itemsAt).paginationJSON, `"truncated":true`)

			var metrics metricdata.ResourceMetrics
			require.NoError(t, harness.reader.Collect(context.Background(), &metrics))
			mismatches, _ := oteltest.FindInt64SumMetric(metrics, "mcp.tool.validation.mismatches")
			for _, point := range mismatches.DataPoints {
				direction, _ := point.Attributes.Value("validation.direction")
				path, _ := point.Attributes.Value("validation.path")
				require.NotEqual(t, "output", direction.AsString(), "the cut page does not match the output schema at %s; declare the pagination fields a cut adds", path.AsString())
			}
		})
	}
}

// A dashboard body the budget cannot page, because its items or its total are
// not where SigNoz puts them, is returned as SigNoz sent it, with a WARN and a
// fail_open count instead of a guessed cut.
func TestListDashboardsFailsOpenOnABodyItCannotPage(t *testing.T) {
	bodies := map[string]func(items []string, total int) string{
		"items under another key": func(items []string, total int) string {
			return fmt.Sprintf(`{"status":"success","data":{"boards":[%s],"total":%d}}`, strings.Join(items, ","), total)
		},
		"null total": func(items []string, _ int) string {
			return fmt.Sprintf(`{"status":"success","data":{"dashboards":[%s],"total":null}}`, strings.Join(items, ","))
		},
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			list := budgetedListTools[len(budgetedListTools)-1]
			require.Equal(t, "signoz_list_dashboards", list.tool)
			list.paged = false
			list.body = body
			items := list.items(20, 400)
			harness := newListBudgetHarness(t, list, items)
			result := callRegisteredTool(t, harness.server, list.tool, `{"limit":20}`)
			require.False(t, result.IsError, allTextBlocks(result))

			require.Equal(t, list.body(items, 20), textContent(t, result))
			require.Contains(t, harness.logs.String(), "list result left unbounded because its items could not be paged")
			require.Equal(t, map[string]int64{budgetEventFailOpen: 1}, harness.budgetEvents(t, list.tool))
		})
	}
}
