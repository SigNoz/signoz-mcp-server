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

	// Step 1: create a dashboard (v6/Perses shape)
	createResult, err := h.handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", map[string]any{
		"schemaVersion": "v6",
		"tags":          []any{},
		"spec": map[string]any{
			"display":   map[string]any{"name": "Temp Dashboard"},
			"variables": []any{},
			"panels":    map[string]any{},
			"layouts":   []any{},
		},
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
		"schemaVersion": "v6",
		"tags":          []any{},
		"spec": map[string]any{
			"display":   map[string]any{"name": "Latency Dashboard"},
			"variables": []any{},
			"panels":    map[string]any{},
			"layouts":   []any{},
		},
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
	// The v6 dashboard fields are forwarded verbatim.
	if parsed["schemaVersion"] != "v6" {
		t.Errorf("schemaVersion = %v, want v6", parsed["schemaVersion"])
	}
	if _, ok := parsed["spec"].(map[string]any); !ok {
		t.Errorf("spec should be forwarded as an object: %s", gotBody)
	}
}

// generateName is set true only when no name is supplied (so v2 derives a
// DNS-1123 name from spec.display.name); a caller-supplied name leaves it unset.
func TestHandleCreateDashboard_GenerateNameDefault(t *testing.T) {
	for _, tc := range []struct {
		desc    string
		name    string // "" = omit the name key
		wantGen bool
	}{
		{"no name sets generateName", "", true},
		{"name supplied leaves it unset", "my-dash", false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var sent map[string]any
			mock := &client.MockClient{CreateDashboardRawFn: func(ctx context.Context, b []byte) (json.RawMessage, error) {
				_ = json.Unmarshal(b, &sent)
				return json.RawMessage(`{"data":{"id":"x"}}`), nil
			}}
			args := map[string]any{"schemaVersion": "v6", "tags": []any{}, "spec": map[string]any{"display": map[string]any{"name": "Hosts"}}}
			if tc.name != "" {
				args["name"] = tc.name
			}
			res, err := newTestHandler(mock).handleCreateDashboard(testCtx(), makeToolRequest("signoz_create_dashboard", args))
			if err != nil || res.IsError {
				t.Fatalf("unexpected failure: err=%v result=%v", err, res.Content)
			}
			if got := sent["generateName"]; (got == true) != tc.wantGen {
				t.Errorf("generateName = %v, want present=%v (body=%v)", got, tc.wantGen, sent)
			}
		})
	}
}

func TestHandleUpdateDashboard_NormalizesWriteBack(t *testing.T) {
	// The handler must resolve the id, drop the {status,data} envelope and all
	// read-only fields, and forward only updatable body fields — whether the
	// caller sends a bare dashboard (id as a param) or the fetched envelope.
	cases := []struct {
		name string
		args map[string]any
	}{
		{
			name: "bare dashboard with read-only fields and top-level id",
			args: map[string]any{
				"id":            "d-1",
				"searchContext": "rename it",
				"schemaVersion": "v6",
				"name":          "d-1",
				"tags":          []any{},
				"spec":          map[string]any{"display": map[string]any{"name": "Renamed"}},
				"createdAt":     "2026-01-01T00:00:00Z",
				"updatedAt":     "2026-01-02T00:00:00Z",
				"createdBy":     "a@b.io",
				"updatedBy":     "a@b.io",
				"orgId":         "org-1",
				"locked":        false,
				"source":        "user",
				"webUrl":        "http://localhost:8080/dashboard/d-1",
			},
		},
		{
			name: "fetched {status,data} envelope with id inside data",
			args: map[string]any{
				"status": "success",
				"data": map[string]any{
					"id":            "d-1",
					"schemaVersion": "v6",
					"name":          "d-1",
					"tags":          []any{},
					"spec":          map[string]any{"display": map[string]any{"name": "Renamed"}},
					"createdAt":     "2026-01-01T00:00:00Z",
					"orgId":         "org-1",
					"webUrl":        "http://localhost:8080/dashboard/d-1",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody []byte
			var gotID string
			mock := &client.MockClient{
				UpdateDashboardRawFn: func(ctx context.Context, id string, dashboardJSON []byte) (json.RawMessage, error) {
					gotID = id
					gotBody = append([]byte(nil), dashboardJSON...)
					return json.RawMessage(`{"status":"success","data":{"id":"d-1"}}`), nil
				},
			}

			h := newTestHandler(mock)
			result, err := h.handleUpdateDashboard(testCtx(), makeToolRequest("signoz_update_dashboard", tc.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("handler returned error result: %v", result.Content)
			}
			if gotID != "d-1" {
				t.Errorf("id = %q, want d-1", gotID)
			}

			var parsed map[string]any
			if err := json.Unmarshal(gotBody, &parsed); err != nil {
				t.Fatalf("PUT body should be JSON: %v\n%s", err, gotBody)
			}
			for _, k := range []string{"status", "data", "id", "uuid", "searchContext", "createdAt", "updatedAt", "createdBy", "updatedBy", "orgId", "locked", "source", "webUrl"} {
				if _, present := parsed[k]; present {
					t.Errorf("envelope/read-only field %q must not reach the PUT body: %s", k, gotBody)
				}
			}
			for _, k := range []string{"schemaVersion", "name", "tags", "spec"} {
				if _, present := parsed[k]; !present {
					t.Errorf("updatable field %q missing from the PUT body: %s", k, gotBody)
				}
			}
		})
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
	template := `{"schemaVersion":"v6","tags":[{"key":"category","value":"hostmetrics"}],"spec":{"display":{"name":"Host Metrics","description":"Host CPU and memory"}}}`

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
	if parsed["schemaVersion"] != "v6" {
		t.Errorf("schemaVersion = %v, want v6", parsed["schemaVersion"])
	}
	if parsed["generateName"] != true {
		t.Errorf("generateName = %v, want true (template has no top-level name)", parsed["generateName"])
	}
	// Import returns the created dashboard via structuredResult (JSON-first +
	// structuredContent), consistent with create.
	if result.StructuredContent == nil {
		t.Error("import result must populate structuredContent")
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

func TestHandleListDashboards_AddsWebURL(t *testing.T) {
	mock := &client.MockClient{
		ListDashboardsFn: func(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
			return json.RawMessage(`{"dashboards":[{"id":"abc-123","name":"Hosts"}],"tags":[],"total":1}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_dashboards", map[string]any{})

	result, err := h.handleListDashboards(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result")
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/dashboard/abc-123"`) {
		t.Fatalf("expected webUrl in output, got: %s", body)
	}
}

func TestHandleListDashboards_ForwardsFilter(t *testing.T) {
	var gotFilter, gotSort, gotOrder string
	mock := &client.MockClient{
		ListDashboardsFn: func(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
			gotFilter, gotSort, gotOrder = filter, sort, order
			return json.RawMessage(`{"dashboards":[],"tags":[],"total":0}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_dashboards", map[string]any{
		"filter": "  name CONTAINS 'overview'  ",
		"sort":   "name",
		"order":  "asc",
	})

	if _, err := h.handleListDashboards(testCtx(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotFilter != "name CONTAINS 'overview'" {
		t.Fatalf("expected trimmed filter forwarded to client, got %q", gotFilter)
	}
	if gotSort != "name" || gotOrder != "asc" {
		t.Fatalf("expected sort/order forwarded, got sort=%q order=%q", gotSort, gotOrder)
	}
}

// 500 is under the old shared cap (1000) but over the v2 cap (200), so it must
// be clamped to 200 before forwarding, with an advisory note.
func TestHandleListDashboards_LimitClamped(t *testing.T) {
	var gotLimit int
	mock := &client.MockClient{
		ListDashboardsFn: func(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
			gotLimit = limit
			return json.RawMessage(`{"dashboards":[],"tags":[],"total":0}`), nil
		},
	}
	res, err := newTestHandler(mock).handleListDashboards(testCtx(),
		makeToolRequest("signoz_list_dashboards", map[string]any{"limit": "500"}))
	if err != nil || res.IsError {
		t.Fatalf("unexpected failure: err=%v result=%v", err, res.Content)
	}
	if gotLimit != dashboardListMaxLimit {
		t.Errorf("forwarded limit = %d, want clamped to %d", gotLimit, dashboardListMaxLimit)
	}
	if !resultNotesContain(res, "exceeded the maximum") {
		t.Errorf("expected clamp advisory note, got: %v", allTextBlocks(res))
	}
}

func TestHandleListDashboards_AddsWebURL_WrappedEnvelope(t *testing.T) {
	// The v2 API wraps the list in a {"data": {...}} envelope; the webUrl
	// injection must reach entries nested under it.
	mock := &client.MockClient{
		ListDashboardsFn: func(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"dashboards":[{"id":"abc-123","name":"Hosts"}],"tags":[],"total":1}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_dashboards", map[string]any{})

	result, err := h.handleListDashboards(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result")
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/dashboard/abc-123"`) {
		t.Fatalf("expected webUrl in wrapped-envelope output, got: %s", body)
	}
}

func TestHandleCreateDashboard_AddsWebURL(t *testing.T) {
	// Create echoes back the server-generated dashboard (with its id); the handler
	// injects a webUrl deep link discovered from that body.
	mock := &client.MockClient{
		CreateDashboardRawFn: func(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","data":{"id":"new-id-1","name":"hosts"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_create_dashboard", map[string]any{
		"schemaVersion": "v6",
		"tags":          []any{},
		"spec":          map[string]any{"display": map[string]any{"name": "Hosts"}},
	})
	result, err := h.handleCreateDashboard(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result: %v", result.Content)
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/dashboard/new-id-1"`) {
		t.Fatalf("expected webUrl on created dashboard, got: %s", body)
	}
}

func TestHandleListDashboards_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		ListDashboardsFn: func(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
			return json.RawMessage(`{"dashboards":[{"id":"abc-123","name":"Hosts"}],"tags":[],"total":1}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_list_dashboards", map[string]any{})

	result, err := h.handleListDashboards(testCtx(), req) // no URL in ctx
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}

func TestHandleGetDashboard_WrappedBodyGetsWebURL(t *testing.T) {
	mock := &client.MockClient{
		GetDashboardFn: func(ctx context.Context, uuid string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"uuid":"x","name":"Hosts"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_dashboard", map[string]any{"uuid": "x"})
	result, err := h.handleGetDashboard(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handler returned error result")
	}
	body := textContent(t, result)
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	inner, ok := obj["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected wrapped data object, got: %s", body)
	}
	if inner["webUrl"] != "https://signoz.example.com/dashboard/x" {
		t.Fatalf("expected webUrl on inner object, got: %s", body)
	}
}

func TestHandleGetDashboard_BareBodyGetsWebURL(t *testing.T) {
	mock := &client.MockClient{
		GetDashboardFn: func(ctx context.Context, uuid string) (json.RawMessage, error) {
			return json.RawMessage(`{"uuid":"x","name":"Hosts"}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_dashboard", map[string]any{"uuid": "x"})
	result, err := h.handleGetDashboard(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if !strings.Contains(body, `"webUrl":"https://signoz.example.com/dashboard/x"`) {
		t.Fatalf("expected top-level webUrl, got: %s", body)
	}
}

func TestHandleGetDashboard_OmitsWebURLWhenNoBaseURL(t *testing.T) {
	mock := &client.MockClient{
		GetDashboardFn: func(ctx context.Context, uuid string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":{"uuid":"x"}}`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_dashboard", map[string]any{"uuid": "x"})
	result, err := h.handleGetDashboard(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if strings.Contains(body, "webUrl") {
		t.Fatalf("expected NO webUrl without base URL, got: %s", body)
	}
}

func TestHandleGetDashboard_MalformedBodyReturnedVerbatim(t *testing.T) {
	mock := &client.MockClient{
		GetDashboardFn: func(ctx context.Context, uuid string) (json.RawMessage, error) {
			return json.RawMessage(`not json`), nil
		},
	}
	h := newTestHandler(mock)
	req := makeToolRequest("signoz_get_dashboard", map[string]any{"uuid": "x"})
	result, err := h.handleGetDashboard(ctxWithURL(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := textContent(t, result)
	if body != "not json" {
		t.Fatalf("expected malformed body returned verbatim, got: %s", body)
	}
}
