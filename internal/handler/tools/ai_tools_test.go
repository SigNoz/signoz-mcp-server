package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// recordedRequest is one request the fake SigNoz received.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
}

// newAIToolsHandler runs the real client against a fake SigNoz and records
// every request it receives.
func newAIToolsHandler(t *testing.T, serve func(w http.ResponseWriter, r *http.Request)) (*Handler, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		serve(w, r)
	}))
	t.Cleanup(srv.Close)
	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: signozclient.NewClient(logpkg.New("error"), srv.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	return h, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), requests...)
	}
}

func successJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, raw)
	}
	return out
}

const forbiddenBody = `{"status":"error","error":{"code":"forbidden","type":"forbidden","message":"only viewers and above can read this"}}`

func TestGetAIPricing_RulesAssembleTheRequestedPageFromUpstreamPages(t *testing.T) {
	const total = 120
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var offset, limit int
		_, _ = fmt.Sscan(r.URL.Query().Get("offset"), &offset)
		_, _ = fmt.Sscan(r.URL.Query().Get("limit"), &limit)
		limit = min(limit, aiPricingRulesUpstreamPageSize) // SigNoz clamps a page to 100 rules
		items := make([]string, 0, limit)
		for i := offset; i < min(offset+limit, total); i++ {
			items = append(items, fmt.Sprintf(`{"id":"rule-%d","modelName":"gpt-4o","provider":"openai","pricing":{"input":2.5,"output":10}}`, i))
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"items":[%s],"total":%d,"offset":%d,"limit":%d}}`, strings.Join(items, ","), total, offset, limit)
	})

	result, err := h.handleGetAIPricing(testCtx(), makeToolRequest("signoz_get_ai_pricing", map[string]any{"limit": "150", "searchText": "gpt"}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, result.Content)
	}

	for _, req := range requests() {
		query, _ := url.ParseQuery(req.Query)
		var limit int
		_, _ = fmt.Sscan(query.Get("limit"), &limit)
		if req.Path != "/api/v1/llm_pricing_rules" || query.Get("q") != "gpt" || limit < 1 || limit > aiPricingRulesUpstreamPageSize {
			t.Fatalf("request %s?%s: want the rules path, q=gpt, and a limit within SigNoz's 100-rule cap", req.Path, req.Query)
		}
	}
	out := successJSON(t, textContent(t, result))
	if out["view"] != "rules" {
		t.Fatalf("view = %v, want rules", out["view"])
	}
	data := out["data"].([]any)
	if len(data) != total {
		t.Fatalf("returned %d rules, want %d", len(data), total)
	}
	for i, item := range data {
		if id := item.(map[string]any)["id"]; id != fmt.Sprintf("rule-%d", i) {
			t.Fatalf("rule %d = %v, want rule-%d: rules must arrive in order without gaps or duplicates", i, id, i)
		}
	}
	pagination := out["pagination"].(map[string]any)
	if pagination["total"] != float64(total) || pagination["hasMore"] != false || pagination["nextOffset"] != float64(-1) {
		t.Fatalf("pagination = %#v, want total %d and no next page", pagination, total)
	}
}

func TestGetAIPricing_UnpricedModelsFilterAndPageLocally(t *testing.T) {
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"items":[
			{"modelName":"claude-sonnet-4","provider":"anthropic","spanCount":40},
			{"modelName":"gpt-4o-mini","provider":"openai","spanCount":12},
			{"modelName":"claude-haiku-4","provider":"anthropic","spanCount":3}]}}`))
	})

	result, err := h.handleGetAIPricing(testCtx(), makeToolRequest("signoz_get_ai_pricing", map[string]any{"view": "unpriced_models", "searchText": "Anthropic", "limit": 1}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, result.Content)
	}
	if got := requests(); len(got) != 1 || got[0].Path != "/api/v1/llm_pricing_rules/unmapped_models" {
		t.Fatalf("upstream requests = %#v", got)
	}
	out := successJSON(t, textContent(t, result))
	data := out["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["modelName"] != "claude-sonnet-4" {
		t.Fatalf("data = %#v, want the first anthropic model only", data)
	}
	pagination := out["pagination"].(map[string]any)
	if pagination["total"] != float64(2) || pagination["hasMore"] != true || pagination["nextOffset"] != float64(1) {
		t.Fatalf("pagination = %#v, want 2 matches with a next page at 1", pagination)
	}
}

func TestGetAIPricing_PermissionDeniedIsCoded(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(forbiddenBody))
	})
	result, err := h.handleGetAIPricing(testCtx(), makeToolRequest("signoz_get_ai_pricing", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if got := resultCode(t, result); got != CodePermissionDenied {
		t.Fatalf("code = %q, want %q", got, CodePermissionDenied)
	}
}

func TestGetAIPricing_RejectsInvalidArgumentsBeforeCallingSigNoz(t *testing.T) {
	for _, tc := range []struct {
		field string
		args  map[string]any
	}{
		{"view", map[string]any{"view": "prices"}},
		{"limit", map[string]any{"limit": "abc"}},
		{"offset", map[string]any{"offset": -1}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			h, requests := newAIToolsHandler(t, func(http.ResponseWriter, *http.Request) {})
			result, _ := h.handleGetAIPricing(testCtx(), makeToolRequest("signoz_get_ai_pricing", tc.args))
			if got := resultCode(t, result); got != CodeValidationFailed || !strings.Contains(resultText(t, result), `"`+tc.field+`"`) {
				t.Fatalf("code = %q text = %q, want a %s validation error", got, resultText(t, result), tc.field)
			}
			if len(requests()) != 0 {
				t.Fatalf("an invalid %s reached SigNoz", tc.field)
			}
		})
	}
}

func TestGetAIPricing_RulesWithoutTotalAreAnUpstreamError(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"items":[{"id":"rule-1"}]}}`))
	})
	result, _ := h.handleGetAIPricing(testCtx(), makeToolRequest("signoz_get_ai_pricing", map[string]any{}))
	if got := resultCode(t, result); got != CodeUpstreamError {
		t.Fatalf("code = %q, want %q: a page without a total must not look complete", got, CodeUpstreamError)
	}
}

const mappingGroupsBody = `{"status":"success","data":{"items":[
	{"id":"group-llm","name":"gen_ai.llm","enabled":true,"origin":"system","version":4,"condition":{"attributes":[{"value":"model","enabled":true}],"resource":[]}},
	{"id":"group-tool","name":"gen_ai.tool","enabled":false,"origin":"system","version":2,"condition":{"attributes":[{"value":"tool.name","enabled":true}],"resource":[]}}]}}`

const llmMappersBody = `{"status":"success","data":{"items":[{"id":"mapper-1","groupId":"group-llm","name":"gen_ai.request.model","fieldContext":"attribute","enabled":true,"origin":"system","config":{"sources":[{"key":"llm.model_name","context":"attribute","operation":"copy","priority":60,"enabled":true}]}}]}}`

func TestListAIAttributeMappings_OneGroupFailingLeavesTheOthersAndSaysSo(t *testing.T) {
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/span_mapper_groups":
			_, _ = w.Write([]byte(mappingGroupsBody))
		case "/api/v1/span_mapper_groups/group-llm/span_mappers":
			_, _ = w.Write([]byte(llmMappersBody))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"not_found","type":"not-found","message":"group not found"}}`))
		}
	})

	result, err := h.handleListAIAttributeMappings(testCtx(), makeToolRequest("signoz_list_ai_attribute_mappings", map[string]any{}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, result.Content)
	}
	if got := requests(); len(got) != 3 {
		t.Fatalf("upstream requests = %#v, want the group list and one mapper list per group", got)
	}
	out := successJSON(t, textContent(t, result))
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data = %#v, want both groups", data)
	}
	llm := data[0].(map[string]any)
	mappers, ok := llm["mappers"].([]any)
	if llm["name"] != "gen_ai.llm" || llm["origin"] != "system" || !ok || len(mappers) != 1 {
		t.Fatalf("first group = %#v, want gen_ai.llm with its fields and one mapper", llm)
	}
	if tool := data[1].(map[string]any); tool["mappers"] != nil {
		t.Fatalf("failed group mappers = %#v, want null", tool["mappers"])
	}
	failed := out["failedGroups"].([]any)
	if out["incomplete"] != true || len(failed) != 1 {
		t.Fatalf("incomplete = %v failedGroups = %#v, want one failed group", out["incomplete"], failed)
	}
	failure := failed[0].(map[string]any)
	if failure["groupId"] != "group-tool" || failure["code"] != CodeNotFound || failure["status"] != float64(http.StatusNotFound) {
		t.Fatalf("failure = %#v, want group-tool NOT_FOUND", failure)
	}
	if out["nextAction"] == "" {
		t.Fatal("an incomplete result must say what to do next")
	}
}

func TestListAIAttributeMappings_UnreadableMappersAreReportedAsFailed(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/span_mapper_groups":
			_, _ = w.Write([]byte(mappingGroupsBody))
		case "/api/v1/span_mapper_groups/group-llm/span_mappers":
			_, _ = w.Write([]byte(llmMappersBody))
		default:
			_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
		}
	})
	result, err := h.handleListAIAttributeMappings(testCtx(), makeToolRequest("signoz_list_ai_attribute_mappings", map[string]any{}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, result.Content)
	}
	out := successJSON(t, textContent(t, result))
	failed, _ := out["failedGroups"].([]any)
	if out["incomplete"] != true || len(failed) != 1 || failed[0].(map[string]any)["groupId"] != "group-tool" {
		t.Fatalf("incomplete = %v failedGroups = %#v, want group-tool reported as failed", out["incomplete"], failed)
	}
}

func TestListAIAttributeMappings_UnreadableGroupListIsAnUpstreamError(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	})
	result, _ := h.handleListAIAttributeMappings(testCtx(), makeToolRequest("signoz_list_ai_attribute_mappings", map[string]any{}))
	if got := resultCode(t, result); got != CodeUpstreamError {
		t.Fatalf("code = %q, want %q: an unreadable group list must not look like no groups", got, CodeUpstreamError)
	}
}

func TestListAIAttributeMappings_PermissionDeniedOnMappersFailsTheWholeCall(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/span_mapper_groups" {
			_, _ = w.Write([]byte(mappingGroupsBody))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(forbiddenBody))
	})
	result, err := h.handleListAIAttributeMappings(testCtx(), makeToolRequest("signoz_list_ai_attribute_mappings", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if got := resultCode(t, result); got != CodePermissionDenied {
		t.Fatalf("code = %q, want %q", got, CodePermissionDenied)
	}
}

const mappingTestResponse = `{"status":"success","data":{"spans":[{"attributes":{"llm.model_name":"gpt-4o","gen_ai.request.model":"gpt-4o"},"resource":{}}],"collectorLogs":[]}}`

func TestTestAIAttributeMapping_DefaultsToTheSavedGroups(t *testing.T) {
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(mappingGroupsBody))
			return
		}
		_, _ = w.Write([]byte(mappingTestResponse))
	})
	spans := []any{map[string]any{"attributes": map[string]any{"llm.model_name": "gpt-4o"}}}

	result, err := h.handleTestAIAttributeMapping(testCtx(), makeToolRequest("signoz_test_ai_attribute_mapping", map[string]any{"spans": spans}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, result.Content)
	}
	got := requests()
	if len(got) != 2 || got[1].Method != http.MethodPost || got[1].Path != "/api/v1/span_mapper_groups/test" {
		t.Fatalf("upstream requests = %#v, want the group list then the test call", got)
	}
	// Groups go without mappers so SigNoz loads each one's saved mappers by name.
	wantBody := `{"spans":[{"attributes":{"llm.model_name":"gpt-4o"}}],"groups":[
		{"name":"gen_ai.llm","enabled":true,"condition":{"attributes":[{"value":"model","enabled":true}],"resource":[]}},
		{"name":"gen_ai.tool","enabled":false,"condition":{"attributes":[{"value":"tool.name","enabled":true}],"resource":[]}}]}`
	assertJSONEqual(t, wantBody, got[1].Body)

	out := successJSON(t, textContent(t, result))
	if out["groupSource"] != "saved" {
		t.Fatalf("groupSource = %v, want saved", out["groupSource"])
	}
	if tested := out["testedGroups"].([]any); len(tested) != 1 || tested[0] != "gen_ai.llm" {
		t.Fatalf("testedGroups = %#v, want only the enabled group", tested)
	}
	mapped := out["spans"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	if mapped["gen_ai.request.model"] != "gpt-4o" {
		t.Fatalf("mapped span = %#v, want SigNoz's mapped attributes", mapped)
	}
}

func TestTestAIAttributeMapping_SendsProvidedGroupsVerbatim(t *testing.T) {
	h, requests := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mappingTestResponse))
	})
	groups := []any{map[string]any{
		"name":      "draft",
		"enabled":   true,
		"condition": map[string]any{"attributes": []any{map[string]any{"value": "model", "enabled": true}}, "resource": []any{}},
		"mappers":   []any{},
	}}
	spans := []any{map[string]any{"attributes": map[string]any{"model": "gpt-4o"}}}

	result, err := h.handleTestAIAttributeMapping(testCtx(), makeToolRequest("signoz_test_ai_attribute_mapping", map[string]any{"spans": spans, "groups": groups}))
	if err != nil || result.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, result.Content)
	}
	got := requests()
	if len(got) != 1 {
		t.Fatalf("upstream requests = %#v, want only the test call", got)
	}
	assertJSONEqual(t, `{"spans":[{"attributes":{"model":"gpt-4o"}}],"groups":[{"name":"draft","enabled":true,"condition":{"attributes":[{"value":"model","enabled":true}],"resource":[]},"mappers":[]}]}`, got[0].Body)
	if out := successJSON(t, textContent(t, result)); out["groupSource"] != "provided" {
		t.Fatalf("groupSource = %v, want provided", out["groupSource"])
	}
}

func TestTestAIAttributeMapping_UpstreamRejectionKeepsSigNozGuidance(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","error":{"code":"mapping_invalid_input","type":"invalid-input","message":"'spans' must contain at least one span"}}`))
	})
	result, err := h.handleTestAIAttributeMapping(testCtx(), makeToolRequest("signoz_test_ai_attribute_mapping", map[string]any{"spans": []any{}, "groups": []any{}}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if got := resultCode(t, result); got != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
	}
	if structured := resultStructuredMap(t, result); structured["upstreamMessage"] != "'spans' must contain at least one span" {
		t.Fatalf("structured = %#v, want SigNoz's message preserved", structured)
	}
}

func assertJSONEqual(t *testing.T, want, got string) {
	t.Helper()
	var wantValue, gotValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got is not JSON: %v\n%s", err, got)
	}
	wantJSON, _ := json.Marshal(wantValue)
	gotJSON, _ := json.Marshal(gotValue)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("JSON differs\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
}

func TestTestAIAttributeMapping_ResponseWithoutSpansIsAnUpstreamError(t *testing.T) {
	h, _ := newAIToolsHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"collectorLogs":[]}}`))
	})
	result, err := h.handleTestAIAttributeMapping(testCtx(), makeToolRequest("signoz_test_ai_attribute_mapping",
		map[string]any{"spans": []any{map[string]any{"attributes": map[string]any{"model": "gpt-4o"}}}, "groups": []any{}}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if got := resultCode(t, result); got != CodeUpstreamError {
		t.Fatalf("code = %q, want %q: a test without mapped spans must not look like a result", got, CodeUpstreamError)
	}
}
