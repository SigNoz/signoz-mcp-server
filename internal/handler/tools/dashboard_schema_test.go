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

// Validate the served examples because JSON Schema ignores OpenAPI discriminators;
// losing the narrowed union branches would reject valid CompositeQuery panels.
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
		var panel map[string]any
		if err := json.Unmarshal([]byte(block), &panel); err != nil {
			t.Errorf("example %d is not valid JSON: %v", i, err)
			continue
		}
		if err := resolved.Validate(panel); err != nil {
			t.Errorf("example %d does not validate against DashboardtypesPanel: %v", i, err)
		}
	}
}

func panelHasMultiEntryCompositeQuery(panel map[string]any) bool {
	spec, _ := panel["spec"].(map[string]any)
	queries, _ := spec["queries"].([]any)
	for _, rawQuery := range queries {
		query, _ := rawQuery.(map[string]any)
		querySpec, _ := query["spec"].(map[string]any)
		plugin, _ := querySpec["plugin"].(map[string]any)
		if plugin["kind"] != "signoz/CompositeQuery" {
			continue
		}
		pluginSpec, _ := plugin["spec"].(map[string]any)
		nestedQueries, _ := pluginSpec["queries"].([]any)
		if len(nestedQueries) > 1 {
			return true
		}
	}
	return false
}

func TestDashboardSchemasValidateQueryCardinalityByPanelKind(t *testing.T) {
	var compositePanel, textPanel string
	var queries []any
	for _, block := range extractJSONObjects(dashboard.WidgetExamples) {
		var panel map[string]any
		if err := json.Unmarshal([]byte(block), &panel); err != nil {
			t.Fatalf("example panel is not valid JSON: %v", err)
		}
		spec := panel["spec"].(map[string]any)
		plugin := spec["plugin"].(map[string]any)
		if plugin["kind"] == "signoz/TextPanel" {
			textPanel = block
		}
		if compositePanel == "" && panelHasMultiEntryCompositeQuery(panel) {
			compositePanel = block
			queries = spec["queries"].([]any)
		}
	}
	if compositePanel == "" || textPanel == "" {
		t.Fatal("need multi-entry CompositeQuery and TextPanel examples in dashboard.WidgetExamples")
	}

	for schemaName, raw := range map[string][]byte{
		"create": createDashboardSchema,
		"update": updateDashboardSchema,
	} {
		t.Run(schemaName, func(t *testing.T) {
			var full jsonschema.Schema
			if err := json.Unmarshal(raw, &full); err != nil {
				t.Fatalf("schema does not parse: %v", err)
			}
			panelSchema := &jsonschema.Schema{
				Ref:  "#/$defs/DashboardtypesPanel",
				Defs: full.Defs,
			}
			resolved, err := panelSchema.Resolve(nil)
			if err != nil {
				t.Fatalf("panel schema does not resolve: %v", err)
			}

			for panelName, basePanel := range map[string]string{
				"composite": compositePanel,
				"text":      textPanel,
			} {
				t.Run(panelName, func(t *testing.T) {
					for _, tc := range []struct {
						name         string
						queries      any
						omit         bool
						wantValidFor string
					}{
						{name: "zero", queries: []any{}, wantValidFor: "text"},
						{name: "one", queries: []any{queries[0]}, wantValidFor: "composite"},
						{name: "two", queries: []any{queries[0], queries[0]}},
						{name: "null", queries: nil},
						{name: "missing", omit: true},
					} {
						t.Run(tc.name, func(t *testing.T) {
							var panel map[string]any
							if err := json.Unmarshal([]byte(basePanel), &panel); err != nil {
								t.Fatalf("example panel is not valid JSON: %v", err)
							}
							spec := panel["spec"].(map[string]any)
							if tc.omit {
								delete(spec, "queries")
							} else {
								spec["queries"] = tc.queries
							}

							err := resolved.Validate(panel)
							wantValid := tc.wantValidFor == panelName
							if wantValid && err != nil {
								t.Fatalf("valid queries rejected: %v", err)
							}
							if !wantValid && err == nil {
								t.Fatal("invalid queries accepted")
							}
						})
					}
				})
			}
		})
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

// TestWidgetsDryRunGuideCoversEverySchemaQueryPlugin catches a new upstream
// query plugin that the widget guide's dry-run envelope mapping does not name.
func TestWidgetsDryRunGuideCoversEverySchemaQueryPlugin(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			Enum          []string `json:"enum"`
			Discriminator struct {
				Mapping map[string]string `json:"mapping"`
			} `json:"discriminator"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(createDashboardSchema, &schema); err != nil {
		t.Fatalf("create schema does not parse: %v", err)
	}
	start := strings.Index(dashboard.WidgetsInstructions, "Dry-run a panel query")
	if start < 0 {
		t.Fatal("widget instructions have no dry-run section")
	}
	section := dashboard.WidgetsInstructions[start:]
	if end := strings.Index(section, "\n\n"); end > 0 {
		section = section[:end]
	}
	envelopeTypes := map[string]bool{}
	for _, queryType := range schema.Defs["Querybuildertypesv5QueryType"].Enum {
		envelopeTypes[queryType] = true
	}
	for kind := range schema.Defs["DashboardtypesQueryPlugin"].Discriminator.Mapping {
		if kind == "signoz/CompositeQuery" {
			if !strings.Contains(section, kind+":") {
				t.Errorf("dry-run section does not explain %s", kind)
			}
			continue
		}
		idx := strings.Index(section, kind+" -> ")
		if idx < 0 {
			t.Errorf("dry-run section does not map %s to an envelope", kind)
			continue
		}
		envelope := strings.FieldsFunc(section[idx+len(kind+" -> "):], func(r rune) bool { return r == ',' || r == '.' || r == ' ' })[0]
		if !envelopeTypes[envelope] {
			t.Errorf("dry-run section maps %s to %q, which is not a Query Builder v5 query type", kind, envelope)
		}
	}
}
