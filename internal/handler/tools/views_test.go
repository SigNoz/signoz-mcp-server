package tools

import (
	"context"
	"encoding/json"
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

func TestHandleListViews_OmittedSourceListsAll(t *testing.T) {
	// SigNoz treats source as an optional filter; the handler forwards the
	// empty value instead of rejecting it.
	var gotSource string
	mock := &client.MockClient{
		ListViewsFn: func(ctx context.Context, source, name string) (json.RawMessage, error) {
			gotSource = source
			return json.RawMessage(`{"status":"success","data":[{"id":"v1","name":"a"}]}`), nil
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleListViews(testCtx(), makeToolRequest("signoz_list_views", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("omitted source must list all views, got error: %v", result.Content)
	}
	if gotSource != "" {
		t.Fatalf("forwarded source = %q, want empty", gotSource)
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

// TestHandleCreateView_UpstreamValidationMapsToValidationFailed pins the
// no-guard contract: SigNoz is the only validator of the view body, and its
// 400 reaches the client as VALIDATION_FAILED with the upstream message.
func TestHandleCreateView_UpstreamValidationMapsToValidationFailed(t *testing.T) {
	mock := &client.MockClient{
		CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
			return nil, &client.HTTPStatusError{
				StatusCode: 400,
				Body:       `{"status":"error","error":{"code":"invalid_input","message":"name: is required"}}`,
			}
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleCreateView(testCtx(), makeToolRequest("signoz_create_view", map[string]any{
		"source": "traces",
		"spec":   map[string]any{},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resultCode(t, result); got != CodeValidationFailed {
		t.Fatalf("code = %q, want %q", got, CodeValidationFailed)
	}
	structured := resultStructuredMap(t, result)
	if structured["upstreamMessage"] != "name: is required" {
		t.Fatalf("upstreamMessage = %v, want the SigNoz validation message", structured["upstreamMessage"])
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

func TestHandleCreateView_IgnoresSignalOnNonBuilderQuery(t *testing.T) {
	// promql/clickhouse_sql queries don't carry a `signal` field; validator
	// should leave them alone.
	for _, tc := range []struct {
		envelopeType string
		query        string
	}{
		{"promql", "rate(x[5m])"},
		{"clickhouse_sql", "SELECT 1"},
	} {
		t.Run(tc.envelopeType, func(t *testing.T) {
			var gotBody []byte
			mock := &client.MockClient{
				CreateViewFn: func(ctx context.Context, body []byte) (json.RawMessage, error) {
					gotBody = body
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
						"type": tc.envelopeType,
						"spec": map[string]any{"name": "A", "query": tc.query},
					}},
				},
			})
			result, _ := h.handleCreateView(testCtx(), req)
			if result.IsError {
				t.Fatalf("expected success for %s query; got: %v", tc.envelopeType, result.Content)
			}
			body := string(gotBody)
			if !strings.Contains(body, `"type":"`+tc.envelopeType+`"`) {
				t.Fatalf("create body lost the envelope type %q: %s", tc.envelopeType, body)
			}
			if !strings.Contains(body, tc.query) {
				t.Fatalf("create body lost the query %q: %s", tc.query, body)
			}
		})
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

func TestHandleUpdateView_AllowsMeterView(t *testing.T) {
	// Updating an existing meter view (source unchanged) must pass the
	// meter validation branch and reach the client.
	var gotID string
	var gotBody []byte
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"m1","source":"meter"}}`), nil
		},
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
			gotID = id
			gotBody = body
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
	if gotID != "m1" {
		t.Fatalf("UpdateView id = %q, want m1", gotID)
	}
	// The v2 update body has no "name" (UpdatableSavedView drops it); the
	// meter source and the query spec must survive.
	if body := string(gotBody); !strings.Contains(body, `"source":"meter"`) || !strings.Contains(body, `"signal":"metrics"`) {
		t.Fatalf("update body lost meter fields: %s", body)
	}
}

func TestHandleUpdateView_SourceChangeForwardedWithoutPrefetch(t *testing.T) {
	// SigNoz owns the update semantics: a changed source moves the view to
	// that Explorer. The handler forwards it and performs no diagnostic GET.
	getCalled := false
	var gotBody []byte
	mock := &client.MockClient{
		GetViewFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			getCalled = true
			return json.RawMessage(`{"status":"success","data":{"id":"v1","source":"traces"}}`), nil
		},
		UpdateViewFn: func(ctx context.Context, id string, body []byte) (json.RawMessage, error) {
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
	result, _ := h.handleUpdateView(testCtx(), req)
	if result.IsError {
		t.Fatalf("source change must be forwarded, got error: %v", result.Content)
	}
	if getCalled {
		t.Fatal("update must not pre-fetch the view")
	}
	if !strings.Contains(string(gotBody), `"source":"logs"`) {
		t.Fatalf("update body lost the new source: %s", gotBody)
	}
}
