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
