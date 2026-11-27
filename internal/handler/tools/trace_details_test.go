package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// The trace and span the fixtures under testdata/trace_details were recorded
// from: a SigNoz built from the trace route changes, on a seeded trace whose
// archive request fails on a 409 from storage.
const (
	testTraceID   = "d9e8e861f9e84ab9b736603684800956"
	testSpanID    = "97adbaac06644874"
	testTraceBase = "/api/v1/traces/" + testTraceID
)

type traceRoute struct {
	status int
	body   string
}

func traceFixture(t *testing.T, name string) traceRoute {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "trace_details", name))
	require.NoError(t, err)
	return traceRoute{http.StatusOK, string(body)}
}

func traceJSON(data any) traceRoute {
	body, _ := json.Marshal(map[string]any{"status": "success", "data": data})
	return traceRoute{http.StatusOK, string(body)}
}

// traceUpstream serves one response per route and records each trace request
// as path?query. A route keyed by path?query wins over one keyed by path.
func traceUpstream(t *testing.T, routes map[string]traceRoute) (*Handler, *[]string) {
	t.Helper()
	var requests []string
	notFound := traceFixture(t, "not_found.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every tool first asks SigNoz for its external URL, for the webUrl link.
		if !strings.HasPrefix(r.URL.Path, "/api/v1/traces/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		request := r.URL.Path + "?" + r.URL.Query().Encode()
		requests = append(requests, request)
		route, ok := routes[request]
		if !ok {
			route, ok = routes[r.URL.Path]
		}
		if !ok {
			route = traceRoute{http.StatusNotFound, notFound.body}
		}
		w.WriteHeader(route.status)
		_, _ = w.Write([]byte(route.body))
	}))
	t.Cleanup(server.Close)
	return newTestHandler(signozclient.NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)), &requests
}

type traceToolResult struct {
	payload map[string]any
	notes   string
	text    string
}

func callTraceDetails(t *testing.T, h *Handler, args map[string]any) (*mcp.CallToolResult, traceToolResult) {
	t.Helper()
	res, err := h.handleGetTraceDetails(testCtx(), makeToolRequest("signoz_get_trace_details", args))
	require.NoError(t, err)
	out := traceToolResult{text: textContent(t, res)}
	if res.IsError {
		return res, out
	}
	require.NoError(t, json.Unmarshal([]byte(out.text), &out.payload))
	var notes []string
	for _, block := range res.Content[1:] {
		note, ok := mcp.AsTextContent(block)
		require.True(t, ok)
		notes = append(notes, note.Text)
	}
	out.notes = strings.Join(notes, "\n")
	return res, out
}

func traceErrorCode(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.True(t, res.IsError, "expected an error result")
	structured, _ := res.StructuredContent.(map[string]any)
	code, _ := structured["code"].(string)
	return code
}

func traceCursorIn(t *testing.T, out traceToolResult) string {
	t.Helper()
	pagination, _ := out.payload["pagination"].(map[string]any)
	cursor, _ := pagination["nextCursor"].(string)
	require.NotEmpty(t, cursor, "a page with more after it gives a cursor")
	assert.Contains(t, out.notes, cursor, "the note carries the same cursor for clients that read text")
	return cursor
}

func TestGetTraceDetails_TraceIDAloneReturnsTheOverview(t *testing.T) {
	h, requests := traceUpstream(t, map[string]traceRoute{
		testTraceBase + "/summary":    traceFixture(t, "summary.json"),
		testTraceBase + "/highlights": traceFixture(t, "highlights.json"),
	})

	// A dashed UUID is the same trace.
	_, out := callTraceDetails(t, h, map[string]any{"traceId": "d9e8e861-f9e8-4ab9-b736-603684800956", "view": []any{"error_origins", "time_spent"}, "pageSize": "2"})

	assert.ElementsMatch(t, []string{testTraceBase + "/summary?", testTraceBase + "/highlights?limit=2&views=error_origins&views=time_spent"}, *requests)
	assert.Equal(t, "POST /api/documents/{id}/archive", out.payload["summary"].(map[string]any)["rootServiceEntryPoint"])
	assert.Len(t, out.payload["errorOrigins"].(map[string]any)["items"], 1, "SigNoz's views pass through under its own keys")
	assert.Contains(t, out.notes, `More of timeSpent: scope "all" with view "time_spent".`, "a view with more spans names the call that lists them")
	assert.NotContains(t, out.notes, "More of errorOrigins", "a view shown whole has nothing more to fetch")
}

func TestGetTraceDetails_SpanIDAloneReturnsTheSpan(t *testing.T) {
	h, requests := traceUpstream(t, map[string]traceRoute{
		testTraceBase + "/spans/" + testSpanID: traceFixture(t, "span.json"),
		testTraceBase + "/summary":             traceFixture(t, "summary.json"),
		testTraceBase + "/highlights":          traceFixture(t, "highlights.json"),
	})

	_, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "spanId": testSpanID})
	assert.Equal(t, []string{testTraceBase + "/spans/" + testSpanID + "?"}, *requests, "the span alone does not fetch the trace around it")
	span := out.payload["span"].(map[string]any)
	assert.Equal(t, "PUT storage-api /objects/{id}/lock", span["name"])
	assert.Contains(t, span["attributes"].(map[string]any)["http.response.body"], "retention policy rp-90d")

	_, out = callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "spanId": testSpanID, "includeOverview": "true"})
	assert.Contains(t, *requests, testTraceBase+"/highlights?limit=5")
	overview := out.payload["traceOverview"].(map[string]any)
	assert.EqualValues(t, 10, overview["summary"].(map[string]any)["totalSpansCount"])
	assert.NotEmpty(t, overview["unmarkedFailures"].(map[string]any)["items"])
	assert.Contains(t, out.payload, "span")
}

func TestGetTraceDetails_SingleSpanKeepsValuesWholeAndListsCutThem(t *testing.T) {
	stack := strings.Repeat("at com.acme.Stage.process(Stage.java:1)\n", 100) + "Caused by: Deadlock found"
	span := map[string]any{
		"span_id": testSpanID, "name": "LedgerWriter.flush",
		"events": []any{map[string]any{"name": "exception", "attributeMap": map[string]any{"exception.stacktrace": stack}}},
	}
	prompt := strings.Repeat("p", 100_000)
	h, _ := traceUpstream(t, map[string]traceRoute{
		testTraceBase + "/spans/" + testSpanID: traceJSON(span),
		testTraceBase + "/spans/llm":           traceJSON(map[string]any{"span_id": "llm", "attributes": map[string]any{"gen_ai.input.messages": prompt, "gen_ai.request.model": "gpt-4o"}}),
		testTraceBase + "/spans":               traceJSON(map[string]any{"total": 1, "items": []any{map[string]any{"span": span}}}),
	})

	_, whole := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "spanId": testSpanID, "selectFields": "http.route"})
	assert.Contains(t, whole.text, "Caused by: Deadlock found", "a single span that fits keeps its values whole")
	assert.Contains(t, whole.notes, "selectFields applies to a list")

	_, list := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all"})
	assert.NotContains(t, list.text, "Caused by: Deadlock found")
	assert.Contains(t, list.notes, "items[0].span.events[0].attributeMap.exception.stacktrace")
	assert.Contains(t, list.notes, "Open a span with spanId for its values whole.")

	_, long := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "spanId": "llm"})
	assert.Equal(t, "gpt-4o", long.payload["span"].(map[string]any)["attributes"].(map[string]any)["gen_ai.request.model"], "short values stay whole")
	assert.Contains(t, long.notes, "attributes.gen_ai.input.messages (100000 characters)", "a span too long for one result says which value was cut")
	assert.LessOrEqual(t, clientLen([]byte(long.text)), traceResultLimit)
}

func TestGetTraceDetails_SpanWithManyEventsStaysWithinOneResult(t *testing.T) {
	events := make([]any, 1000)
	for i := range events {
		events[i] = map[string]any{"name": "retry", "attributeMap": map[string]any{"detail": strings.Repeat("y", 200)}}
	}
	h, _ := traceUpstream(t, map[string]traceRoute{testTraceBase + "/spans/" + testSpanID: traceJSON(map[string]any{"span_id": testSpanID, "name": "Worker.run", "events": events})})

	_, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "spanId": testSpanID})

	shown := len(out.payload["span"].(map[string]any)["events"].([]any))
	assert.Less(t, shown, 1000)
	assert.Contains(t, out.notes, fmt.Sprintf("events shows the first %d of 1000 to fit the size limit.", shown))
	assert.LessOrEqual(t, clientLen([]byte(out.text)), traceResultLimit)
}

func TestGetTraceDetails_ListsPickTheRouteByScope(t *testing.T) {
	spans := testTraceBase + "/spans"
	list := traceFixture(t, "spans.json")
	h, requests := traceUpstream(t, map[string]traceRoute{
		spans: list,
		spans + "/" + testSpanID + "/descendants": list,
		spans + "/" + testSpanID + "/children":    list,
		spans + "/" + testSpanID + "/ancestors":   list,
	})

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"scope": "all"}, spans + "?"},
		{map[string]any{"search": "worldpay AND has_error = true"}, spans + "?query=worldpay+AND+has_error+%3D+true"},
		{map[string]any{"spanId": testSpanID, "view": "error_origins"}, spans + "/" + testSpanID + "/descendants?view=error_origins"},
		{map[string]any{"spanId": testSpanID, "scope": "descendants"}, spans + "/" + testSpanID + "/descendants?"},
		{map[string]any{"spanId": testSpanID, "scope": "children", "selectFields": []any{"http.route"}}, spans + "/" + testSpanID + "/children?selectFields=http.route"},
		{map[string]any{"spanId": testSpanID, "scope": "ancestors", "pageSize": 5}, spans + "/" + testSpanID + "/ancestors?limit=5"},
	} {
		*requests = nil
		tc.args["traceId"] = testTraceID
		_, out := callTraceDetails(t, h, tc.args)
		assert.Equal(t, []string{tc.want}, *requests)
		assert.Len(t, out.payload["items"], 3)
	}
}

func TestGetTraceDetails_CursorContinuesTheSameCall(t *testing.T) {
	h, requests := traceUpstream(t, map[string]traceRoute{testTraceBase + "/spans": traceFixture(t, "spans.json")})

	_, first := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all"})
	assert.Contains(t, first.notes, "Items 1 to 3 of 10.")
	assert.Equal(t, true, first.payload["pagination"].(map[string]any)["hasMore"])
	cursor := traceCursorIn(t, first)

	*requests = nil
	_, _ = callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all", "cursor": cursor})
	assert.Equal(t, []string{testTraceBase + "/spans?offset=3"}, *requests)

	res, _ := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all", "view": "links", "cursor": cursor})
	assert.Equal(t, CodeValidationFailed, traceErrorCode(t, res), "a cursor of another call is rejected, not read as an offset")
}

func TestGetTraceDetails_LastPageSaysSo(t *testing.T) {
	h, _ := traceUpstream(t, map[string]traceRoute{testTraceBase + "/spans": traceJSON(map[string]any{"total": 2, "items": []any{map[string]any{"span": map[string]any{"span_id": "a"}}, map[string]any{"span": map[string]any{"span_id": "b"}}}})})

	_, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all"})

	pagination := out.payload["pagination"].(map[string]any)
	assert.Equal(t, false, pagination["hasMore"])
	assert.NotContains(t, pagination, "nextCursor")
	assert.Contains(t, out.notes, "Items 1 to 2 of 2. This is the last page.")
}

func TestGetTraceDetails_ListPageEndsAtTheSizeLimit(t *testing.T) {
	items := make([]any, 200)
	for i := range items {
		items[i] = map[string]any{"span": map[string]any{"span_id": fmt.Sprintf("%016d", i), "name": "process_payout", "attributes": map[string]any{"note": strings.Repeat("x", 400)}}}
	}
	h, requests := traceUpstream(t, map[string]traceRoute{testTraceBase + "/spans": traceJSON(map[string]any{"total": 5008, "items": items})})

	res, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all", "pageSize": 200})

	shown := len(out.payload["items"].([]any))
	assert.Less(t, shown, 200)
	assert.Greater(t, shown, 10)
	assert.Contains(t, out.notes, fmt.Sprintf("Items 1 to %d of 5008. The page ended early at the size limit.", shown))

	// The next page starts after the last span shown, not after the last span SigNoz sent.
	*requests = nil
	_, _ = callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all", "pageSize": 200, "cursor": traceCursorIn(t, out)})
	assert.Equal(t, []string{fmt.Sprintf("%s/spans?limit=200&offset=%d", testTraceBase, shown)}, *requests)
	total := 0
	for _, block := range res.Content {
		text, _ := mcp.AsTextContent(block)
		total += clientLen([]byte(text.Text))
	}
	assert.LessOrEqual(t, total, traceResultLimit, "the whole result, notes included, as a client counts it")
}

func TestGetTraceDetails_ThreadKeepsMessagesAndDropsAttributes(t *testing.T) {
	h, requests := traceUpstream(t, map[string]traceRoute{testTraceBase + "/thread": traceFixture(t, "thread.json")})

	_, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "view": "thread"})

	chat := out.payload["spans"].([]any)[1].(map[string]any)
	assert.Equal(t, "support-agent", chat["service.name"])
	assert.NotContains(t, chat, "attributes")
	assert.NotContains(t, chat, "events")
	assert.NotEmpty(t, chat["formattedInput"])
	assert.NotContains(t, out.text, "gen_ai.input.messages", "the raw message attributes repeat the formatted ones")

	// The thread pages by SigNoz's own cursor, carried inside ours.
	var recorded struct {
		Data struct {
			NextCursor string `json:"nextCursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(traceFixture(t, "thread.json").body), &recorded))
	*requests = nil
	_, _ = callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "view": "thread", "cursor": traceCursorIn(t, out), "pageSize": 10})
	assert.Equal(t, []string{testTraceBase + "/thread?after=" + recorded.Data.NextCursor + "&limit=10"}, *requests)
}

func TestGetTraceDetails_ThreadPageEndsAtTheSizeLimit(t *testing.T) {
	spans := make([]any, 20)
	for i := range spans {
		messages := make([]any, 10)
		for j := range messages {
			messages[j] = map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "content": strings.Repeat("m", 900)}}}
		}
		spans[i] = map[string]any{"span_id": fmt.Sprintf("s%02d", i), "name": "chat gpt-4o", "formattedInput": messages}
	}
	thread := testTraceBase + "/thread"
	h, requests := traceUpstream(t, map[string]traceRoute{
		thread + "?":        traceJSON(map[string]any{"spans": spans, "nextCursor": "after-s19"}),
		thread + "?limit=4": traceJSON(map[string]any{"spans": spans[:4], "nextCursor": "after-s03"}),
	})

	_, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "view": "thread"})

	require.Len(t, out.payload["spans"], 4, "four spans of about 9,000 characters fit in one result")
	assert.Equal(t, []string{thread + "?", thread + "?limit=4"}, *requests, "SigNoz is asked again for only the spans that fit")
	assert.Contains(t, out.notes, "The page ended early at the size limit.")

	// The cursor carries the shorter page's nextCursor, so no span is skipped.
	*requests = nil
	_, _ = callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "view": "thread", "cursor": traceCursorIn(t, out)})
	assert.Equal(t, []string{thread + "?after=after-s03"}, *requests)
}

func TestGetTraceDetails_RejectsInputsThatDoNotCombine(t *testing.T) {
	h, requests := traceUpstream(t, nil)
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"two views in a list", map[string]any{"scope": "all", "view": []any{"error_origins", "links"}}, "exactly one view"},
		{"thread with a span", map[string]any{"view": "thread", "spanId": testSpanID}, `"thread" takes only`},
		{"scope without a span", map[string]any{"scope": "children"}, "needs a spanId"},
		{"unknown scope", map[string]any{"scope": "subtree"}, "must be one of all, descendants, children, or ancestors"},
		{"all with a span", map[string]any{"scope": "all", "spanId": testSpanID}, `use "descendants"`},
		{"descendants without a span", map[string]any{"scope": "descendants"}, "needs a spanId"},
		{"page size that is not a number", map[string]any{"scope": "all", "pageSize": "many"}, "must be a positive integer"},
		{"page size below one", map[string]any{"scope": "all", "pageSize": -5}, "must be a positive integer"},
		{"unreadable cursor", map[string]any{"scope": "all", "cursor": "not-a-cursor"}, "is not valid"},
		{"retired includeSpans", map[string]any{"includeSpans": false}, `scope "all" lists its spans`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["traceId"] = testTraceID
			res, out := callTraceDetails(t, h, tc.args)
			assert.Equal(t, CodeValidationFailed, traceErrorCode(t, res))
			assert.Contains(t, out.text, tc.want)
		})
	}
	assert.Empty(t, *requests, "an invalid call never reaches the trace routes")
}

func TestGetTraceDetails_SigNozErrorsReachTheAgent(t *testing.T) {
	invalid := traceFixture(t, "invalid_view.json")
	invalid.status = http.StatusBadRequest
	changed := "/api/v1/traces/" + strings.Repeat("0", 32)
	h, requests := traceUpstream(t, map[string]traceRoute{
		testTraceBase + "/spans": invalid,
		changed + "/spans":       traceJSON(map[string]any{"total": 3}),
	})

	// SigNoz, not this tool, knows the views.
	res, out := callTraceDetails(t, h, map[string]any{"traceId": testTraceID, "scope": "all", "view": "slowest"})
	assert.Equal(t, CodeValidationFailed, traceErrorCode(t, res))
	assert.Contains(t, out.text, "expected one of error_origins, unmarked_failures, time_spent, ai_usage, links")

	// A traceparent header is cut down to its trace ID.
	*requests = nil
	res, out = callTraceDetails(t, h, map[string]any{"traceId": "00-" + strings.Repeat("a", 32) + "-00f067aa0ba902b7-00"})
	assert.Equal(t, []string{"/api/v1/traces/" + strings.Repeat("a", 32) + "/summary?"}, *requests)
	assert.Equal(t, CodeNotFound, traceErrorCode(t, res))
	assert.Contains(t, out.text, "trace not found")

	// A list without its items is a changed contract, not an empty trace.
	res, _ = callTraceDetails(t, h, map[string]any{"traceId": strings.Repeat("0", 32), "scope": "all"})
	assert.Equal(t, CodeUpstreamError, traceErrorCode(t, res))
}
