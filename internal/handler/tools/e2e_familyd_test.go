//go:build e2e

// Package tools — Family D live end-to-end verification.
//
// This test exercises the Family D changes (param schema, types &
// descriptions, tracking #367) against a real SigNoz instance. It is gated by
// the `e2e` build tag AND by env vars, so it never runs in the normal unit
// suite and carries no embedded secret.
//
// Run:
//
//	SIGNOZ_E2E_URL="https://app.us.staging.signoz.cloud" \
//	SIGNOZ_E2E_TOKEN="<session-jwt>" \
//	go test -tags e2e -run TestFamilyD_E2E -v ./internal/handler/tools/
//
// SIGNOZ_E2E_TOKEN is the raw JWT (no "Bearer " prefix); the test adds the
// scheme. The token is read from the environment only — it is never written to
// a file, committed, or logged.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/mark3labs/mcp-go/mcp"
)

// The e2e helpers below carry a "D" suffix (e2eEnvD / e2eHandlerD / firstTextD)
// so they stay unique within the shared `tools` test package once every family
// branch's e2e_family*_test.go file lands on main — sibling branches declare
// their own e2eEnv/e2eHandler/firstText, which would otherwise be duplicate
// top-level declarations in the same package.

// e2eEnvD returns the staging base URL and a ready-to-use Authorization header
// value ("Bearer <jwt>"), or skips the test when either env var is absent.
func e2eEnvD(t *testing.T) (baseURL, authValue string) {
	t.Helper()
	baseURL = strings.TrimSpace(os.Getenv("SIGNOZ_E2E_URL"))
	token := strings.TrimSpace(os.Getenv("SIGNOZ_E2E_TOKEN"))
	if baseURL == "" || token == "" {
		t.Skip("SIGNOZ_E2E_URL and SIGNOZ_E2E_TOKEN must be set to run Family D e2e tests")
	}
	if n, err := util.NormalizeSigNozURL(baseURL); err == nil {
		baseURL = n
	}
	return baseURL, "Bearer " + token
}

// e2eHandlerD builds a Handler backed by a real SigNoz client speaking to the
// staging instance with a session-JWT Authorization header.
func e2eHandlerD(t *testing.T) *Handler {
	t.Helper()
	baseURL, authValue := e2eEnvD(t)
	client := signozclient.NewClient(logpkg.New("error"), baseURL, authValue, "Authorization", nil)
	return &Handler{
		logger:         logpkg.New("error"),
		clientOverride: client,
	}
}

func e2eCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 90*time.Second)
}

func mustNoToolError(t *testing.T, res *mcp.CallToolResult, label string) {
	t.Helper()
	if res == nil {
		t.Fatalf("%s: nil result", label)
	}
	if res.IsError {
		t.Fatalf("%s: tool returned error: %s", label, firstTextD(t, res))
	}
}

func firstTextD(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		return ""
	}
	return tc.Text
}

// ---------------------------------------------------------------------------
// search_docs: new searchText param + legacy query alias (N12)
//
// The docs index is a local bleve index (not a staging concern), so this flow
// uses the in-process docs test corpus — but it verifies the exact Family D
// behavior: the canonical searchText param and the permanent legacy query
// alias both return results.
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_SearchDocsParamAndAlias(t *testing.T) {
	// Build a local docs index (mirrors newDocsTestHandler in docs_test.go).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg, err := docsindex.NewIndexRegistry(ctx, docsHandlerSnapshot())
	if err != nil {
		t.Fatalf("build docs index: %v", err)
	}
	defer reg.Close(context.Background())

	h := &Handler{logger: logpkg.New("error")}
	h.SetDocsIndex(reg)

	t.Run("canonical searchText", func(t *testing.T) {
		res, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"searchText": "docker collector logs",
			"limit":      5,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		mustNoToolError(t, res, "searchText")
		sr := res.StructuredContent.(docsindex.SearchResponse)
		if len(sr.Results) == 0 {
			t.Fatalf("searchText returned no results")
		}
	})

	t.Run("legacy query alias", func(t *testing.T) {
		res, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"query": "docker collector logs",
			"limit": 5,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		mustNoToolError(t, res, "query-alias")
		sr := res.StructuredContent.(docsindex.SearchResponse)
		if len(sr.Results) == 0 {
			t.Fatalf("legacy query alias returned no results")
		}
	})
}

// ---------------------------------------------------------------------------
// get_service_top_operations: tags as real array AND legacy JSON-string (N14)
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_ServiceTopOperationsTags(t *testing.T) {
	h := e2eHandlerD(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	service := firstLiveServiceName(t, h, ctx)
	if service == "" {
		t.Skip("no services found on staging; cannot exercise get_service_top_operations")
	}
	t.Logf("using live service: %q", service)

	// Empty tags (baseline) must work.
	resBase, err := h.handleGetServiceTopOperations(ctx, makeToolRequest("signoz_get_service_top_operations", map[string]any{
		"service":   service,
		"timeRange": "6h",
	}))
	if err != nil {
		t.Fatalf("baseline call error: %v", err)
	}
	mustNoToolError(t, resBase, "tags-empty")

	// Real array form (the new canonical schema). Empty array filters nothing,
	// so it must round-trip to the same successful shape.
	resArr, err := h.handleGetServiceTopOperations(ctx, makeToolRequest("signoz_get_service_top_operations", map[string]any{
		"service":   service,
		"timeRange": "6h",
		"tags":      []any{},
	}))
	if err != nil {
		t.Fatalf("array-tags call error: %v", err)
	}
	mustNoToolError(t, resArr, "tags-array")

	// Legacy JSON-array string form (back-compat).
	resStr, err := h.handleGetServiceTopOperations(ctx, makeToolRequest("signoz_get_service_top_operations", map[string]any{
		"service":   service,
		"timeRange": "6h",
		"tags":      "[]",
	}))
	if err != nil {
		t.Fatalf("string-tags call error: %v", err)
	}
	mustNoToolError(t, resStr, "tags-json-string")

	// Both tag shapes must produce a parseable top-operations payload.
	for label, res := range map[string]*mcp.CallToolResult{"array": resArr, "json-string": resStr} {
		body := firstTextD(t, res)
		var probe any
		if err := json.Unmarshal([]byte(body), &probe); err != nil {
			t.Fatalf("%s tags: response not valid JSON: %v", label, err)
		}
	}
	t.Logf("get_service_top_operations: array and JSON-string tag shapes both succeeded and round-tripped")
}

func firstLiveServiceName(t *testing.T, h *Handler, ctx context.Context) string {
	t.Helper()
	res, err := h.handleListServices(ctx, makeToolRequest("signoz_list_services", map[string]any{
		"timeRange": "6h",
		"limit":     "50",
	}))
	if err != nil {
		t.Fatalf("list_services error: %v", err)
	}
	mustNoToolError(t, res, "list_services")

	var wrapper struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(firstTextD(t, res)), &wrapper); err != nil {
		t.Fatalf("list_services: parse paginated response: %v", err)
	}
	for _, m := range wrapper.Data {
		if name, _ := m["serviceName"].(string); name != "" {
			return name
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Enum params accept valid values (N15 stable sets)
// requestType (scalar/time_series) on aggregate_logs / aggregate_traces.
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_RequestTypeEnumValues(t *testing.T) {
	h := e2eHandlerD(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	for _, signal := range []string{"logs", "traces"} {
		for _, rt := range []string{"scalar", "time_series"} {
			tool := "signoz_aggregate_" + signal
			res, err := h.callAggregate(ctx, tool, map[string]any{
				"aggregation": "count",
				"timeRange":   "30m",
				"requestType": rt,
			})
			if err != nil {
				t.Fatalf("%s requestType=%s error: %v", tool, rt, err)
			}
			mustNoToolError(t, res, tool+"/requestType="+rt)
		}
	}
	t.Logf("requestType enum (scalar, time_series) accepted on aggregate_logs and aggregate_traces")
}

func (h *Handler) callAggregate(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	req := makeToolRequest(tool, args)
	switch tool {
	case "signoz_aggregate_logs":
		return h.handleAggregateLogs(ctx, req)
	case "signoz_aggregate_traces":
		return h.handleAggregateTraces(ctx, req)
	default:
		panic("unknown aggregate tool " + tool)
	}
}

// ---------------------------------------------------------------------------
// signal enum (metrics/traces/logs) on get_field_keys / get_field_values (N15)
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_SignalEnumValues(t *testing.T) {
	h := e2eHandlerD(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	for _, signal := range []string{"metrics", "traces", "logs"} {
		res, err := h.handleGetFieldKeys(ctx, makeToolRequest("signoz_get_field_keys", map[string]any{
			"signal": signal,
		}))
		if err != nil {
			t.Fatalf("get_field_keys signal=%s error: %v", signal, err)
		}
		mustNoToolError(t, res, "get_field_keys/signal="+signal)
	}
	t.Logf("signal enum (metrics, traces, logs) accepted on get_field_keys")
}

// ---------------------------------------------------------------------------
// order (asc/desc) + state (firing/inactive) on get_alert_history (N15).
// Needs a real ruleId from list_alert_rules.
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_AlertHistoryEnums(t *testing.T) {
	h := e2eHandlerD(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	ruleID := firstLiveRuleID(t, h, ctx)
	if ruleID == "" {
		t.Skip("no alert rules found on staging; cannot exercise get_alert_history enums")
	}
	t.Logf("using live ruleId: %q", ruleID)

	for _, order := range []string{"asc", "desc"} {
		res, err := h.handleGetAlertHistory(ctx, makeToolRequest("signoz_get_alert_history", map[string]any{
			"ruleId":    ruleID,
			"timeRange": "24h",
			"order":     order,
		}))
		if err != nil {
			t.Fatalf("get_alert_history order=%s error: %v", order, err)
		}
		mustNoToolError(t, res, "get_alert_history/order="+order)
	}

	for _, state := range []string{"firing", "inactive"} {
		res, err := h.handleGetAlertHistory(ctx, makeToolRequest("signoz_get_alert_history", map[string]any{
			"ruleId":    ruleID,
			"timeRange": "24h",
			"state":     state,
		}))
		if err != nil {
			t.Fatalf("get_alert_history state=%s error: %v", state, err)
		}
		mustNoToolError(t, res, "get_alert_history/state="+state)
	}
	t.Logf("order (asc, desc) and state (firing, inactive) enums accepted on get_alert_history")
}

func firstLiveRuleID(t *testing.T, h *Handler, ctx context.Context) string {
	t.Helper()
	res, err := h.handleListAlertRules(ctx, makeToolRequest("signoz_list_alert_rules", map[string]any{"limit": "50"}))
	if err != nil {
		t.Fatalf("list_alert_rules error: %v", err)
	}
	mustNoToolError(t, res, "list_alert_rules")

	var wrapper struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(firstTextD(t, res)), &wrapper); err != nil {
		t.Fatalf("list_alert_rules: parse paginated response: %v", err)
	}
	for _, m := range wrapper.Data {
		if id, _ := m["ruleId"].(string); id != "" {
			return id
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Evolving free-string set: aggregation operators (N15 split).
// Confirms every operator we advertise (allowedAggregations / validAggregations)
// actually runs against staging, and reports any operator the backend rejects.
// This is the live counterpart to the in-code drift guard.
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_AggregationSetMatchesBackend(t *testing.T) {
	h := e2eHandlerD(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	// "count" and "rate" need no field; the rest need an aggregateOn. Use a
	// field that exists on traces everywhere.
	const numericField = "durationNano"
	var rejected []string
	for agg := range validAggregations {
		args := map[string]any{
			"aggregation": agg,
			"timeRange":   "30m",
			"requestType": "scalar",
		}
		if !aggregationsWithoutField[agg] {
			args["aggregateOn"] = numericField
		}
		res, err := h.handleAggregateTraces(ctx, makeToolRequest("signoz_aggregate_traces", args))
		if err != nil {
			t.Fatalf("aggregate_traces agg=%s transport error: %v", agg, err)
		}
		if res.IsError {
			t.Logf("DRIFT: aggregation %q rejected by backend: %s", agg, firstTextD(t, res))
			rejected = append(rejected, agg)
		}
	}
	if len(rejected) > 0 {
		t.Errorf("aggregation drift: backend rejected operators we advertise: %v", rejected)
	} else {
		t.Logf("aggregation set matches backend: all advertised operators accepted (%s)", allowedAggregations)
	}
}

// ---------------------------------------------------------------------------
// timeRange / stepInterval unified grammar (N26).
// Confirms the grammar the unified descriptions advertise actually works:
//   - minutes ('30m') on a tool, and '2h' on get_top_metrics (which previously
//     advertised neither minutes nor 1h),
//   - stepInterval seconds value on a time_series aggregate.
// ---------------------------------------------------------------------------

func TestFamilyD_E2E_TimeRangeAndStepIntervalGrammar(t *testing.T) {
	h := e2eHandlerD(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	t.Run("get_top_metrics accepts 2h (minutes/hours grammar)", func(t *testing.T) {
		res, err := h.handleGetTopMetrics(ctx, makeToolRequest("signoz_get_top_metrics", map[string]any{
			"timeRange": "2h",
		}))
		if err != nil {
			t.Fatalf("get_top_metrics timeRange=2h error: %v", err)
		}
		mustNoToolError(t, res, "get_top_metrics/2h")
	})

	t.Run("get_top_metrics accepts minutes (30m)", func(t *testing.T) {
		res, err := h.handleGetTopMetrics(ctx, makeToolRequest("signoz_get_top_metrics", map[string]any{
			"timeRange": "30m",
		}))
		if err != nil {
			t.Fatalf("get_top_metrics timeRange=30m error: %v", err)
		}
		mustNoToolError(t, res, "get_top_metrics/30m")
	})

	t.Run("aggregate_logs time_series with stepInterval seconds", func(t *testing.T) {
		res, err := h.handleAggregateLogs(ctx, makeToolRequest("signoz_aggregate_logs", map[string]any{
			"aggregation":  "count",
			"timeRange":    "1h",
			"requestType":  "time_series",
			"stepInterval": "3600",
		}))
		if err != nil {
			t.Fatalf("aggregate_logs stepInterval=3600 error: %v", err)
		}
		mustNoToolError(t, res, "aggregate_logs/stepInterval")
	})
}
