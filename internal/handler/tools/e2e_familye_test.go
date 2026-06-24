//go:build e2e

// Package tools live end-to-end tests for the Family E (#366) parameter
// changes (K1 timestamps, K3 limit, K4 requestType, K5 id rename).
//
// These tests talk to a REAL SigNoz instance. They are gated behind the `e2e`
// build tag AND read the instance URL + bearer token from the environment,
// skipping when either is absent. No credential is ever hardcoded, logged, or
// committed.
//
// Run:
//
//	SIGNOZ_E2E_URL=https://app.example.signoz.cloud \
//	SIGNOZ_E2E_TOKEN='<session-jwt>' \
//	go test -tags e2e -run E2EFamilyE ./internal/handler/tools/...
//
// Auth: the token is sent as `Authorization: Bearer <token>` (JWT session) by
// constructing the client with authHeaderName="Authorization" and
// apiKey="Bearer <token>" — exactly how the request path stamps it.
//
// Every resource the tests create is prefixed `mcp-e2e-e-<rand>` and deleted in
// a t.Cleanup, with a follow-up read asserting it is gone.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/mark3labs/mcp-go/mcp"
)

// e2eEnv reads the live instance URL + token from the environment, skipping the
// test (never failing) when either is missing. The token is returned only for
// in-memory use; it is never logged.
func e2eEnv(t *testing.T) (baseURL, token string) {
	t.Helper()
	baseURL = strings.TrimRight(os.Getenv("SIGNOZ_E2E_URL"), "/")
	token = os.Getenv("SIGNOZ_E2E_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("SIGNOZ_E2E_URL and SIGNOZ_E2E_TOKEN must both be set to run the Family E live e2e tests")
	}
	return baseURL, token
}

// e2eHandler builds a Handler whose GetClient returns a live SigNoz client
// authenticated with the bearer JWT, plus a context carrying the instance URL
// for webUrl enrichment.
func e2eHandler(t *testing.T) (*Handler, context.Context) {
	t.Helper()
	baseURL, token := e2eEnv(t)
	// JWT session auth: header name "Authorization", value "Bearer <token>".
	live := signozclient.NewClient(logpkg.New("error"), baseURL, "Bearer "+token, "Authorization", nil)
	h := &Handler{logger: logpkg.New("error"), clientOverride: live}
	ctx := util.SetSigNozURL(context.Background(), baseURL)
	return h, ctx
}

func e2eRandSuffix() string {
	return fmt.Sprintf("%d-%04d", time.Now().UnixNano(), rand.Intn(10000))
}

func e2eText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil || len(r.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := mcp.AsTextContent(r.Content[0])
	if !ok {
		t.Fatal("first content block is not text")
	}
	return tc.Text
}

// ---------------------------------------------------------------------------
// K1 — timestamp magnitude auto-detect over a real window
// ---------------------------------------------------------------------------

// TestE2EFamilyE_K1_TimestampAutoDetect runs search_logs over the same real
// ~1h window expressed as seconds, millis, and nanos. Auto-detect should make
// all three resolve to the same window and return without error.
func TestE2EFamilyE_K1_TimestampAutoDetect(t *testing.T) {
	h, ctx := e2eHandler(t)

	now := time.Now()
	endMs := now.UnixMilli()
	startMs := now.Add(-1 * time.Hour).UnixMilli()

	magnitudes := []struct {
		name       string
		start, end string
	}{
		{"seconds", strconv.FormatInt(startMs/1000, 10), strconv.FormatInt(endMs/1000, 10)},
		{"millis", strconv.FormatInt(startMs, 10), strconv.FormatInt(endMs, 10)},
		{"nanos", strconv.FormatInt(startMs*1e6, 10), strconv.FormatInt(endMs*1e6, 10)},
	}

	for _, m := range magnitudes {
		t.Run(m.name, func(t *testing.T) {
			req := makeToolRequest("signoz_search_logs", map[string]any{
				"start": m.start,
				"end":   m.end,
				"limit": "5",
			})
			res, err := h.handleSearchLogs(ctx, req)
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if res.IsError {
				t.Fatalf("%s magnitude returned an error result: %s", m.name, e2eText(t, res))
			}
			// Block 0 must be valid JSON (the raw QB payload).
			var parsed any
			if err := json.Unmarshal([]byte(e2eText(t, res)), &parsed); err != nil {
				t.Fatalf("%s magnitude: block 0 not valid JSON: %v", m.name, err)
			}
		})
	}
}

// TestE2EFamilyE_K1_ServicesNsAndMs confirms list_services and
// get_service_top_operations still work with ns AND ms windows after the
// ms→auto-detect migration, and that list_metrics works with the new timeRange.
func TestE2EFamilyE_K1_ServicesNsAndMs(t *testing.T) {
	h, ctx := e2eHandler(t)

	now := time.Now()
	endMs := now.UnixMilli()
	startMs := now.Add(-6 * time.Hour).UnixMilli()

	windows := map[string]map[string]any{
		"ns": {"start": strconv.FormatInt(startMs*1e6, 10), "end": strconv.FormatInt(endMs*1e6, 10)},
		"ms": {"start": strconv.FormatInt(startMs, 10), "end": strconv.FormatInt(endMs, 10)},
	}

	for name, w := range windows {
		t.Run("list_services_"+name, func(t *testing.T) {
			res, err := h.handleListServices(ctx, makeToolRequest("signoz_list_services", w))
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if res.IsError {
				t.Fatalf("list_services %s window error: %s", name, e2eText(t, res))
			}
		})
	}

	// get_service_top_operations needs a service name; pull the first one from
	// list_services if available, else skip the sub-test gracefully.
	listRes, err := h.handleListServices(ctx, makeToolRequest("signoz_list_services", windows["ms"]))
	if err == nil && !listRes.IsError {
		service := firstServiceName(e2eText(t, listRes))
		if service != "" {
			for name, w := range windows {
				args := map[string]any{"service": service}
				for k, v := range w {
					args[k] = v
				}
				t.Run("top_operations_"+name, func(t *testing.T) {
					res, err := h.handleGetServiceTopOperations(ctx, makeToolRequest("signoz_get_service_top_operations", args))
					if err != nil {
						t.Fatalf("transport error: %v", err)
					}
					if res.IsError {
						t.Fatalf("top_operations %s window error: %s", name, e2eText(t, res))
					}
				})
			}
		}
	}

	t.Run("list_metrics_timeRange", func(t *testing.T) {
		res, err := h.handleListMetrics(ctx, makeToolRequest("signoz_list_metrics", map[string]any{
			"timeRange": "1h",
			"limit":     "5",
		}))
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if res.IsError {
			t.Fatalf("list_metrics timeRange error: %s", e2eText(t, res))
		}
	})
}

func firstServiceName(body string) string {
	var page paginate.Response
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		return ""
	}
	for _, item := range page.Data {
		if m, ok := item.(map[string]any); ok {
			if name, _ := m["serviceName"].(string); name != "" {
				return name
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// K3 — limit (docs number-or-string; list-tool clamp)
// ---------------------------------------------------------------------------

// TestE2EFamilyE_K3_DocsLimitNumberAndString exercises search_docs with the
// limit as a JSON number AND a string against the embedded docs index (a
// self-contained, in-process index — no live SigNoz call needed).
func TestE2EFamilyE_K3_DocsLimitNumberAndString(t *testing.T) {
	// Still env-gated for consistency with the rest of the e2e suite.
	e2eEnv(t)

	h, cleanup := newDocsTestHandler(t)
	defer cleanup()

	for _, lim := range []any{float64(3), "3"} {
		res, err := h.handleSearchDocs(context.Background(), makeToolRequest("signoz_search_docs", map[string]any{
			"query": "docker",
			"limit": lim,
		}))
		if err != nil {
			t.Fatalf("limit %#v: transport error: %v", lim, err)
		}
		if res.IsError {
			t.Fatalf("limit %#v: error result: %s", lim, e2eText(t, res))
		}
	}
}

// TestE2EFamilyE_K3_ListClamp asks a list tool for limit > 1000 and asserts the
// clamp note is surfaced and the effective page size is MaxLimit.
func TestE2EFamilyE_K3_ListClamp(t *testing.T) {
	h, ctx := e2eHandler(t)

	res, err := h.handleListAlertRules(ctx, makeToolRequest("signoz_list_alert_rules", map[string]any{
		"limit": "999999",
	}))
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_alert_rules error: %s", e2eText(t, res))
	}
	// The effective limit must be clamped to MaxLimit in the pagination block.
	var page paginate.Response
	if err := json.Unmarshal([]byte(e2eText(t, res)), &page); err != nil {
		t.Fatalf("block 0 not a pagination response: %v", err)
	}
	if page.Pagination.Limit != paginate.MaxLimit {
		t.Fatalf("effective limit = %d, want clamped to %d", page.Pagination.Limit, paginate.MaxLimit)
	}
	// A clamp note must be present as a trailing block.
	foundNote := false
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok && strings.Contains(tc.Text, "clamped") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatal("expected a clamp note block when limit exceeds MaxLimit")
	}
}

// ---------------------------------------------------------------------------
// K4 — requestType (valid succeeds, unknown rejected) on both signals
// ---------------------------------------------------------------------------

func TestE2EFamilyE_K4_RequestTypeValidation(t *testing.T) {
	h, ctx := e2eHandler(t)

	t.Run("aggregate_logs_valid_scalar", func(t *testing.T) {
		res, err := h.handleAggregateLogs(ctx, makeToolRequest("signoz_aggregate_logs", map[string]any{
			"aggregation": "count",
			"timeRange":   "1h",
			"requestType": "scalar",
		}))
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if res.IsError {
			t.Fatalf("valid scalar aggregate_logs error: %s", e2eText(t, res))
		}
	})

	t.Run("aggregate_logs_unknown_rejected", func(t *testing.T) {
		res, err := h.handleAggregateLogs(ctx, makeToolRequest("signoz_aggregate_logs", map[string]any{
			"aggregation": "count",
			"timeRange":   "1h",
			"requestType": "totally-bogus",
		}))
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if !res.IsError {
			t.Fatal("unknown requestType should be rejected for aggregate_logs")
		}
		if !strings.Contains(e2eText(t, res), "requestType") {
			t.Fatalf("rejection should mention requestType, got: %s", e2eText(t, res))
		}
	})

	t.Run("query_metrics_unknown_rejected", func(t *testing.T) {
		res, err := h.handleQueryMetrics(ctx, makeToolRequest("signoz_query_metrics", map[string]any{
			"metricName":  "system.cpu.time",
			"timeRange":   "1h",
			"requestType": "totally-bogus",
		}))
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if !res.IsError {
			t.Fatal("unknown requestType should be rejected for query_metrics")
		}
		if !strings.Contains(e2eText(t, res), "requestType") {
			t.Fatalf("rejection should mention requestType, got: %s", e2eText(t, res))
		}
	})
}

// ---------------------------------------------------------------------------
// K5 — id rename: read existing via id + legacy; create→get→delete a throwaway
//       view via BOTH id and the legacy viewId key.
// ---------------------------------------------------------------------------

// TestE2EFamilyE_K5_ReadByIDAndLegacy reads the first existing alert rule and
// dashboard (if any) by the canonical "id" param AND the legacy key, confirming
// both alias paths reach the backend.
func TestE2EFamilyE_K5_ReadByIDAndLegacy(t *testing.T) {
	h, ctx := e2eHandler(t)

	// Alert rule: list, pick the first ruleId, get by id + ruleId.
	rulesRes, err := h.handleListAlertRules(ctx, makeToolRequest("signoz_list_alert_rules", map[string]any{}))
	if err == nil && !rulesRes.IsError {
		if ruleID := firstField(e2eText(t, rulesRes), "ruleId"); ruleID != "" {
			for _, key := range []string{"id", "ruleId"} {
				res, err := h.handleGetAlert(ctx, makeToolRequest("signoz_get_alert", map[string]any{key: ruleID}))
				if err != nil {
					t.Fatalf("get_alert via %q transport error: %v", key, err)
				}
				if res.IsError {
					t.Fatalf("get_alert via %q error: %s", key, e2eText(t, res))
				}
			}
		} else {
			t.Log("no existing alert rules to read; skipping get_alert alias check")
		}
	}

	// Dashboard: list, pick the first uuid, get by id + uuid.
	dashRes, err := h.handleListDashboards(ctx, makeToolRequest("signoz_list_dashboards", map[string]any{}))
	if err == nil && !dashRes.IsError {
		if uuid := firstField(e2eText(t, dashRes), "uuid"); uuid != "" {
			for _, key := range []string{"id", "uuid"} {
				res, err := h.handleGetDashboard(ctx, makeToolRequest("signoz_get_dashboard", map[string]any{key: uuid}))
				if err != nil {
					t.Fatalf("get_dashboard via %q transport error: %v", key, err)
				}
				if res.IsError {
					t.Fatalf("get_dashboard via %q error: %s", key, e2eText(t, res))
				}
			}
		} else {
			t.Log("no existing dashboards to read; skipping get_dashboard alias check")
		}
	}
}

// firstField pulls the first non-empty value of fieldName from the data array
// of a paginate.Response body.
func firstField(body, fieldName string) string {
	var page paginate.Response
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		return ""
	}
	for _, item := range page.Data {
		if m, ok := item.(map[string]any); ok {
			if v, _ := m[fieldName].(string); v != "" {
				return v
			}
		}
	}
	return ""
}

// TestE2EFamilyE_K5_ViewCRUDRoundTrip CLONES an existing saved view (rather than
// hand-crafting a payload, per CLAUDE.md), creates a throwaway copy with a
// unique mcp-e2e-e-<rand> name, reads it back via BOTH the canonical "id" and
// legacy "viewId" params, then deletes it via the legacy key and confirms it is
// gone. A t.Cleanup guarantees deletion even on failure. If no existing view is
// found on any sourcePage, the CRUD subtest is skipped (never hand-crafted).
func TestE2EFamilyE_K5_ViewCRUDRoundTrip(t *testing.T) {
	h, ctx := e2eHandler(t)

	// 1. Find a real existing view to clone its shape from. Try each sourcePage.
	srcViewID, srcSourcePage := findExistingView(t, h, ctx)
	if srcViewID == "" {
		t.Skip("no existing saved view found on any sourcePage to clone; skipping view CRUD round-trip (refusing to hand-craft a payload)")
	}

	// 2. Read the source view's full config.
	srcRes, err := h.handleGetView(ctx, makeToolRequest("signoz_get_view", map[string]any{"id": srcViewID}))
	if err != nil {
		t.Fatalf("get_view (source) transport error: %v", err)
	}
	if srcRes.IsError {
		t.Fatalf("get_view (source) error: %s", e2eText(t, srcRes))
	}
	srcData := extractViewData(e2eText(t, srcRes))
	if srcData == nil {
		t.Fatalf("could not extract source view data from: %s", e2eText(t, srcRes))
	}

	// 3. Build the create payload from THAT real shape, with a unique name.
	//    Server-populated fields (id/createdAt/...) are stripped by the create
	//    handler's marshalViewBody, so we can pass the cloned data as-is.
	name := "mcp-e2e-e-" + e2eRandSuffix()
	createArgs := map[string]any{}
	for k, v := range srcData {
		createArgs[k] = v
	}
	createArgs["name"] = name
	delete(createArgs, "category") // keep the clone minimal/unambiguous
	t.Logf("cloning existing %q view (sourcePage=%s) into %q", srcViewID, srcSourcePage, name)

	createRes, err := h.handleCreateView(ctx, makeToolRequest("signoz_create_view", createArgs))
	if err != nil {
		t.Fatalf("create_view transport error: %v", err)
	}
	if createRes.IsError {
		t.Fatalf("create_view (cloned shape) error: %s", e2eText(t, createRes))
	}
	viewID := extractViewID(e2eText(t, createRes))
	if viewID == "" {
		t.Fatalf("could not extract created view id from: %s", e2eText(t, createRes))
	}

	// Guaranteed cleanup + gone-confirmation.
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		_, _ = h.handleDeleteView(ctx, makeToolRequest("signoz_delete_view", map[string]any{"id": viewID}))
	})

	// Read back via BOTH the canonical id and the legacy viewId.
	for _, key := range []string{"id", "viewId"} {
		res, err := h.handleGetView(ctx, makeToolRequest("signoz_get_view", map[string]any{key: viewID}))
		if err != nil {
			t.Fatalf("get_view via %q transport error: %v", key, err)
		}
		if res.IsError {
			t.Fatalf("get_view via %q error: %s", key, e2eText(t, res))
		}
		if gotName := extractViewName(e2eText(t, res)); gotName != name {
			t.Fatalf("get_view via %q: name = %q, want %q (round-trip mismatch)", key, gotName, name)
		}
	}

	// Delete via the legacy key to prove the alias works on the mutation path.
	delRes, err := h.handleDeleteView(ctx, makeToolRequest("signoz_delete_view", map[string]any{"viewId": viewID}))
	if err != nil {
		t.Fatalf("delete_view transport error: %v", err)
	}
	if delRes.IsError {
		t.Fatalf("delete_view via legacy viewId error: %s", e2eText(t, delRes))
	}
	deleted = true

	// Confirm it is gone — a subsequent get must error.
	goneRes, err := h.handleGetView(ctx, makeToolRequest("signoz_get_view", map[string]any{"id": viewID}))
	if err != nil {
		t.Fatalf("post-delete get_view transport error: %v", err)
	}
	if !goneRes.IsError {
		t.Fatalf("view %s should be gone after delete, but get_view succeeded: %s", viewID, e2eText(t, goneRes))
	}
}

func extractViewID(body string) string {
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return ""
	}
	if env.Data.ID != "" {
		return env.Data.ID
	}
	return env.ID
}

func extractViewName(body string) string {
	var env struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return ""
	}
	if env.Data.Name != "" {
		return env.Data.Name
	}
	return env.Name
}

// findExistingView returns the id and sourcePage of the first existing saved
// view found across the standard sourcePages, or ("", "") if none exist. Used
// to clone a real view's shape rather than hand-craft a create payload.
func findExistingView(t *testing.T, h *Handler, ctx context.Context) (id, sourcePage string) {
	t.Helper()
	for _, sp := range []string{"traces", "logs", "metrics", "meter"} {
		res, err := h.handleListViews(ctx, makeToolRequest("signoz_list_views", map[string]any{
			"sourcePage": sp,
			"limit":      "1",
		}))
		if err != nil || res.IsError {
			continue
		}
		if vid := firstField(e2eText(t, res), "id"); vid != "" {
			return vid, sp
		}
	}
	return "", ""
}

// extractViewData returns the SavedView "data" object from a get_view response
// envelope ({"status":...,"data":{...}}), or the top-level object if there is
// no envelope. This is the real shape we clone into a new create_view payload.
func extractViewData(body string) map[string]any {
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err == nil && len(env.Data) > 0 {
		return env.Data
	}
	var flat map[string]any
	if err := json.Unmarshal([]byte(body), &flat); err == nil && len(flat) > 0 {
		return flat
	}
	return nil
}
