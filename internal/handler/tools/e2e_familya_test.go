//go:build e2e

// Package tools live end-to-end verification for the Family A (silent-failure)
// fixes (#363). It drives the MODIFIED handlers against a real SigNoz instance
// to confirm the assumed JSON response shapes (esp. the N4 row-count paths) and
// the new behaviors actually hold against real data.
//
// CREDENTIALS: read ONLY from the environment, never hardcoded. The test
// t.Skip()s when either var is absent so the normal (untagged) build and CI are
// unaffected. Run with:
//
//	SIGNOZ_E2E_URL=https://app.us.staging.signoz.cloud \
//	SIGNOZ_E2E_TOKEN=<session-jwt> \
//	go test -tags=e2e -run E2E ./internal/handler/tools/...
//
// The token is supplied as a Bearer Authorization header. Every resource the
// test creates uses a unique "mcp-e2e-a-<rand>" prefix and is deleted (and the
// deletion confirmed) before the test returns.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/mark3labs/mcp-go/mcp"
)

// e2eSetup builds a real SigNoz client pointed at staging using env-supplied
// credentials and wraps it in a Handler via the clientOverride path. It skips
// the test when credentials are absent.
func e2eSetup(t *testing.T) (*Handler, context.Context) {
	t.Helper()
	baseURL := os.Getenv("SIGNOZ_E2E_URL")
	token := os.Getenv("SIGNOZ_E2E_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("SIGNOZ_E2E_URL / SIGNOZ_E2E_TOKEN not set; skipping live E2E")
	}
	// JWT bearer auth: header name "Authorization", value "Bearer <token>".
	client := signozclient.NewClient(logpkg.New("error"), baseURL, "Bearer "+token, "Authorization", nil)
	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: client,
	}
	return h, context.Background()
}

func e2eRand() string {
	return fmt.Sprintf("mcp-e2e-a-%d-%04d", time.Now().UnixNano()%1_000_000, rand.Intn(10000))
}

// firstTextBlock returns content block 0's text (the JSON payload).
func firstTextBlock(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil || len(r.Content) == 0 {
		t.Fatalf("result has no content")
	}
	tc, ok := mcp.AsTextContent(r.Content[0])
	if !ok {
		t.Fatalf("block 0 is not text")
	}
	return tc.Text
}

// noteBlocks returns all non-JSON note blocks (1..n) concatenated.
func noteBlocks(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) < 2 {
		return ""
	}
	var b strings.Builder
	for _, c := range r.Content[1:] {
		if tc, ok := mcp.AsTextContent(c); ok {
			b.WriteString(tc.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- N4: confirm assumed JSON paths against real responses ---

// TestE2E_N4_SearchLogs_RowPath verifies the data.data.results[].rows[] path
// used by countQueryRangeRows matches the live search_logs response.
func TestE2E_N4_SearchLogs_RowPath(t *testing.T) {
	h, ctx := e2eSetup(t)
	res, err := h.handleSearchLogs(ctx, makeToolRequest("signoz_search_logs", map[string]any{
		"timeRange": "1h",
		"limit":     "5",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("search_logs returned error: %s", firstTextBlock(t, res))
	}
	body := firstTextBlock(t, res)
	n, ok := countQueryRangeRows([]byte(body))
	if !ok {
		t.Errorf("N4 DRIFT: countQueryRangeRows could not walk live search_logs body (data.data.results[].rows[] path may have changed). Body prefix: %s", truncForLog(body))
	} else {
		t.Logf("search_logs: counted %d rows via data.data.results[].rows[]", n)
	}
	note := noteBlocks(res)
	if !strings.Contains(note, "hasMore") {
		t.Errorf("expected a completeness note with hasMore, got notes: %q", note)
	} else {
		t.Logf("search_logs completeness note: %s", strings.TrimSpace(note))
	}
}

// TestE2E_N4_SearchTraces_RowPath verifies the row path AND returns a real
// traceId for the includeSpans test. It also asserts error=true/false.
func TestE2E_N4_SearchTraces_RowPath(t *testing.T) {
	h, ctx := e2eSetup(t)
	res, err := h.handleSearchTraces(ctx, makeToolRequest("signoz_search_traces", map[string]any{
		"timeRange": "1h",
		"limit":     "5",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("search_traces returned error: %s", firstTextBlock(t, res))
	}
	body := firstTextBlock(t, res)
	n, ok := countQueryRangeRows([]byte(body))
	if !ok {
		t.Errorf("N4 DRIFT: countQueryRangeRows could not walk live search_traces body. Body prefix: %s", truncForLog(body))
	} else {
		t.Logf("search_traces: counted %d rows", n)
	}
	if !strings.Contains(noteBlocks(res), "hasMore") {
		t.Errorf("expected completeness note with hasMore on search_traces")
	}
}

func TestE2E_N4_ListMetrics_Path(t *testing.T) {
	h, ctx := e2eSetup(t)
	res, err := h.handleListMetrics(ctx, makeToolRequest("signoz_list_metrics", map[string]any{
		"limit": "5",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_metrics returned error: %s", firstTextBlock(t, res))
	}
	body := firstTextBlock(t, res)
	n, ok := countDataArrayRows([]byte(body), "metrics")
	if !ok {
		t.Errorf("N4 DRIFT: list_metrics data.metrics[] path not found. Body prefix: %s", truncForLog(body))
	} else {
		t.Logf("list_metrics: counted %d rows via data.metrics[]", n)
	}
	if !strings.Contains(noteBlocks(res), "hasMore") {
		t.Errorf("expected completeness note with hasMore on list_metrics")
	}
}

func TestE2E_N4_TopMetrics_Path(t *testing.T) {
	h, ctx := e2eSetup(t)
	res, err := h.handleGetTopMetrics(ctx, makeToolRequest("signoz_get_top_metrics", map[string]any{
		"timeRange": "24h",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_top_metrics returned error: %s", firstTextBlock(t, res))
	}
	body := firstTextBlock(t, res)
	n, ok := countDataArrayRows([]byte(body), "samples")
	if !ok {
		t.Errorf("N4 DRIFT: get_top_metrics data.samples[] path not found. Body prefix: %s", truncForLog(body))
	} else {
		t.Logf("get_top_metrics: counted %d rows via data.samples[]", n)
	}
	note := noteBlocks(res)
	if !strings.Contains(note, "metrics") {
		t.Errorf("expected a top-metrics completeness note, got %q", note)
	}
}

// TestE2E_N4_AlertHistory_Path needs a real ruleId. It lists alert rules first,
// then fetches history for the first rule. If no rules exist, it skips.
func TestE2E_N4_AlertHistory_Path(t *testing.T) {
	h, ctx := e2eSetup(t)
	rulesRes, err := h.handleListAlertRules(ctx, makeToolRequest("signoz_list_alert_rules", map[string]any{"limit": "5"}))
	if err != nil {
		t.Fatalf("list_alert_rules handler error: %v", err)
	}
	if rulesRes.IsError {
		t.Fatalf("list_alert_rules error: %s", firstTextBlock(t, rulesRes))
	}
	ruleID := firstAlertRuleID(t, firstTextBlock(t, rulesRes))
	if ruleID == "" {
		t.Skip("no alert rules on staging; cannot exercise get_alert_history row path")
	}
	res, err := h.handleGetAlertHistory(ctx, makeToolRequest("signoz_get_alert_history", map[string]any{
		"ruleId":    ruleID,
		"timeRange": "24h",
		"limit":     "5",
	}))
	if err != nil {
		t.Fatalf("get_alert_history handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_alert_history error: %s", firstTextBlock(t, res))
	}
	body := firstTextBlock(t, res)
	n, ok := countAlertHistoryRows([]byte(body))
	if !ok {
		t.Errorf("N4 DRIFT: get_alert_history data[] or data.items[] path not found. Body prefix: %s", truncForLog(body))
	} else {
		t.Logf("get_alert_history: counted %d rows via data[] or data.items[]", n)
	}
	if !strings.Contains(noteBlocks(res), "hasMore") {
		t.Errorf("expected completeness note with hasMore on get_alert_history")
	}
}

// --- N1: execute_builder_query succeeds; warning-note path is exercised ---

func TestE2E_N1_ExecuteBuilderQuery(t *testing.T) {
	h, ctx := e2eSetup(t)
	now := time.Now().UnixMilli()
	start := now - int64(time.Hour/time.Millisecond)
	query := map[string]any{
		"schemaVersion": "v1",
		"start":         start,
		"end":           now,
		"requestType":   "scalar",
		"compositeQuery": map[string]any{
			"queries": []any{
				map[string]any{
					"type": "builder_query",
					"spec": map[string]any{
						"name":         "A",
						"signal":       "logs",
						"aggregations": []any{map[string]any{"expression": "count()"}},
					},
				},
			},
		},
	}
	res, err := h.handleExecuteBuilderQuery(ctx, makeToolRequest("signoz_execute_builder_query", map[string]any{"query": query}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("execute_builder_query error: %s", firstTextBlock(t, res))
	}
	body := firstTextBlock(t, res)
	if !json.Valid([]byte(body)) {
		t.Errorf("execute_builder_query block 0 is not valid JSON")
	}
	// Warning note path: only present if the backend emits warnings. Just log.
	t.Logf("execute_builder_query content blocks=%d, notes=%q", len(res.Content), strings.TrimSpace(noteBlocks(res)))
}

// --- N2: stepInterval as JSON number AND string both honored ---

func TestE2E_N2_StepInterval_NumberAndString(t *testing.T) {
	h, ctx := e2eSetup(t)
	for _, tc := range []struct {
		name string
		step any
	}{
		{"json-number", float64(60)},
		{"string", "60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := h.handleAggregateLogs(ctx, makeToolRequest("signoz_aggregate_logs", map[string]any{
				"aggregation":  "count",
				"timeRange":    "1h",
				"requestType":  "time_series",
				"stepInterval": tc.step,
			}))
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if res.IsError {
				t.Fatalf("aggregate_logs error: %s", firstTextBlock(t, res))
			}
			// A dropped stepInterval would have produced no error but the parser
			// warning is internal; the key assertion is the call succeeds and the
			// body is valid JSON for both number and string forms.
			if !json.Valid([]byte(firstTextBlock(t, res))) {
				t.Errorf("aggregate_logs body not valid JSON for stepInterval=%v", tc.step)
			}
		})
	}

	// Confirm the parser actually keeps the value (number form) at the unit level
	// against the live path by parsing directly.
	req, perr := parseAggregateArgs(map[string]any{
		"aggregation":  "count",
		"timeRange":    "1h",
		"stepInterval": float64(120),
	}, "logs", "")
	if perr != nil {
		t.Fatalf("parse error: %v", perr)
	}
	if req.StepInterval == nil || *req.StepInterval != 120 {
		t.Errorf("stepInterval number not honored: %v", req.StepInterval)
	}
}

// --- N3: booleans accept real bool + legacy string; garbage rejected ---

func TestE2E_N3_SearchTraces_ErrorBool(t *testing.T) {
	h, ctx := e2eSetup(t)
	for _, v := range []any{true, false, "true", "false"} {
		res, err := h.handleSearchTraces(ctx, makeToolRequest("signoz_search_traces", map[string]any{
			"timeRange": "1h",
			"limit":     "2",
			"error":     v,
		}))
		if err != nil {
			t.Fatalf("handler error for error=%v: %v", v, err)
		}
		if res.IsError {
			t.Errorf("search_traces error=%v unexpectedly failed: %s", v, firstTextBlock(t, res))
		}
	}
	// garbage rejected
	res, err := h.handleSearchTraces(ctx, makeToolRequest("signoz_search_traces", map[string]any{
		"timeRange": "1h",
		"error":     "maybe",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for garbage error value")
	}
}

func TestE2E_N3_GetTraceDetails_IncludeSpans(t *testing.T) {
	h, ctx := e2eSetup(t)
	traceID := e2eFindTraceID(t, h, ctx)
	if traceID == "" {
		t.Skip("no traces on staging in the last 6h; cannot exercise includeSpans")
	}
	for _, v := range []any{true, false, "true", "false"} {
		res, err := h.handleGetTraceDetails(ctx, makeToolRequest("signoz_get_trace_details", map[string]any{
			"traceId":      traceID,
			"timeRange":    "6h",
			"includeSpans": v,
		}))
		if err != nil {
			t.Fatalf("handler error includeSpans=%v: %v", v, err)
		}
		if res.IsError {
			t.Errorf("get_trace_details includeSpans=%v failed: %s", v, firstTextBlock(t, res))
		}
	}
	// garbage rejected
	res, err := h.handleGetTraceDetails(ctx, makeToolRequest("signoz_get_trace_details", map[string]any{
		"traceId":      traceID,
		"includeSpans": "perhaps",
	}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error for garbage includeSpans")
	}
}

func TestE2E_N3_QueryMetrics_IsMonotonic(t *testing.T) {
	h, ctx := e2eSetup(t)
	// Pick a real metric name from list_metrics.
	metricName := e2eFirstMetricName(t, h, ctx)
	if metricName == "" {
		t.Skip("no metrics on staging; cannot exercise isMonotonic")
	}
	for _, v := range []any{true, false, "true", "false"} {
		res, err := h.handleQueryMetrics(ctx, makeToolRequest("signoz_query_metrics", map[string]any{
			"metricName":  metricName,
			"timeRange":   "1h",
			"isMonotonic": v,
		}))
		if err != nil {
			t.Fatalf("handler error isMonotonic=%v: %v", v, err)
		}
		// query_metrics may legitimately error if the metric type/aggregation
		// combo is invalid, but it must NOT error on isMonotonic parsing alone.
		// We accept either outcome but log it.
		t.Logf("query_metrics isMonotonic=%v IsError=%v", v, res.IsError)
	}
	// garbage rejected at parse layer
	if _, perr := parseMetricsQueryArgs(map[string]any{"metricName": metricName, "isMonotonic": "yep"}); perr == nil {
		t.Errorf("expected parse error for garbage isMonotonic")
	}
}

// --- N6: bad-webhook channel create -> success + warning note -> delete ---

func TestE2E_N6_ChannelTestSendFailure(t *testing.T) {
	h, ctx := e2eSetup(t)
	name := e2eRand() + "-badhook"

	// Create a webhook channel pointing at an unroutable URL so the test-send
	// fails. send_resolved given as a real bool to also exercise N3.
	createRes, err := h.handleCreateNotificationChannel(ctx, makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "webhook",
		"name":          name,
		"webhook_url":   "http://127.0.0.1:1/mcp-e2e-bad-hook",
		"send_resolved": false,
	}))
	if err != nil {
		t.Fatalf("create handler error: %v", err)
	}
	if createRes.IsError {
		// If creation itself fails we have nothing to clean up; report and stop.
		t.Fatalf("channel create returned IsError (expected fail-open success): %s", firstTextBlock(t, createRes))
	}
	createdID := e2eChannelID(t, firstTextBlock(t, createRes))
	if createdID == "" {
		t.Fatalf("could not extract channel id from create response; manual cleanup needed for name=%s", name)
	}
	t.Logf("created channel id=%s", createdID)
	// Register cleanup IMMEDIATELY so a later t.Fatalf can never orphan a real
	// channel. t.Cleanup runs even on failure/fatal.
	t.Cleanup(func() { e2eCleanupChannel(t, h, ctx, createdID) })

	// N6: must NOT be IsError; must carry a prominent warning note about the
	// failed test-send (the bad webhook can't be reached).
	if createRes.IsError {
		t.Errorf("N6: expected fail-open success, got IsError")
	}
	note := noteBlocks(createRes)
	body := firstTextBlock(t, createRes)
	// The test-send may pass or fail depending on backend egress, but the
	// embedded test_notification field must be present either way.
	if !strings.Contains(body, "test_notification") {
		t.Errorf("expected test_notification in body")
	}
	if strings.Contains(body, `"success":false`) {
		if !strings.Contains(note, "WARNING") {
			t.Errorf("N6: test failed but no prominent WARNING note; notes=%q", note)
		} else {
			t.Logf("N6 warning note surfaced: %s", strings.TrimSpace(note))
		}
	} else {
		t.Logf("N6: backend reported test-send success for unroutable URL (egress-dependent); warning-note path not exercised this run")
	}
}

// TestE2E_N6_NormalChannelLifecycle: normal create -> verify -> delete -> gone.
func TestE2E_N6_NormalChannelLifecycle(t *testing.T) {
	h, ctx := e2eSetup(t)
	name := e2eRand() + "-ok"

	// Use a webhook to a benign-but-likely-reachable URL; we don't assert the
	// test-send succeeds (egress-dependent), only the lifecycle round-trips.
	createRes, err := h.handleCreateNotificationChannel(ctx, makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":          "webhook",
		"name":          name,
		"webhook_url":   "https://example.com/mcp-e2e-ok",
		"send_resolved": "true",
	}))
	if err != nil {
		t.Fatalf("create handler error: %v", err)
	}
	if createRes.IsError {
		t.Fatalf("channel create failed: %s", firstTextBlock(t, createRes))
	}
	createdID := e2eChannelID(t, firstTextBlock(t, createRes))
	if createdID == "" {
		t.Fatalf("could not extract channel id; manual cleanup needed for name=%s", name)
	}
	t.Logf("created channel id=%s name=%s", createdID, name)
	// Register cleanup IMMEDIATELY so a later t.Fatalf can never orphan a real
	// channel. t.Cleanup runs even on failure/fatal.
	t.Cleanup(func() { e2eCleanupChannel(t, h, ctx, createdID) })

	// Verify via GET that name round-tripped server-side.
	getRes, err := h.handleGetNotificationChannel(ctx, makeToolRequest("signoz_get_notification_channel", map[string]any{"id": createdID}))
	if err != nil {
		t.Fatalf("get handler error: %v", err)
	}
	if getRes.IsError {
		t.Errorf("get channel failed: %s", firstTextBlock(t, getRes))
	} else if !strings.Contains(firstTextBlock(t, getRes), name) {
		t.Errorf("channel name %q did not round-trip server-side; GET body: %s", name, truncForLog(firstTextBlock(t, getRes)))
	} else {
		t.Logf("channel name round-tripped server-side")
	}
}

// --- N5: normal list tools succeed (forcing truly-empty is impractical) ---

func TestE2E_N5_ListsSucceed(t *testing.T) {
	h, ctx := e2eSetup(t)

	dbRes, err := h.handleListDashboards(ctx, makeToolRequest("signoz_list_dashboards", map[string]any{"limit": "5"}))
	if err != nil {
		t.Fatalf("list_dashboards handler error: %v", err)
	}
	if dbRes.IsError {
		t.Errorf("list_dashboards failed: %s", firstTextBlock(t, dbRes))
	} else if !json.Valid([]byte(firstTextBlock(t, dbRes))) {
		t.Errorf("list_dashboards body not valid JSON")
	}

	chRes, err := h.handleListNotificationChannels(ctx, makeToolRequest("signoz_list_notification_channels", map[string]any{"limit": "5"}))
	if err != nil {
		t.Fatalf("list_notification_channels handler error: %v", err)
	}
	if chRes.IsError {
		t.Errorf("list_notification_channels failed: %s", firstTextBlock(t, chRes))
	} else if !json.Valid([]byte(firstTextBlock(t, chRes))) {
		t.Errorf("list_notification_channels body not valid JSON")
	}
	t.Log("N5: cannot force a truly-empty list on staging; verified normal list paths succeed and coerce-to-empty branch is covered by unit tests")
}

// --- helpers ---

// e2eCleanupChannel deletes a channel and confirms it is gone. It is designed
// to run via t.Cleanup (registered the moment a non-empty id is known), so it
// must never orphan a real resource: it uses t.Errorf (not t.Fatalf) so a
// failure is recorded without aborting other cleanups, and it tolerates an
// already-deleted channel (a delete error / non-error GET is reported, not
// fatal). Cleanups run in LIFO order even when the test fails or t.Fatalf-s.
func e2eCleanupChannel(t *testing.T, h *Handler, ctx context.Context, id string) {
	t.Helper()
	if id == "" {
		return
	}
	delRes, err := h.handleDeleteNotificationChannel(ctx, makeToolRequest("signoz_delete_notification_channel", map[string]any{"id": id}))
	if err != nil {
		t.Errorf("CLEANUP: delete handler error for id=%s: %v (manual cleanup may be needed)", id, err)
		return
	}
	if delRes.IsError {
		t.Errorf("CLEANUP: DELETE failed for id=%s (manual cleanup may be needed): %s", id, firstTextBlock(t, delRes))
		return
	}
	// Confirm gone: a follow-up GET should error (404).
	getRes, err := h.handleGetNotificationChannel(ctx, makeToolRequest("signoz_get_notification_channel", map[string]any{"id": id}))
	if err != nil {
		t.Errorf("CLEANUP: confirm-gone GET handler error for id=%s: %v", id, err)
		return
	}
	if !getRes.IsError {
		t.Errorf("CLEANUP NOT CONFIRMED: channel id=%s still fetchable after delete", id)
	} else {
		t.Logf("confirmed channel id=%s deleted (GET now errors)", id)
	}
}

func e2eChannelID(t *testing.T, body string) string {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return ""
	}
	// Create returns {"channel": {...createResp...}, "test_notification": {...}}.
	// createResp is typically {"data":{"id":"..."}} or {"id":"..."}.
	if ch, ok := resp["channel"].(map[string]any); ok {
		if id := digID(ch); id != "" {
			return id
		}
	}
	return digID(resp)
}

// digID extracts an "id" from a map, descending into a nested "data" object.
func digID(m map[string]any) string {
	if id, ok := m["id"].(string); ok && id != "" {
		return id
	}
	if data, ok := m["data"].(map[string]any); ok {
		if id, ok := data["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

func firstAlertRuleID(t *testing.T, body string) string {
	t.Helper()
	var resp struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return ""
	}
	for _, raw := range resp.Data {
		var rule struct {
			RuleID string `json:"ruleId"`
			ID     string `json:"id"`
		}
		if err := json.Unmarshal(raw, &rule); err == nil {
			if rule.RuleID != "" {
				return rule.RuleID
			}
			if rule.ID != "" {
				return rule.ID
			}
		}
	}
	return ""
}

func e2eFindTraceID(t *testing.T, h *Handler, ctx context.Context) string {
	t.Helper()
	res, err := h.handleSearchTraces(ctx, makeToolRequest("signoz_search_traces", map[string]any{
		"timeRange": "6h",
		"limit":     "5",
	}))
	if err != nil || res.IsError {
		return ""
	}
	body := firstTextBlock(t, res)
	// Walk data.data.results[].rows[].data.traceID (the same shape the row
	// counter uses); fall back to a loose scan for any "traceID"/"trace_id".
	var env struct {
		Data struct {
			Data struct {
				Results []struct {
					Rows []struct {
						Data map[string]any `json:"data"`
					} `json:"rows"`
				} `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err == nil {
		for _, r := range env.Data.Data.Results {
			for _, row := range r.Rows {
				for _, k := range []string{"traceID", "trace_id", "traceId"} {
					if v, ok := row.Data[k].(string); ok && v != "" {
						return v
					}
				}
			}
		}
	}
	return ""
}

func e2eFirstMetricName(t *testing.T, h *Handler, ctx context.Context) string {
	t.Helper()
	res, err := h.handleListMetrics(ctx, makeToolRequest("signoz_list_metrics", map[string]any{"limit": "5"}))
	if err != nil || res.IsError {
		return ""
	}
	var env struct {
		Data struct {
			Metrics []struct {
				MetricName string `json:"metricName"`
			} `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(firstTextBlock(t, res)), &env); err == nil {
		for _, m := range env.Data.Metrics {
			if m.MetricName != "" {
				return m.MetricName
			}
		}
	}
	return ""
}

func truncForLog(s string) string {
	const max = 400
	if len(s) > max {
		return s[:max] + "...(truncated)"
	}
	return s
}
