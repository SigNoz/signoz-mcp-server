package types

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestSchemaExport_NoV4Fields locks in that the JSON Schema generated for
// the MCP signoz_create_dashboard / signoz_update_dashboard tools never
// exposes v4 BuilderQuery fields to the LLM. Regressions here would
// re-introduce the silent-acceptance bug from 2026-04-30.
//
// v4 markers come from signoz/pkg/transition/migrate_common.go lines 283-293.
func TestSchemaExport_NoV4Fields(t *testing.T) {
	schema, err := jsonschema.For[CreateDashboardInput](nil)
	if err != nil {
		t.Fatalf("reflect schema: %v", err)
	}

	// Marshal to JSON so we can navigate by raw key — the typed Schema
	// struct is awkward for $defs traversal across library versions.
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	// google/jsonschema-go v0.4.2 produces flat (inline) schemas with no $defs.
	// Walk the known path to BuilderQuery's item schema:
	//   .properties.widgets.items.properties.query.properties.builder.properties.queryData.items
	bq := findBuilderQuery(doc)
	if bq == nil {
		t.Logf("schema JSON:\n%s", raw)
		t.Fatal("could not locate BuilderQuery item schema in generated schema — structure may have changed")
	}

	props, _ := bq["properties"].(map[string]any)
	if props == nil {
		t.Fatal("BuilderQuery has no properties block")
	}

	forbiddenV4 := []string{
		"aggregateOperator", "aggregateAttribute",
		"filters",
		"temporality", "timeAggregation", "spaceAggregation",
		"reduceTo", "seriesAggregation",
		"ShiftBy", "IsAnomaly", "QueriesUsedInFormula",
	}
	for _, k := range forbiddenV4 {
		if _, present := props[k]; present {
			t.Errorf("BuilderQuery.properties[%q] is present in generated schema (v4 leak)", k)
		}
	}

	requiredV5 := []string{"aggregations", "filter", "having"}
	for _, k := range requiredV5 {
		if _, present := props[k]; !present {
			t.Errorf("BuilderQuery.properties[%q] missing from generated schema", k)
		}
	}

	required, _ := bq["required"].([]any)
	if !containsString(required, "aggregations") {
		t.Errorf("BuilderQuery.required does not contain 'aggregations' — schema must force v5 aggregations[]")
	}

	// having must be object-shaped with an 'expression' property — never an array.
	// google/jsonschema-go inlines *HavingExpression as {"type":["null","object"],...}
	havingSchema, _ := props["having"].(map[string]any)
	if havingSchema == nil {
		t.Fatal("BuilderQuery.properties.having is absent — typed having shape missing")
	}
	if !schemaIsObjectLike(havingSchema) {
		t.Errorf("having schema type = %v, want object or [null,object]", havingSchema["type"])
	}
	if hp, _ := havingSchema["properties"].(map[string]any); hp == nil || hp["expression"] == nil {
		t.Errorf("having schema properties.expression missing")
	}
}

// findBuilderQuery navigates the inlined flat schema to the object that
// represents a single BuilderQuery item. google/jsonschema-go v0.4.2 does
// not produce $defs; all nested types are inlined.
//
// Path walked:
//
//	.properties.widgets.items.properties.query.properties.builder
//	  .properties.queryData.items
func findBuilderQuery(doc map[string]any) map[string]any {
	nav := func(m map[string]any, keys ...string) map[string]any {
		cur := m
		for _, k := range keys {
			next, _ := cur[k].(map[string]any)
			if next == nil {
				return nil
			}
			cur = next
		}
		return cur
	}

	// First try $defs / definitions for future-proofing if the library ever
	// changes to emit referenced schemas.
	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := doc[key].(map[string]any); ok {
			if d, ok := defs["BuilderQuery"].(map[string]any); ok {
				return d
			}
		}
	}

	// Fall back to the known inline path.
	return nav(doc,
		"properties", "widgets",
		"items",
		"properties", "query",
		"properties", "builder",
		"properties", "queryData",
		"items",
	)
}

// schemaIsObjectLike returns true if the schema type is "object" or includes
// "object" in a multi-type array (e.g. ["null","object"] for pointer fields).
func schemaIsObjectLike(schema map[string]any) bool {
	switch v := schema["type"].(type) {
	case string:
		return v == "object"
	case []any:
		for _, item := range v {
			if s, _ := item.(string); s == "object" {
				return true
			}
		}
	}
	return false
}

// findDef looks up a definition by name in either $defs or definitions.
// Returns nil when the library uses a flat/inline schema (no $defs).
func findDef(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := doc[key].(map[string]any); ok {
			if d, ok := defs[name].(map[string]any); ok {
				return d
			}
		}
	}
	return nil
}

func containsString(arr []any, want string) bool {
	for _, v := range arr {
		if s, _ := v.(string); s == want {
			return true
		}
	}
	return false
}
