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
		"viewId":         "v1",
		"name":           "renamed",
		"sourcePage":     "logs",
		"compositeQuery": map[string]any{"queryType": "builder"},
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
}

func TestHandleUpdateView_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	req := makeToolRequest("signoz_update_view", map[string]any{
		"name":           "x",
		"sourcePage":     "logs",
		"compositeQuery": map[string]any{},
	})
	result, _ := h.handleUpdateView(testCtx(), req)
	if !result.IsError {
		t.Fatalf("expected validation error")
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
	// Simulate a caller who follows the documented flow: call signoz_get_view,
	// then pass the entire response back into signoz_update_view. The response
	// shape is {"status":"success","data":{...view fields...}} — the handler
	// must unwrap `data` before validating.
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
		"status": "success",
		"data": map[string]any{
			"id":             "v1",
			"name":           "renamed",
			"sourcePage":     "traces",
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

func TestHandleUpdateView_NoUnwrapWhenTopLevelValid(t *testing.T) {
	// If the caller sends a top-level SavedView that happens to have a
	// `data` key (e.g. someone stuffing payload metadata under a field they
	// named `data`), we must NOT unwrap — `name` and `sourcePage` at the
	// top level indicate an un-enveloped body.
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
		"name":           "direct",
		"sourcePage":     "metrics",
		"compositeQuery": map[string]any{"queryType": "builder"},
		"data":           map[string]any{"unrelated": "stuff"},
	})
	result, err := h.handleUpdateView(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler error: %v", result.Content)
	}
	if !strings.Contains(string(gotBody), `"name":"direct"`) {
		t.Errorf("top-level body got clobbered: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"unrelated"`) {
		t.Errorf("`data` subfield should be preserved when top-level is valid: %s", gotBody)
	}
}
