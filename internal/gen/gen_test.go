package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerate_MinimalSpec asserts the generator produces valid Go for a tiny
// in-memory OpenAPI doc and doesn't emit handlers for excluded operations.
func TestGenerate_MinimalSpec(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yml")
	if err := os.WriteFile(specPath, []byte(minimalSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	handlersDir := filepath.Join(dir, "handlers")
	rootDir := filepath.Join(dir, "gentools")
	if err := Run(Config{
		SpecPath:           specPath,
		HandlersDir:        handlersDir,
		RootDir:            rootDir,
		GentoolsImportPath: "example.com/gentools",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Exactly one tag ("widgets") should produce a handlers file plus the
	// register file. The deprecated and SAML operations should not appear.
	entries, err := os.ReadDir(handlersDir)
	if err != nil {
		t.Fatalf("read handlers dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	wantFiles := map[string]bool{
		"zz_generated_widgets.go":  true,
		"zz_generated_register.go": true,
	}
	for _, n := range names {
		if !wantFiles[n] {
			t.Errorf("unexpected handler file: %s", n)
		}
		delete(wantFiles, n)
	}
	for n := range wantFiles {
		t.Errorf("missing handler file: %s", n)
	}

	_ = rootDir
	body, err := os.ReadFile(filepath.Join(handlersDir, "zz_generated_widgets.go"))
	if err != nil {
		t.Fatalf("read handlers: %v", err)
	}
	got := string(body)

	mustContain := []string{
		`mcp.NewTool("signoz_list_widgets"`,
		`mcp.NewTool("signoz_get_widget_by_id"`,
		`mcp.WithReadOnlyHintAnnotation(true)`,
		`mcp.WithDestructiveHintAnnotation(true)`,
		`http.MethodDelete`,
		`gentypes.GetWidgetByIDInput`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("handlers file missing %q", want)
		}
	}

	mustNotContain := []string{
		// Deprecated operation is excluded.
		`signoz_list_widgets_deprecated`,
		// SAML callback is excluded.
		`signoz_complete_saml_login`,
	}
	for _, bad := range mustNotContain {
		if strings.Contains(got, bad) {
			t.Errorf("handlers file unexpectedly contains %q", bad)
		}
	}
}

const minimalSpec = `openapi: 3.0.3
info:
  title: Test
  version: "1"
paths:
  /widgets:
    get:
      tags: [widgets]
      operationId: ListWidgets
      summary: List widgets
      responses:
        '200':
          description: ok
    post:
      tags: [widgets]
      operationId: CreateWidget
      summary: Create widget
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
      responses:
        '201':
          description: created
  /widgets/{id}:
    get:
      tags: [widgets]
      operationId: GetWidgetByID
      summary: Get a widget
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: string }
      responses:
        '200':
          description: ok
    delete:
      tags: [widgets]
      operationId: DeleteWidget
      summary: Delete widget
      parameters:
        - in: path
          name: id
          required: true
          schema: { type: string }
      responses:
        '204':
          description: no content
  /legacy:
    get:
      tags: [widgets]
      operationId: ListWidgetsDeprecated
      summary: Old
      responses:
        '200':
          description: ok
  /api/v1/complete/saml:
    post:
      tags: [auth]
      operationId: CompleteSAMLLogin
      summary: SAML callback
      responses:
        '303':
          description: redirect
`
