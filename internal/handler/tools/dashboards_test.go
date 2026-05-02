package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

func TestHandleDeleteDashboard_Success(t *testing.T) {
	// Simulate a create-then-delete flow: the mock "creates" a dashboard and
	// then the delete handler removes it by UUID.
	const createdUUID = "abc-123-def"

	created := false
	deleted := false

	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			created = true
			return json.RawMessage(fmt.Sprintf(`{"status":"success","data":{"uuid":"%s"}}`, createdUUID)), nil
		},
		DeleteDashboardFn: func(ctx context.Context, id string) error {
			if id != createdUUID {
				t.Errorf("expected uuid=%s, got %q", createdUUID, id)
			}
			deleted = true
			return nil
		},
	}

	h := newTestHandler(mock)

	// Step 1: create a dashboard
	createResult, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", map[string]any{
		"title":   "Temp Dashboard",
		"widgets": []any{},
		"layout":  []any{},
	}))
	if err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}
	if createResult.IsError {
		t.Fatalf("create returned error result: %v", createResult.Content)
	}
	if !created {
		t.Fatal("CreateDashboardRawFn was not called")
	}

	// Step 2: delete the dashboard we just created
	deleteResult, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{
		"uuid": createdUUID,
	}))
	if err != nil {
		t.Fatalf("unexpected error on delete: %v", err)
	}
	if deleteResult.IsError {
		t.Fatalf("delete returned error result: %v", deleteResult.Content)
	}
	if !deleted {
		t.Fatal("DeleteDashboardFn was not called")
	}
}

func TestHandleCreateDashboard_StripsSearchContext(t *testing.T) {
	var gotBody []byte
	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			gotBody = append([]byte(nil), dashboardJSON...)
			return json.RawMessage(`{"status":"success","data":{"uuid":"dashboard-123"}}`), nil
		},
	}

	h := newTestHandler(mock)
	result, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", map[string]any{
		"searchContext": "create a dashboard for service latency",
		"title":         "Latency Dashboard",
		"widgets":       []any{},
		"layout":        []any{},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	if len(gotBody) == 0 {
		t.Fatal("CreateDashboardRawFn was not called")
	}

	var parsed map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("dashboard payload should be JSON: %v\n%s", err, gotBody)
	}
	if _, hasSearchContext := parsed["searchContext"]; hasSearchContext {
		t.Errorf("searchContext should be stripped from the API payload: %s", gotBody)
	}
	if parsed["title"] != "Latency Dashboard" {
		t.Errorf("title = %v, want Latency Dashboard", parsed["title"])
	}
}

func TestHandleDeleteDashboard_EmptyUUID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{
		"uuid": "",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for empty uuid")
	}
}

func TestHandleDeleteDashboard_MissingUUID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing uuid")
	}
}

func TestHandleDeleteDashboard_ClientError(t *testing.T) {
	mock := &client.MockClient{
		DeleteDashboardFn: func(ctx context.Context, id string) error {
			return fmt.Errorf("not found")
		},
	}
	h := newTestHandler(mock)
	result, err := h.handleDeleteDashboard(testCtx(), makeToolRequest("signoz_delete_dashboard", map[string]any{
		"uuid": "nonexistent-uuid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result when client returns error")
	}
}

// withTemplateServer swaps the package HTTP client for the test server's
// client and restores it on cleanup.
func withTemplateServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	origClient := templateHTTPClient
	templateHTTPClient = srv.Client()
	t.Cleanup(func() { templateHTTPClient = origClient })
}

func TestHandleImportDashboard_Success(t *testing.T) {
	template := `{"title":"Host Metrics","tags":["hostmetrics"],"layout":[],"widgets":[]}`

	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(template))
	}))
	defer srv.Close()

	// Point the package fetcher at our test server.
	origBase := templateRepoBaseURLVar
	templateRepoBaseURLVar = srv.URL
	t.Cleanup(func() { templateRepoBaseURLVar = origBase })

	withTemplateServer(t, srv)

	var gotBody []byte
	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			gotBody = append([]byte(nil), dashboardJSON...)
			return json.RawMessage(`{"status":"success","data":{"uuid":"created-uuid"}}`), nil
		},
	}

	h := newTestHandler(mock)
	result, err := h.handleImportDashboard(testCtx(), makeToolRequest(
		"signoz_import_dashboard",
		map[string]any{"path": "hostmetrics/hostmetrics.json"},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	if !strings.HasSuffix(receivedPath, "/hostmetrics/hostmetrics.json") {
		t.Errorf("unexpected fetch path: %s", receivedPath)
	}
	if len(gotBody) == 0 {
		t.Fatal("CreateDashboardRawFn was not called")
	}
	var parsed map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("payload should be JSON: %v", err)
	}
	if parsed["title"] != "Host Metrics" {
		t.Errorf("title = %v, want Host Metrics", parsed["title"])
	}
}

func TestHandleImportDashboard_MissingPath(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleImportDashboard(testCtx(), makeToolRequest(
		"signoz_import_dashboard",
		map[string]any{},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result for missing path")
	}
}

func TestHandleImportDashboard_RejectsAbsoluteAndURL(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	for _, bad := range []string{"/etc/passwd", "https://example.com/x.json", "..\\windows", "../escape.json"} {
		result, err := h.handleImportDashboard(testCtx(), makeToolRequest(
			"signoz_import_dashboard",
			map[string]any{"path": bad},
		))
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", bad, err)
		}
		if !result.IsError {
			t.Errorf("expected error result for path %q", bad)
		}
	}
}

func TestHandleListDashboardTemplates_FullCatalog(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleListDashboardTemplates(testCtx(), makeToolRequest(
		"signoz_list_dashboard_templates",
		map[string]any{},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &entries); err != nil {
		t.Fatalf("response should be a JSON array: %v\n%s", err, textContent.Text)
	}
	if len(entries) < 50 {
		t.Errorf("expected the full catalog (>=50 entries), got %d", len(entries))
	}
	// Spot-check that a known template is present and well-formed.
	foundPostgres := false
	for _, e := range entries {
		if e["path"] == "postgresql/postgresql.json" {
			foundPostgres = true
			for _, key := range []string{"id", "title", "path", "category"} {
				if _, ok := e[key]; !ok {
					t.Errorf("postgres entry missing %q field: %#v", key, e)
				}
			}
			break
		}
	}
	if !foundPostgres {
		t.Error("expected postgresql/postgresql.json to be in the bundled catalog")
	}
}

func TestHandleListDashboardTemplates_CategoryFilter(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleListDashboardTemplates(testCtx(), makeToolRequest(
		"signoz_list_dashboard_templates",
		map[string]any{"category": "apm"},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &entries); err != nil {
		t.Fatalf("response should be a JSON array: %v\n%s", err, textContent.Text)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one Apm template")
	}
	for _, e := range entries {
		if !strings.EqualFold(e["category"].(string), "Apm") {
			t.Errorf("category filter leaked entry from %q", e["category"])
		}
	}
}

func TestHandleListDashboardTemplates_UnknownCategory(t *testing.T) {
	h := newTestHandler(&client.MockClient{})
	result, err := h.handleListDashboardTemplates(testCtx(), makeToolRequest(
		"signoz_list_dashboard_templates",
		map[string]any{"category": "no-such-category-zzz"},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error: %v", result.Content)
	}
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &entries); err != nil {
		t.Fatalf("response should be a JSON array: %v\n%s", err, textContent.Text)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty result for unknown category, got %d entries", len(entries))
	}
}

func TestHandleImportDashboard_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	origBase := templateRepoBaseURLVar
	templateRepoBaseURLVar = srv.URL
	t.Cleanup(func() { templateRepoBaseURLVar = origBase })
	withTemplateServer(t, srv)

	h := newTestHandler(&client.MockClient{})
	result, err := h.handleImportDashboard(testCtx(), makeToolRequest(
		"signoz_import_dashboard",
		map[string]any{"path": "no/such/template.json"},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error result on 404")
	}
}
