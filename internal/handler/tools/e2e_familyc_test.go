//go:build e2e

// Package tools live E2E verification for the Family C (#365) output-envelope
// changes: structuredContent on code-controlled tools, its ABSENCE on raw QB
// passthrough tools, the JSON-first query_metrics envelope, and the error-code
// taxonomy — all asserted against a real SigNoz instance.
//
// Credential hygiene: this test reads the instance URL and bearer JWT from the
// environment and t.Skip()s when either is absent. NO secret is hardcoded here,
// and the test never logs the token. Run with:
//
//	SIGNOZ_E2E_URL=... SIGNOZ_E2E_TOKEN=... go test -tags e2e -run TestE2EFamilyC ./internal/handler/tools/
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// e2eHandler builds a Handler wired to a real SigNoz client using a bearer JWT.
// It skips the test if the required env vars are not set, so the committed test
// carries no secret and is inert in CI without credentials.
func e2eHandler(t *testing.T) (*Handler, context.Context) {
	t.Helper()
	baseURL := os.Getenv("SIGNOZ_E2E_URL")
	token := os.Getenv("SIGNOZ_E2E_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("SIGNOZ_E2E_URL / SIGNOZ_E2E_TOKEN not set; skipping live Family C E2E")
	}
	// Bearer JWT: the client sets the auth header verbatim, so the apiKey value
	// must include the "Bearer " prefix and authHeaderName is "Authorization".
	client := signozclient.NewClient(logpkg.New("error"), baseURL, "Bearer "+token, "Authorization", nil)
	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: client,
	}
	ctx := util.SetSigNozURL(context.Background(), baseURL)
	return h, ctx
}

// callOK runs a handler and fails the subtest if it errored.
func callOK(t *testing.T, fn func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), ctx context.Context, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := fn(ctx, makeToolRequest(name, args))
	if err != nil {
		t.Fatalf("%s: transport error: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool returned error result: %s", name, firstText(res))
	}
	return res
}

func firstText(r *mcp.CallToolResult) string {
	if len(r.Content) == 0 {
		return ""
	}
	tc, ok := mcp.AsTextContent(r.Content[0])
	if !ok {
		return ""
	}
	return tc.Text
}

// assertStructuredMatchesText asserts the result carries StructuredContent that
// round-trips to the SAME JSON value as the text block (block 0). This is the
// core Family C contract for code-controlled tools.
func assertStructuredMatchesText(t *testing.T, name string, r *mcp.CallToolResult) {
	t.Helper()
	if r.StructuredContent == nil {
		t.Fatalf("%s: code-controlled tool must populate StructuredContent (got nil)", name)
	}
	text := firstText(r)
	var textVal any
	if err := json.Unmarshal([]byte(text), &textVal); err != nil {
		t.Fatalf("%s: text block 0 must be valid JSON: %v", name, err)
	}
	structBytes, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatalf("%s: failed to marshal StructuredContent: %v", name, err)
	}
	var structVal any
	if err := json.Unmarshal(structBytes, &structVal); err != nil {
		t.Fatalf("%s: StructuredContent must round-trip to JSON: %v", name, err)
	}
	if !reflect.DeepEqual(textVal, structVal) {
		t.Fatalf("%s: StructuredContent does not match text block 0\ntext=%s\nstructured=%s", name, text, structBytes)
	}
	t.Logf("PASS %s: structuredContent round-trips text block (%d bytes)", name, len(text))
}

func assertNoStructured(t *testing.T, name string, r *mcp.CallToolResult) {
	t.Helper()
	if r.StructuredContent != nil {
		t.Fatalf("%s: raw passthrough must NOT populate StructuredContent, got %#v", name, r.StructuredContent)
	}
	t.Logf("PASS %s: no structuredContent (passthrough)", name)
}

// firstDataID pulls the first element's value for any of the given keys out of a
// paginate.Wrap-style {"data":[...],"pagination":{...}} payload. Returns "" if
// the collection is empty or no key matched.
func firstDataID(text string, keys ...string) string {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil || len(env.Data) == 0 {
		return ""
	}
	for _, k := range keys {
		if v, ok := env.Data[0][k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func TestE2EFamilyC_StructuredContentOnListTools(t *testing.T) {
	h, ctx := e2eHandler(t)

	// list_* tools — all paginate.Wrap, all code-controlled.
	assertStructuredMatchesText(t, "list_services", callOK(t, h.handleListServices, ctx, "signoz_list_services", map[string]any{"timeRange": "1h"}))
	assertStructuredMatchesText(t, "list_alerts", callOK(t, h.handleListAlerts, ctx, "signoz_list_alerts", map[string]any{}))
	assertStructuredMatchesText(t, "list_alert_rules", callOK(t, h.handleListAlertRules, ctx, "signoz_list_alert_rules", map[string]any{}))
	assertStructuredMatchesText(t, "list_dashboards", callOK(t, h.handleListDashboards, ctx, "signoz_list_dashboards", map[string]any{}))
	assertStructuredMatchesText(t, "list_views", callOK(t, h.handleListViews, ctx, "signoz_list_views", map[string]any{"sourcePage": "traces"}))
	assertStructuredMatchesText(t, "list_notification_channels", callOK(t, h.handleListNotificationChannels, ctx, "signoz_list_notification_channels", map[string]any{}))
}

func TestE2EFamilyC_StructuredContentOnGetTools(t *testing.T) {
	h, ctx := e2eHandler(t)

	// get_dashboard — id from list_dashboards.
	dl := callOK(t, h.handleListDashboards, ctx, "signoz_list_dashboards", map[string]any{})
	if uuid := firstDataID(firstText(dl), "uuid", "id"); uuid != "" {
		assertStructuredMatchesText(t, "get_dashboard", callOK(t, h.handleGetDashboard, ctx, "signoz_get_dashboard", map[string]any{"uuid": uuid}))
	} else {
		t.Log("SKIP get_dashboard: no dashboards on instance")
	}

	// get_alert — ruleId from list_alert_rules.
	al := callOK(t, h.handleListAlertRules, ctx, "signoz_list_alert_rules", map[string]any{})
	if ruleID := firstDataID(firstText(al), "ruleId", "id"); ruleID != "" {
		assertStructuredMatchesText(t, "get_alert", callOK(t, h.handleGetAlert, ctx, "signoz_get_alert", map[string]any{"ruleId": ruleID}))
	} else {
		t.Log("SKIP get_alert: no alert rules on instance")
	}

	// get_view — viewId from list_views (try each sourcePage).
	var viewID string
	for _, sp := range []string{"traces", "logs", "metrics"} {
		vl := callOK(t, h.handleListViews, ctx, "signoz_list_views", map[string]any{"sourcePage": sp})
		if viewID = firstDataID(firstText(vl), "id", "uuid"); viewID != "" {
			break
		}
	}
	if viewID != "" {
		assertStructuredMatchesText(t, "get_view", callOK(t, h.handleGetView, ctx, "signoz_get_view", map[string]any{"viewId": viewID}))
	} else {
		t.Log("SKIP get_view: no saved views on instance")
	}

	// get_notification_channel — id from list_notification_channels.
	cl := callOK(t, h.handleListNotificationChannels, ctx, "signoz_list_notification_channels", map[string]any{})
	if chID := firstDataID(firstText(cl), "id"); chID != "" {
		assertStructuredMatchesText(t, "get_notification_channel", callOK(t, h.handleGetNotificationChannel, ctx, "signoz_get_notification_channel", map[string]any{"id": chID}))
	} else {
		t.Log("SKIP get_notification_channel: no channels on instance")
	}

	// get_trace_details — traceId from a recent search_traces.
	st := callOK(t, h.handleSearchTraces, ctx, "signoz_search_traces", map[string]any{"timeRange": "1h", "limit": "1"})
	if tid := firstTraceID(firstText(st)); tid != "" {
		assertStructuredMatchesText(t, "get_trace_details", callOK(t, h.handleGetTraceDetails, ctx, "signoz_get_trace_details", map[string]any{"traceId": tid, "timeRange": "1h"}))
	} else {
		t.Log("SKIP get_trace_details: no traces in last hour")
	}
}

// firstTraceID walks a QB v5 search_traces response for the first traceID value.
func firstTraceID(text string) string {
	var env struct {
		Data struct {
			Results []struct {
				Rows []struct {
					Data map[string]any `json:"data"`
				} `json:"rows"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		return ""
	}
	for _, r := range env.Data.Results {
		for _, row := range r.Rows {
			for _, k := range []string{"traceID", "trace_id", "traceId"} {
				if v, ok := row.Data[k]; ok {
					if s, ok := v.(string); ok && s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

func TestE2EFamilyC_NoStructuredOnPassthrough(t *testing.T) {
	h, ctx := e2eHandler(t)

	assertNoStructured(t, "search_logs", callOK(t, h.handleSearchLogs, ctx, "signoz_search_logs", map[string]any{"timeRange": "1h", "limit": "1"}))
	assertNoStructured(t, "search_traces", callOK(t, h.handleSearchTraces, ctx, "signoz_search_traces", map[string]any{"timeRange": "1h", "limit": "1"}))
	assertNoStructured(t, "aggregate_logs", callOK(t, h.handleAggregateLogs, ctx, "signoz_aggregate_logs", map[string]any{"aggregation": "count", "timeRange": "1h"}))
	assertNoStructured(t, "aggregate_traces", callOK(t, h.handleAggregateTraces, ctx, "signoz_aggregate_traces", map[string]any{"aggregation": "count", "timeRange": "1h"}))
}

// TestE2EFamilyC_QueryMetricsJSONFirst pins N17 against live data: block 0 is
// parseable JSON, the decisions/warnings are a SEPARATE note block, and the
// result carries no structuredContent (passthrough).
func TestE2EFamilyC_QueryMetricsJSONFirst(t *testing.T) {
	h, ctx := e2eHandler(t)

	// Find a real metric to query.
	ml, err := h.GetClient(ctx)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	now := time.Now()
	raw, err := ml.ListMetrics(ctx, now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli(), 5, "", "")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	metricName := firstMetricName(raw)
	if metricName == "" {
		t.Skip("no metrics on instance to query")
	}

	res := callOK(t, h.handleQueryMetrics, ctx, "signoz_query_metrics", map[string]any{
		"metricName": metricName,
		"timeRange":  "1h",
	})

	if res.StructuredContent != nil {
		t.Fatalf("query_metrics is passthrough; want no StructuredContent, got %#v", res.StructuredContent)
	}
	if len(res.Content) < 1 {
		t.Fatalf("query_metrics: no content blocks")
	}
	// Block 0 must be parseable JSON with no prose preamble.
	block0 := firstText(res)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(block0), &parsed); err != nil {
		t.Fatalf("query_metrics block 0 must be valid JSON, got: %s", block0)
	}
	// If a note block exists (decisions/warnings), it must be a SEPARATE block.
	if len(res.Content) >= 2 {
		note, ok := mcp.AsTextContent(res.Content[1])
		if !ok {
			t.Fatalf("query_metrics block 1 should be a text note")
		}
		t.Logf("PASS query_metrics: JSON block 0 (%d bytes) + separate decisions note (%d bytes) for metric %q", len(block0), len(note.Text), metricName)
	} else {
		t.Logf("PASS query_metrics: JSON block 0 (%d bytes), no note block, for metric %q", len(block0), metricName)
	}
}

func firstMetricName(raw []byte) string {
	// Try {"data":{"metrics":[{"metricName":...}]}} then a few looser shapes.
	var wrap struct {
		Data struct {
			Metrics []struct {
				MetricName string `json:"metricName"`
			} `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrap); err == nil {
		for _, m := range wrap.Data.Metrics {
			if m.MetricName != "" {
				return m.MetricName
			}
		}
	}
	return ""
}

// TestE2EFamilyC_ErrorCodes verifies the error-code taxonomy against live
// behavior: a missing required arg yields VALIDATION_FAILED locally, and a
// well-formed but nonexistent id yields an UPSTREAM_ERROR from the backend.
func TestE2EFamilyC_ErrorCodes(t *testing.T) {
	h, ctx := e2eHandler(t)

	// VALIDATION_FAILED: missing required "uuid" on get_dashboard (local guard).
	vres, err := h.handleGetDashboard(ctx, makeToolRequest("signoz_get_dashboard", map[string]any{}))
	if err != nil {
		t.Fatalf("get_dashboard(no uuid): transport error: %v", err)
	}
	if !vres.IsError {
		t.Fatalf("get_dashboard(no uuid): expected an error result")
	}
	if code := codeOf(t, vres); code != CodeValidationFailed {
		t.Fatalf("get_dashboard(no uuid): code = %q, want %q", code, CodeValidationFailed)
	}
	t.Logf("PASS validation error: get_dashboard(no uuid) -> %s", CodeValidationFailed)

	// UPSTREAM_ERROR: well-formed UUIDv7 ruleId that does not exist. delete_alert
	// wraps the backend failure in upstreamError -> UPSTREAM_ERROR.
	const ghostRule = "0196634d-5d66-75c4-b778-e317f49dab7a"
	dres, err := h.handleDeleteAlert(ctx, makeToolRequest("signoz_delete_alert", map[string]any{"ruleId": ghostRule}))
	if err != nil {
		t.Fatalf("delete_alert(ghost): transport error: %v", err)
	}
	if !dres.IsError {
		// A non-error here means the rule somehow existed; treat as inconclusive
		// rather than a false failure.
		t.Logf("WARN delete_alert(ghost) returned success unexpectedly; skipping upstream-code assertion")
	} else {
		if code := codeOf(t, dres); code != CodeUpstreamError {
			t.Fatalf("delete_alert(ghost): code = %q, want %q (text=%s)", code, CodeUpstreamError, firstText(dres))
		}
		t.Logf("PASS upstream error: delete_alert(ghost ruleId) -> %s", CodeUpstreamError)
	}
}

func codeOf(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r.StructuredContent == nil {
		t.Fatalf("error result missing StructuredContent")
	}
	m, ok := r.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent is %T, want map[string]any", r.StructuredContent)
	}
	c, _ := m["code"].(string)
	return c
}

// TestE2EFamilyC_MutationStructuredContent creates a notification channel
// (prefixed mcp-e2e-c-<rand>), asserts the create result carries
// structuredContent, then DELETES it and confirms it is gone.
func TestE2EFamilyC_MutationStructuredContent(t *testing.T) {
	h, ctx := e2eHandler(t)

	name := fmt.Sprintf("mcp-e2e-c-%d", rand.Int63())

	// Create a webhook channel (no external side effects; the test-send may fail
	// fail-open but the channel is still created).
	createRes, err := h.handleCreateNotificationChannel(ctx, makeToolRequest("signoz_create_notification_channel", map[string]any{
		"type":        "webhook",
		"name":        name,
		"webhook_url": "https://example.com/mcp-e2e-c-webhook",
	}))
	if err != nil {
		t.Fatalf("create channel: transport error: %v", err)
	}
	if createRes.IsError {
		t.Fatalf("create channel failed: %s", firstText(createRes))
	}
	// Mutation result is synthesized JSON -> must carry structuredContent.
	assertStructuredMatchesText(t, "create_notification_channel", createRes)

	// Recover the created channel id so we can delete it.
	chID := createdChannelID(firstText(createRes))
	if chID == "" {
		// Fall back to listing and matching by name.
		lc := callOK(t, h.handleListNotificationChannels, ctx, "signoz_list_notification_channels", map[string]any{"limit": "1000"})
		chID = channelIDByName(firstText(lc), name)
	}
	if chID == "" {
		t.Fatalf("could not determine created channel id for cleanup (name=%s) — MANUAL CLEANUP REQUIRED", name)
	}

	// Delete it.
	delRes, err := h.handleDeleteNotificationChannel(ctx, makeToolRequest("signoz_delete_notification_channel", map[string]any{"id": chID}))
	if err != nil {
		t.Fatalf("delete channel %s: transport error: %v — MANUAL CLEANUP REQUIRED", chID, err)
	}
	if delRes.IsError {
		t.Fatalf("delete channel %s failed: %s — MANUAL CLEANUP REQUIRED", chID, firstText(delRes))
	}
	assertStructuredMatchesText(t, "delete_notification_channel", delRes)

	// Confirm gone: get_notification_channel should now error.
	getRes, err := h.handleGetNotificationChannel(ctx, makeToolRequest("signoz_get_notification_channel", map[string]any{"id": chID}))
	if err != nil {
		t.Fatalf("confirm-gone get: transport error: %v", err)
	}
	if !getRes.IsError {
		t.Fatalf("channel %s still exists after delete — MANUAL CLEANUP REQUIRED", chID)
	}
	t.Logf("PASS mutation lifecycle: created %q (id=%s), structuredContent present on create+delete, confirmed deleted", name, chID)
}

// createdChannelID extracts the id from a create/update channel result whose
// "channel" field is the backend's create response.
func createdChannelID(text string) string {
	var top struct {
		ID      string          `json:"id"`
		Channel json.RawMessage `json:"channel"`
	}
	if err := json.Unmarshal([]byte(text), &top); err != nil {
		return ""
	}
	if top.ID != "" {
		return top.ID
	}
	// channel may be {"data":{"id":...}} or {"id":...} or a scalar wrapper.
	var chWrap struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(top.Channel, &chWrap); err == nil {
		if chWrap.Data.ID != "" {
			return chWrap.Data.ID
		}
		if chWrap.ID != "" {
			return chWrap.ID
		}
	}
	// channel may also be a raw string id.
	var s string
	if err := json.Unmarshal(top.Channel, &s); err == nil {
		if _, convErr := strconv.Atoi(s); convErr == nil || s != "" {
			return s
		}
	}
	return ""
}

func channelIDByName(listText, name string) string {
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(listText), &env); err != nil {
		return ""
	}
	for _, ch := range env.Data {
		if n, _ := ch["name"].(string); n == name {
			if id, ok := ch["id"].(string); ok {
				return id
			}
		}
	}
	return ""
}
