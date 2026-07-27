package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
)

// TestEmbeddedDashboardSchemasAreValid ensures each embedded dashboard input
// schema (create/update/patch) parses and resolves as a Draft 2020-12 schema.
// These schemas are generated from the upstream OpenAPI spec by extract_schemas.py;
// a bad regen (dangling $ref, malformed union) would otherwise only surface at
// runtime in a schema-aware MCP client.
func TestEmbeddedDashboardSchemasAreValid(t *testing.T) {
	cases := map[string][]byte{
		"create": createDashboardSchema,
		"update": updateDashboardSchema,
		"patch":  patchDashboardSchema,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			var s jsonschema.Schema
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatalf("embedded %s schema does not parse: %v", name, err)
			}
			if _, err := s.Resolve(nil); err != nil {
				t.Fatalf("embedded %s schema does not resolve as draft 2020-12: %v", name, err)
			}
		})
	}
}

// TestWidgetExamplesValidateAgainstCreateSchema is the cross-contract guard tying
// the widgets-examples resource to the embedded create schema. Every worked panel
// served at signoz://dashboard/widgets-examples must satisfy the schema clients
// are handed. This specifically pins the discriminated-union contract: the Perses
// query/panel/variable unions rely on OAS `discriminator`, which JSON-Schema
// validators ignore, so extract_schemas.py narrows each branch's discriminator to
// a `const`. If a future regen drops that, the signoz/CompositeQuery examples
// (multiple builder queries + a formula) stop validating here — failing the test
// instead of silently shipping a schema that rejects the very pattern the docs teach.
func TestWidgetExamplesValidateAgainstCreateSchema(t *testing.T) {
	var full jsonschema.Schema
	if err := json.Unmarshal(createDashboardSchema, &full); err != nil {
		t.Fatalf("create schema does not parse: %v", err)
	}
	// Each example block is a single panel — the object placed in spec.panels — so
	// validate it against the DashboardtypesPanel definition, resolved with the
	// full create-schema $defs so all internal $refs resolve.
	panelSchema := &jsonschema.Schema{
		Ref:  "#/$defs/DashboardtypesPanel",
		Defs: full.Defs,
	}
	resolved, err := panelSchema.Resolve(nil)
	if err != nil {
		t.Fatalf("panel schema does not resolve: %v", err)
	}

	panels := extractJSONObjects(dashboard.WidgetExamples)
	if len(panels) == 0 {
		t.Fatal("no example panels extracted from dashboard.WidgetExamples")
	}
	for i, block := range panels {
		var v any
		if err := json.Unmarshal([]byte(block), &v); err != nil {
			t.Errorf("example %d is not valid JSON: %v", i, err)
			continue
		}
		if err := resolved.Validate(v); err != nil {
			t.Errorf("example %d does not validate against DashboardtypesPanel: %v", i, err)
		}
	}
}

// TestDashboardSchemasEnforceExactlyOnePanelQuery guards the local schema
// correction that mirrors the backend's panel validation. The outer panel
// queries array must contain exactly one query, while that query may itself be
// a CompositeQuery containing multiple builder queries and formulas.
func TestDashboardSchemasEnforceExactlyOnePanelQuery(t *testing.T) {
	panels := extractJSONObjects(dashboard.WidgetExamples)
	if len(panels) == 0 {
		t.Fatal("no example panels extracted from dashboard.WidgetExamples")
	}
	// The first example intentionally uses a CompositeQuery with several nested
	// entries, proving the constraint applies only to the panel's outer array.
	basePanel := panels[0]
	var assertedPanel map[string]any
	if err := json.Unmarshal([]byte(basePanel), &assertedPanel); err != nil {
		t.Fatalf("first example panel is not valid JSON: %v", err)
	}
	panelSpec, ok := assertedPanel["spec"].(map[string]any)
	if !ok {
		t.Fatal("first example panel has no object spec")
	}
	outerQueries, ok := panelSpec["queries"].([]any)
	if !ok || len(outerQueries) != 1 {
		t.Fatalf("first example must have exactly one outer query, got %T with length %d", panelSpec["queries"], len(outerQueries))
	}
	outerQuery, ok := outerQueries[0].(map[string]any)
	if !ok {
		t.Fatal("first example's outer query is not an object")
	}
	querySpec, ok := outerQuery["spec"].(map[string]any)
	if !ok {
		t.Fatal("first example's outer query has no object spec")
	}
	plugin, ok := querySpec["plugin"].(map[string]any)
	if !ok || plugin["kind"] != "signoz/CompositeQuery" {
		t.Fatalf("first example must use signoz/CompositeQuery, got %v", querySpec["plugin"])
	}
	pluginSpec, ok := plugin["spec"].(map[string]any)
	if !ok {
		t.Fatal("first example's CompositeQuery has no object spec")
	}
	nestedQueries, ok := pluginSpec["queries"].([]any)
	if !ok || len(nestedQueries) < 2 {
		t.Fatalf("first example's CompositeQuery must have at least two nested entries, got %T with length %d", pluginSpec["queries"], len(nestedQueries))
	}

	for schemaName, raw := range map[string][]byte{
		"create": createDashboardSchema,
		"update": updateDashboardSchema,
	} {
		t.Run(schemaName, func(t *testing.T) {
			var full jsonschema.Schema
			if err := json.Unmarshal(raw, &full); err != nil {
				t.Fatalf("%s schema does not parse: %v", schemaName, err)
			}
			panelSchema := &jsonschema.Schema{
				Ref:  "#/$defs/DashboardtypesPanel",
				Defs: full.Defs,
			}
			resolved, err := panelSchema.Resolve(nil)
			if err != nil {
				t.Fatalf("%s panel schema does not resolve: %v", schemaName, err)
			}

			for _, queryCount := range []int{0, 1, 2} {
				t.Run(fmt.Sprintf("queries_%d", queryCount), func(t *testing.T) {
					var panel map[string]any
					if err := json.Unmarshal([]byte(basePanel), &panel); err != nil {
						t.Fatalf("example panel is not valid JSON: %v", err)
					}
					spec := panel["spec"].(map[string]any)
					queries := spec["queries"].([]any)
					switch queryCount {
					case 0:
						spec["queries"] = []any{}
					case 1:
						// Keep the valid CompositeQuery example unchanged.
					case 2:
						spec["queries"] = append(queries, queries[0])
					}

					err := resolved.Validate(panel)
					if queryCount == 1 && err != nil {
						t.Fatalf("one outer query should validate: %v", err)
					}
					if queryCount != 1 && err == nil {
						t.Fatalf("%d outer queries should fail validation", queryCount)
					}
				})
			}
		})
	}
}

func TestPatchSchemaCarriesOneQueryRecovery(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(patchDashboardSchema, &schema); err != nil {
		t.Fatalf("patch schema does not parse: %v", err)
	}
	description := schema.Properties["patch"].Description
	for _, required := range []string{
		"replace /spec/panels/<id>/spec/queries/0",
		"never add or append a second outer query",
		"signoz/CompositeQuery",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("patch description must include %q, got: %s", required, description)
		}
	}
}

func TestPatchSchemaRequiresArrayButAllowsEmpty(t *testing.T) {
	var full jsonschema.Schema
	if err := json.Unmarshal(patchDashboardSchema, &full); err != nil {
		t.Fatalf("patch schema does not parse: %v", err)
	}
	resolved, err := full.Resolve(nil)
	if err != nil {
		t.Fatalf("patch schema does not resolve: %v", err)
	}

	tests := []struct {
		name      string
		patch     any
		wantValid bool
	}{
		{name: "empty array remains valid", patch: []any{}, wantValid: true},
		{name: "null is rejected", patch: nil},
		{name: "object is rejected", patch: map[string]any{}},
		{name: "string is rejected", patch: "[]"},
		{name: "number is rejected", patch: float64(1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := resolved.Validate(map[string]any{"patch": tc.patch})
			if tc.wantValid && err != nil {
				t.Fatalf("valid patch payload was rejected: %v", err)
			}
			if !tc.wantValid && err == nil {
				t.Fatalf("invalid patch value %T should be rejected", tc.patch)
			}
		})
	}
}

// TestDashboardExamplesValidateAgainstCreateSchema ties the dashboard-examples
// resource to the embedded create schema: every complete dashboard served at
// signoz://dashboard/examples must validate as a create payload clients are handed.
func TestDashboardExamplesValidateAgainstCreateSchema(t *testing.T) {
	var full jsonschema.Schema
	if err := json.Unmarshal(createDashboardSchema, &full); err != nil {
		t.Fatalf("create schema does not parse: %v", err)
	}
	resolved, err := full.Resolve(nil)
	if err != nil {
		t.Fatalf("create schema does not resolve: %v", err)
	}
	dashboards := extractJSONObjects(dashboard.DashboardExamples)
	if len(dashboards) == 0 {
		t.Fatal("no example dashboards extracted from dashboard.DashboardExamples")
	}
	for i, block := range dashboards {
		var v any
		if err := json.Unmarshal([]byte(block), &v); err != nil {
			t.Errorf("example %d is not valid JSON: %v", i, err)
			continue
		}
		if err := resolved.Validate(v); err != nil {
			t.Errorf("example %d does not validate against the create schema: %v", i, err)
		}
	}
}

// extractJSONObjects pulls every top-level, brace-balanced JSON object (a line
// beginning with '{' through its matching '}') out of a text blob — the panel
// blocks embedded in the widgets-examples resource. It relies on the blocks being
// pretty-printed with the outer braces in column 0 and any in-string braces (e.g.
// legend "{{key}}" placeholders) being locally balanced.
func extractJSONObjects(text string) []string {
	var out []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); {
		if strings.HasPrefix(lines[i], "{") {
			depth := 0
			var buf []string
			for i < len(lines) {
				buf = append(buf, lines[i])
				depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
				i++
				if depth == 0 {
					break
				}
			}
			out = append(out, strings.Join(buf, "\n"))
		} else {
			i++
		}
	}
	return out
}
