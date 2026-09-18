package tools

import (
	"encoding/json"
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

func TestDashboardSchemasAdvertiseTextPanelAndCanonicalID(t *testing.T) {
	for name, raw := range map[string][]byte{"create": createDashboardSchema, "update": updateDashboardSchema} {
		t.Run(name, func(t *testing.T) {
			text := string(raw)
			if !strings.Contains(text, `"const": "signoz/TextPanel"`) {
				t.Fatal("schema does not advertise signoz/TextPanel")
			}
			if strings.Contains(text, `"uuid"`) {
				t.Fatal("schema still advertises removed uuid alias")
			}
		})
	}
	if strings.Contains(string(patchDashboardSchema), `"uuid"`) {
		t.Fatal("patch schema still advertises removed uuid alias")
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
		panel := v.(map[string]any)
		spec := panel["spec"].(map[string]any)
		plugin := spec["plugin"].(map[string]any)
		queries := spec["queries"].([]any)
		if plugin["kind"] == textPanelKind {
			if len(queries) != 0 {
				t.Errorf("TextPanel example %d has %d queries, want 0", i, len(queries))
			}
		} else if len(queries) != 1 {
			t.Errorf("query panel example %d has %d queries, want 1", i, len(queries))
		}
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
