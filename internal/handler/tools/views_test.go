package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleListViews_Traces(t *testing.T) {
	var gotSource, gotName string
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, source, name string) (json.RawMessage, error) {
			gotSource = source
			gotName = name
			return json.RawMessage(`{"status":"success","data":[{"id":"v1","name":"akshay"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{
		"source": "traces",
		"name":   "ak",
	})

	result, err := h.handleListViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if gotSource != "traces" || gotName != "ak" {
		t.Errorf("client called with unexpected args: source=%q name=%q", gotSource, gotName)
	}
}

func TestHandleListViews_MissingSource(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_list_views", map[string]any{})
	result, _ := h.handleListViews(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
}

func TestHandleListViews_InvalidSource(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_list_views", map[string]any{
		"source": "exceptions",
	})
	result, _ := h.handleListViews(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "traces") || !strings.Contains(body, "logs") || !strings.Contains(body, "metrics") {
		t.Errorf("error should list valid source values; got: %s", body)
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
			return json.RawMessage(`{"status":"success","data":{"id":"new-id"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "my view",
		"source": "traces",
		"spec": map[string]any{
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces"},
			}},
		},
	})
	result, err := h.handleCreateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"my view"`) ||
		!strings.Contains(string(gotBody), `"source":"traces"`) ||
		!strings.Contains(string(gotBody), `"spec"`) {
		t.Errorf("body missing required fields: %s", gotBody)
	}
	if strings.Contains(string(gotBody), `"searchContext"`) {
		t.Errorf("searchContext should have been stripped from body: %s", gotBody)
	}
}

func TestHandleCreateView_MissingName(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"source": "traces",
		"spec":   map[string]any{},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error, got success")
	}
}

func TestHandleCreateView_InvalidSource(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "x",
		"source": "bogus",
		"spec":   map[string]any{},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
	}
}

func TestHandleCreateView_MissingSpec(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "x",
		"source": "traces",
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
			"source": "logs",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "logs"},
				}},
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
	if gotID != "v1" {
		t.Errorf("id = %q", gotID)
	}
	if strings.Contains(string(gotBody), `"viewId"`) {
		t.Errorf("viewId should not leak into body: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"source":"logs"`) {
		t.Errorf("body missing view fields: %s", gotBody)
	}
}

func TestHandleUpdateView_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_update_view", map[string]any{
		"view": map[string]any{
			"source": "logs",
			"spec":   map[string]any{},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
	}
}

// Back-compat: callers that send SavedView fields flat at the top level
// (pre-wrapper schema) should still work.
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
		"viewId": "v1",
		"name":   "flat",
		"source": "traces",
		"spec": map[string]any{
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces"},
			}},
		},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"source":"traces"`) {
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
				"id":     "v1",
				"name":   "renamed",
				"source": "traces",
				"spec": map[string]any{
					"queries": []any{map[string]any{
						"type": "builder_query",
						"spec": map[string]any{"name": "A", "signal": "traces"},
					}},
				},
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
	if !strings.Contains(string(gotBody), `"source":"traces"`) {
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
			"name":   "my view",
			"source": "logs",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "logs"},
				}},
			},
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
	// When the "view" object has valid top-level name/source, the envelope
	// unwrap must leave any `data` subfield alone — it might be legitimate
	// SavedView payload content the caller wanted to preserve.
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
			"name":   "direct",
			"source": "metrics",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "metrics"},
				}},
			},
			"data": map[string]any{"unrelated": "stuff"},
		},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"source":"metrics"`) {
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
		ListViewsFn: func(ctx context.Context, source, name string) (json.RawMessage, error) {
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
		"source": "traces",
		"limit":  "2",
		"offset": "2",
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
		"id":     "019dade7-3edc-79f4-b885-f6fad49722f2",
		"name":   "x",
		"source": "traces",
		"spec": map[string]any{
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces"},
			}},
		},
		"createdAt": "2026-04-21T10:00:00Z",
		"createdBy": "user@example.com",
		"updatedAt": "2026-04-21T10:00:00Z",
		"updatedBy": "user@example.com",
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
			"id":     "v1",
			"name":   "renamed",
			"source": "traces",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "traces"},
				}},
			},
			"createdAt": "2026-04-21T10:00:00Z",
			"createdBy": "user@example.com",
			"updatedAt": "2026-04-21T10:00:00Z",
			"updatedBy": "user@example.com",
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
	// source. The handler must treat that as an empty list, not an
	// "invalid response format" error.
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, source, name string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":null}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{"source": "metrics"})
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
		ListViewsFn: func(ctx context.Context, source, name string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{"source": "traces"})
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
				ListViewsFn: func(ctx context.Context, source, n string) (json.RawMessage, error) {
					return json.RawMessage(raw), nil
				},
			}
			h := newTestHandler(mock)
			req := makeToolRequest("signoz_list_views", map[string]any{"source": "metrics"})
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

func TestHandleCreateView_RejectsSignalSourceMismatch(t *testing.T) {
	// Documented rule: builder_query.spec.signal must equal source.
	// Upstream doesn't enforce this; a mismatch silently saves a broken view.
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "bad",
		"source": "logs",
		"spec": map[string]any{
			"panelType":   "list",
			"requestType": "raw",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error for signal/source mismatch")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "signal") || !strings.Contains(body, "source") {
		t.Errorf("error should mention signal and source; got: %s", body)
	}
}

func TestHandleCreateView_RejectsEmptyBuilderSignal(t *testing.T) {
	// Upstream doesn't enforce signal presence on builder_query; an empty
	// signal silently saves an unusable view. Reject at the MCP boundary.
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "missing-signal",
		"source": "logs",
		"spec": map[string]any{
			"panelType":   "list",
			"requestType": "raw",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error for empty signal")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "signal") {
		t.Errorf("error should mention signal; got: %s", body)
	}
}

func TestHandleCreateView_AllowsMatchingSignal(t *testing.T) {
	called := false
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{"status":"success","data":{"id":"ok"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "ok",
		"source": "traces",
		"spec": map[string]any{
			"panelType":   "list",
			"requestType": "raw",
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
		"name":   "p",
		"source": "metrics",
		"spec": map[string]any{
			"panelType":   "graph",
			"requestType": "time_series",
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

func TestHandleListViews_Meter(t *testing.T) {
	// "meter" (Cost Meter Explorer) is a valid source and must be passed
	// through to the client verbatim, not rejected as invalid.
	var gotSource string
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, source, name string) (json.RawMessage, error) {
			gotSource = source
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_views", map[string]any{"source": "meter"})
	result, err := h.handleListViews(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error for meter source: %v", result.Content)
	}
	if gotSource != "meter" {
		t.Errorf("client called with source=%q, want meter", gotSource)
	}
}

func TestHandleCreateView_AllowsMeterView(t *testing.T) {
	// A Cost Meter view: source "meter", signal "metrics", spec.source "meter".
	var gotBody []byte
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{"status":"success","data":{"id":"m1"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "log-ingestion",
		"source": "meter",
		"spec": map[string]any{
			"panelType":   "graph",
			"requestType": "time_series",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "metrics", "source": "meter"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("expected meter view to be accepted; got: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"source":"meter"`) ||
		!strings.Contains(string(gotBody), `"signal":"metrics"`) {
		t.Errorf("meter view body missing source/signal: %s", gotBody)
	}
}

func TestHandleCreateView_RejectsMeterWithoutSource(t *testing.T) {
	// A "meter" view that omits source="meter" would silently query the
	// default metrics store. Reject it at the MCP boundary.
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "bad-meter",
		"source": "meter",
		"spec": map[string]any{
			"panelType":   "graph",
			"requestType": "time_series",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "metrics"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected rejection for meter view without source=meter")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "source") || !strings.Contains(body, "meter") {
		t.Errorf("error should mention source and meter; got: %s", body)
	}
}

func TestHandleCreateView_RejectsMeterWrongSignal(t *testing.T) {
	// A "meter" view is queried as metrics; any other signal is invalid.
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "bad-meter-signal",
		"source": "meter",
		"spec": map[string]any{
			"panelType":   "graph",
			"requestType": "time_series",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "logs", "source": "meter"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected rejection for meter view with non-metrics signal")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "metrics") {
		t.Errorf("error should require signal metrics; got: %s", body)
	}
}

func TestHandleCreateView_RejectsSourceMeterOnMetricsPage(t *testing.T) {
	// source="meter" belongs on the "meter" page, not mis-filed under
	// "metrics" (this is the exact mis-filing the meter source fixes).
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "misfiled-meter",
		"source": "metrics",
		"spec": map[string]any{
			"panelType":   "graph",
			"requestType": "time_series",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "metrics", "source": "meter"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected rejection for source=meter on metrics page")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "meter") || !strings.Contains(body, "source") {
		t.Errorf("error should point to source \"meter\"; got: %s", body)
	}
}

func TestHandleCreateView_RejectsSourceMeterOnTracesPage(t *testing.T) {
	// The anti-mis-filing guard is not metrics-only: source="meter" is invalid
	// on any non-meter page, including traces.
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_create_view", map[string]any{
		"name":   "misfiled-meter-traces",
		"source": "traces",
		"spec": map[string]any{
			"panelType":   "list",
			"requestType": "raw",
			"queries": []any{map[string]any{
				"type": "builder_query",
				"spec": map[string]any{"name": "A", "signal": "traces", "source": "meter"},
			}},
		},
	})
	result, _ := h.handleCreateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected rejection for source=meter on traces page")
	}
}

func TestHandleUpdateView_AllowsMeterView(t *testing.T) {
	// Updating an existing meter view (source unchanged) must pass the
	// meter validation branch and reach the client.
	updateCalled := false
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"m1","source":"meter"}}`), nil
		},
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			updateCalled = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "m1",
		"view": map[string]any{
			"name":   "renamed-meter",
			"source": "meter",
			"spec": map[string]any{
				"panelType":   "graph",
				"requestType": "time_series",
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "metrics", "source": "meter"},
				}},
			},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("expected meter update to succeed; got: %v", result.Content)
	}
	if !updateCalled {
		t.Fatalf("UpdateView should have been called for a valid meter view")
	}
}

func TestHandleUpdateView_RejectsSignalMismatch(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":   "x",
			"source": "logs",
			"spec": map[string]any{
				"panelType":   "list",
				"requestType": "raw",
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

func TestHandleUpdateView_RejectsSourceChange(t *testing.T) {
	// Saved views are scoped to an Explorer. The handler must GET the
	// existing view and reject a source that differs.
	updateCalled := false
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"v1","name":"old","source":"traces","spec":{"queries":[]}}}`), nil
		},
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			updateCalled = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":   "renamed",
			"source": "logs",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "logs"},
				}},
			},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected source-change to be rejected")
	}
	if updateCalled {
		t.Errorf("UpdateView must not be called when source is changing")
	}
	body := renderContent(result.Content)
	if !strings.Contains(body, "source") || !strings.Contains(body, "traces") || !strings.Contains(body, "logs") {
		t.Errorf("error should name existing and new source; got: %s", body)
	}
}

func TestHandleUpdateView_AllowsSameSource(t *testing.T) {
	updateCalled := false
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"v1","source":"logs"}}`), nil
		},
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			updateCalled = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":   "renamed",
			"source": "logs",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "logs"},
				}},
			},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("expected success; got: %v", result.Content)
	}
	if !updateCalled {
		t.Fatalf("UpdateView should have been called when source matches")
	}
}

func TestHandleUpdateView_ProceedsWhenGetViewFails(t *testing.T) {
	// If the source-lock pre-fetch fails (network blip, 404 after a
	// concurrent delete), prefer to let the PUT attempt proceed rather than
	// block on a diagnostic GET. Upstream will return its own error.
	updateCalled := false
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return nil, fmt.Errorf("boom")
		},
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			updateCalled = true
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_update_view", map[string]any{
		"viewId": "v1",
		"view": map[string]any{
			"name":   "x",
			"source": "traces",
			"spec": map[string]any{
				"queries": []any{map[string]any{
					"type": "builder_query",
					"spec": map[string]any{"name": "A", "signal": "traces"},
				}},
			},
		},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("expected handler to proceed when GetView fails; got: %v", result.Content)
	}
	if !updateCalled {
		t.Fatalf("UpdateView should have been called")
	}
}
