package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleListViews_Traces(t *testing.T) {
	var gotSourcePage, gotName, gotCategory string
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, sourcePage, name, category string) (json.RawMessage, error) {
			gotSourcePage = sourcePage
			gotName = name
			gotCategory = category
			return json.RawMessage(`{"status":"success","data":[{"id":"v1","name":"akshay"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{
		"sourcePage": "traces",
		"name":       "ak",
	})

	result, err := h.handleListViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if gotSourcePage != "traces" || gotName != "ak" || gotCategory != "" {
		t.Errorf("client called with unexpected args: sp=%q name=%q cat=%q", gotSourcePage, gotName, gotCategory)
	}
}

func TestHandleListViews_MissingSourcePage(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_list_views", map[string]any{})
	result, _ := h.handleListViews(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
}

func TestHandleListViews_InvalidSourcePage(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_list_views", map[string]any{
		"sourcePage": "exceptions",
	})
	result, _ := h.handleListViews(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "traces") || !strings.Contains(body, "logs") || !strings.Contains(body, "metrics") {
		t.Errorf("error should list valid sourcePage values; got: %s", body)
	}
}

func TestHandleGetView_Success(t *testing.T) {
	var gotID string
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			gotID = id
			return json.RawMessage(`{"status":"success","data":{"id":"v1"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_view", map[string]any{"viewId": "v1"})
	result, err := h.handleGetView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if gotID != "v1" {
		t.Errorf("viewId = %q", gotID)
	}
}

func TestHandleGetView_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_get_view", map[string]any{"viewId": ""})
	result, _ := h.handleGetView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
}

// renderContent serializes a tool result's content for substring assertions.
func renderContent(content []mcp.Content) string {
	b, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestHandleCreateView_Success(t *testing.T) {
	var gotBody []byte
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success","data":{"id":"new-id","name":"x"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":           "my view",
		"sourcePage":     "traces",
		"compositeQuery": map[string]any{"queryType": "builder"},
	})
	result, err := h.handleCreateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"my view"`) ||
		!strings.Contains(string(gotBody), `"sourcePage":"traces"`) ||
		!strings.Contains(string(gotBody), `"compositeQuery"`) {
		t.Errorf("body missing required fields: %s", gotBody)
	}
	if strings.Contains(string(gotBody), `"searchContext"`) {
		t.Errorf("searchContext should have been stripped from body: %s", gotBody)
	}
}

func TestHandleCreateView_MissingName(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"sourcePage":     "traces",
		"compositeQuery": map[string]any{},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
}

func TestHandleCreateView_InvalidSourcePage(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":           "x",
		"sourcePage":     "bogus",
		"compositeQuery": map[string]any{},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
	}
}

func TestHandleCreateView_MissingCompositeQuery(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":       "x",
		"sourcePage": "traces",
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
	}
}

func TestHandleUpdateView_Success(t *testing.T) {
	var gotID string
	var gotBody []byte
	mock := &client.MockClient{
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			gotID = id
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":           "renamed",
			"sourcePage":     "logs",
			"compositeQuery": map[string]any{"queryType": "builder"},
		},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if gotID != "v1" {
		t.Errorf("id = %q", gotID)
	}
	if strings.Contains(string(gotBody), `"viewId"`) {
		t.Errorf("viewId should not leak into body: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"name":"renamed"`) {
		t.Errorf("body missing view fields: %s", gotBody)
	}
}

func TestHandleUpdateView_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_update_view", map[string]any{
		"view": map[string]any{
			"name":           "x",
			"sourcePage":     "logs",
			"compositeQuery": map[string]any{},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
	}
}

// Back-compat: callers that send SavedView fields flat at the top level
// (pre-UpdateViewInput schema) should still work.
func TestHandleUpdateView_FlatFieldsBackCompat(t *testing.T) {
	var gotBody []byte
	mock := &client.MockClient{
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId":         "v1",
		"name":           "flat",
		"sourcePage":     "traces",
		"compositeQuery": map[string]any{"queryType": "builder"},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"flat"`) {
		t.Errorf("flat body not accepted: %s", gotBody)
	}
}

func TestHandleDeleteView_Success(t *testing.T) {
	var gotID string
	mock := &client.MockClient{
		DeleteViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			gotID = id
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_delete_view", map[string]any{"viewId": "v1"})
	result, err := h.handleDeleteView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if gotID != "v1" {
		t.Errorf("id = %q", gotID)
	}
}

func TestHandleDeleteView_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_delete_view", map[string]any{})
	result, _ := h.handleDeleteView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
	}
}

func TestHandleUpdateView_UnwrapsGetViewEnvelope(t *testing.T) {
	// Caller pastes the entire signoz_get_view response under "view"
	// ({status,data:{...}}). Handler must unwrap `data` before validating.
	var gotBody []byte
	mock := &client.MockClient{
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"status": "success",
			"data": map[string]any{
				"id":             "v1",
				"name":           "renamed",
				"sourcePage":     "traces",
				"compositeQuery": map[string]any{"queryType": "builder"},
			},
		},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"renamed"`) {
		t.Errorf("body missing unwrapped fields: %s", gotBody)
	}
	if strings.Contains(string(gotBody), `"status":"success"`) {
		t.Errorf("envelope 'status' leaked into body: %s", gotBody)
	}
	if strings.Contains(string(gotBody), `"data":`) {
		t.Errorf("envelope 'data' leaked into body: %s", gotBody)
	}
}

func TestHandleCreateView_UnwrapsEnvelope(t *testing.T) {
	var gotBody []byte
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success","data":{"id":"new"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"status": "success",
		"data": map[string]any{
			"name":           "my view",
			"sourcePage":     "logs",
			"compositeQuery": map[string]any{"queryType": "builder"},
		},
	})
	result, err := h.handleCreateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"my view"`) {
		t.Errorf("body missing unwrapped fields: %s", gotBody)
	}
}

func TestHandleUpdateView_NoUnwrapWhenViewIsValid(t *testing.T) {
	// When the "view" object has valid top-level name/sourcePage, the
	// envelope unwrap must leave any `data` subfield alone — it might be
	// legitimate SavedView payload content the caller wanted to preserve.
	var gotBody []byte
	mock := &client.MockClient{
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":           "direct",
			"sourcePage":     "metrics",
			"compositeQuery": map[string]any{"queryType": "builder"},
			"data":           map[string]any{"unrelated": "stuff"},
		},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"direct"`) {
		t.Errorf("view body got clobbered: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"unrelated"`) {
		t.Errorf("`data` subfield should be preserved when view is valid: %s", gotBody)
	}
}

func TestHandleListViews_Pagination(t *testing.T) {
	// Upstream returns 5 views; request page size 2, offset 2 → expect items
	// [2, 3] and pagination metadata with total=5, hasMore=true, nextOffset=4.
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, sourcePage, name, category string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":[` +
				`{"id":"v0","name":"a"},` +
				`{"id":"v1","name":"b"},` +
				`{"id":"v2","name":"c"},` +
				`{"id":"v3","name":"d"},` +
				`{"id":"v4","name":"e"}` +
				`]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{
		"sourcePage": "traces",
		"limit":      "2",
		"offset":     "2",
	})
	result, err := h.handleListViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	body := renderContent(result.Content)
	for _, want := range []string{`\"id\":\"v2\"`, `\"id\":\"v3\"`, `\"total\":5`, `\"hasMore\":true`, `\"nextOffset\":4`} {
		if !strings.Contains(body, want) {
			t.Errorf("pagination response missing %q; got: %s", want, body)
		}
	}
	for _, unwanted := range []string{`\"id\":\"v0\"`, `\"id\":\"v1\"`, `\"id\":\"v4\"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("pagination response includes out-of-page item %q; got: %s", unwanted, body)
		}
	}
}

func TestHandleCreateView_StripsServerPopulatedFields(t *testing.T) {
	// If an LLM copies a signoz_get_view response wholesale (including
	// server-populated id, createdAt/By, updatedAt/By), the create body
	// sent upstream must omit them.
	var gotBody []byte
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"id":             "019dade7-3edc-79f4-b885-f6fad49722f2",
		"name":           "x",
		"sourcePage":     "traces",
		"compositeQuery": map[string]any{"queryType": "builder"},
		"createdAt":      "2026-04-21T10:00:00Z",
		"createdBy":      "user@example.com",
		"updatedAt":      "2026-04-21T10:00:00Z",
		"updatedBy":      "user@example.com",
	})
	result, err := h.handleCreateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	for _, forbidden := range []string{`"id":`, `"createdAt":`, `"createdBy":`, `"updatedAt":`, `"updatedBy":`} {
		if strings.Contains(string(gotBody), forbidden) {
			t.Errorf("server-populated field %q leaked into body: %s", forbidden, gotBody)
		}
	}
	if !strings.Contains(string(gotBody), `"name":"x"`) {
		t.Errorf("body missing view fields: %s", gotBody)
	}
}

func TestHandleUpdateView_StripsServerPopulatedFields(t *testing.T) {
	var gotBody []byte
	mock := &client.MockClient{
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"id":             "v1",
			"name":           "renamed",
			"sourcePage":     "traces",
			"compositeQuery": map[string]any{"queryType": "builder"},
			"createdAt":      "2026-04-21T10:00:00Z",
			"createdBy":      "user@example.com",
			"updatedAt":      "2026-04-21T10:00:00Z",
			"updatedBy":      "user@example.com",
		},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	for _, forbidden := range []string{`"id":`, `"createdAt":`, `"createdBy":`, `"updatedAt":`, `"updatedBy":`} {
		if strings.Contains(string(gotBody), forbidden) {
			t.Errorf("server-populated field %q leaked into body: %s", forbidden, gotBody)
		}
	}
}

func TestHandleListViews_EmptyResult(t *testing.T) {
	// Upstream returns `data: null` when there are zero views for a
	// sourcePage. The handler must treat that as an empty list, not an
	// "invalid response format" error.
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, sourcePage, name, category string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":null}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{"sourcePage": "metrics"})
	result, err := h.handleListViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success on empty data; got error: %v", result.Content)
	}
	body := renderContent(result.Content)
	for _, want := range []string{`\"data\":[]`, `\"total\":0`, `\"hasMore\":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("empty-list response missing %q; got: %s", want, body)
		}
	}
}

func TestHandleListViews_MissingDataField(t *testing.T) {
	// Same fallback when upstream omits `data` entirely.
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, sourcePage, name, category string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{"sourcePage": "traces"})
	result, err := h.handleListViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success on missing data field; got: %v", result.Content)
	}
}

func TestHandleListViews_NonArrayDataIsEmpty(t *testing.T) {
	// Some SigNoz deployments return `data: {}` (or a scalar) when the
	// filter matches zero rows. The handler must treat any non-array shape
	// as an empty list rather than surfacing "invalid response format".
	cases := map[string]string{
		"empty object": `{"status":"success","data":{}}`,
		"string":       `{"status":"success","data":"nope"}`,
		"number":       `{"status":"success","data":0}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			mock := &client.MockClient{
				ListViewsFn: func(ctx context.Context, sourcePage, n, category string) (json.RawMessage, error) {
					return json.RawMessage(raw), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_list_views", map[string]any{"sourcePage": "metrics"})
			result, err := h.handleListViews(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("expected success on non-array data %q; got error: %v", raw, result.Content)
			}
			body := renderContent(result.Content)
			if !strings.Contains(body, `\"data\":[]`) || !strings.Contains(body, `\"total\":0`) {
				t.Errorf("expected empty data+total=0; got: %s", body)
			}
		})
	}
}

func TestHandleCreateView_RejectsSignalSourcePageMismatch(t *testing.T) {
	// Documented rule: builder_query.spec.signal must equal sourcePage.
	// Upstream doesn't enforce this; a mismatch silently saves a broken view.
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":       "bad",
		"sourcePage": "logs",
		"compositeQuery": map[string]any{
			"queryType": "builder",
			"panelType": "list",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error for signal/sourcePage mismatch")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "signal") || !strings.Contains(body, "sourcePage") {
		t.Errorf("error should mention signal and sourcePage; got: %s", body)
	}
}

func TestHandleCreateView_AllowsMatchingSignal(t *testing.T) {
	called := false
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success","data":"ok"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":       "ok",
		"sourcePage": "traces",
		"compositeQuery": map[string]any{
			"queryType": "builder",
			"panelType": "list",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("expected success; got: %v", result.Content)
	}
	if !called {
		t.Fatalf("CreateView should have been called")
	}
}

func TestHandleCreateView_IgnoresSignalOnNonBuilderQuery(t *testing.T) {
	// promql/clickhouse queries don't carry a `signal` field; validator
	// should leave them alone.
	called := false
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":       "p",
		"sourcePage": "metrics",
		"compositeQuery": map[string]any{
			"queryType": "promql",
			"panelType": "graph",
			"queries": []any{map[string]any{
				"type": "promql_query",
				"spec": map[string]any{"name": "A", "query": "rate(x[5m])"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("expected success for promql query; got: %v", result.Content)
	}
	if !called {
		t.Fatalf("CreateView should have been called")
	}
}

func TestHandleUpdateView_RejectsSignalMismatch(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":       "x",
			"sourcePage": "logs",
			"compositeQuery": map[string]any{
				"queryType": "builder",
				"panelType": "list",
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "metrics"},
				}},
			},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected signal-mismatch rejection")
	}
}
