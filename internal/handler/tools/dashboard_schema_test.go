package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
)

// TestWidgetExamplesValidateAgainstCreateSchema is the cross-contract guard tying
// the widgets-examples resource to the embedded create schema. Every worked panel
// served at signoz://dashboard/widgets-examples must satisfy the schema clients
// are handed. This specifically pins the discriminated-union contract: the Perses
// query/panel/variable unions rely on OAS `discriminator`, which JSON-Schema
// validators ignore, so tools/specgen/specgen.py narrows each branch's discriminator to
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
