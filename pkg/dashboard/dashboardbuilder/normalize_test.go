package dashboardbuilder

import (
	"fmt"
	"reflect"
	"testing"
)

func TestCanonicalDynamicSource(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"", ""},
		{"Traces", "Traces"},
		{"Logs", "Logs"},
		{"Metrics", "Metrics"},
		{"All telemetry", "All telemetry"},
		{"traces", "Traces"},
		{"logs", "Logs"},
		{"metrics", "Metrics"},
		{"METRICS", "Metrics"},
		{"  Logs  ", "Logs"},
		{"all telemetry", "All telemetry"},
		{"ALL TELEMETRY", "All telemetry"},
		{"  All telemetry  ", "All telemetry"},
		{"all sources", "All telemetry"},
		{"All Sources", "All telemetry"},
		{"ALL SOURCES", "All telemetry"},
		{"  all sources  ", "All telemetry"},
		{"foobar", "foobar"},
	}
	for _, c := range cases {
		got := canonicalDynamicSource(c.in)
		if got != c.out {
			t.Errorf("canonicalDynamicSource(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}

func TestCoerceHavingInQueryMaps_EmptyObjectToEmptyArray(t *testing.T) {
	entries := []map[string]any{
		{"queryName": "A", "having": map[string]any{"expression": ""}},
		{"queryName": "B", "having": map[string]any{"expression": "   "}},
	}
	coerceHavingInQueryMaps(entries)
	for i, entry := range entries {
		got, ok := entry["having"].([]any)
		if !ok {
			t.Fatalf("entry %d: expected []any, got %T", i, entry["having"])
		}
		if len(got) != 0 {
			t.Errorf("entry %d: expected empty array, got %v", i, got)
		}
	}
}

func TestCoerceHavingInQueryMaps_AlreadyArrayUntouched(t *testing.T) {
	original := []any{map[string]any{"columnName": "count", "op": ">", "value": 10}}
	entries := []map[string]any{{"queryName": "A", "having": original}}
	coerceHavingInQueryMaps(entries)
	got, ok := entries[0]["having"].([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", entries[0]["having"])
	}
	if !reflect.DeepEqual(got, original) {
		t.Errorf("expected %v, got %v", original, got)
	}
}

func TestCoerceHavingInQueryMaps_NonEmptyExpressionParsedToClauses(t *testing.T) {
	entries := []map[string]any{
		{"queryName": "A", "having": map[string]any{"expression": "count() > 10"}},
		{"queryName": "B", "having": map[string]any{"expression": "sum(system.network.io) > 0"}},
		{"queryName": "C", "having": map[string]any{"expression": "count() >= 5 AND count() < 100"}},
	}
	coerceHavingInQueryMaps(entries)

	got0, ok := entries[0]["having"].([]any)
	if !ok || len(got0) != 1 {
		t.Fatalf("entry 0: expected single-clause []any, got %#v", entries[0]["having"])
	}
	c0 := got0[0].(map[string]any)
	if c0["columnName"] != "count()" || c0["op"] != ">" || c0["value"] != int64(10) {
		t.Errorf("entry 0 clause = %#v", c0)
	}

	got1, ok := entries[1]["having"].([]any)
	if !ok || len(got1) != 1 {
		t.Fatalf("entry 1: expected single-clause []any, got %#v", entries[1]["having"])
	}
	c1 := got1[0].(map[string]any)
	if c1["columnName"] != "sum(system.network.io)" || c1["op"] != ">" || c1["value"] != int64(0) {
		t.Errorf("entry 1 clause = %#v", c1)
	}

	got2, ok := entries[2]["having"].([]any)
	if !ok || len(got2) != 2 {
		t.Fatalf("entry 2: expected two-clause []any, got %#v", entries[2]["having"])
	}
	c2a := got2[0].(map[string]any)
	c2b := got2[1].(map[string]any)
	if c2a["op"] != ">=" || c2a["value"] != int64(5) {
		t.Errorf("entry 2 clause[0] = %#v", c2a)
	}
	if c2b["op"] != "<" || c2b["value"] != int64(100) {
		t.Errorf("entry 2 clause[1] = %#v", c2b)
	}
}

func TestCoerceHavingInQueryMaps_UnparseableExpressionUntouched(t *testing.T) {
	// No recognizable comparison operator at top level — leave the original
	// shape so downstream validation can surface a clear error rather than
	// fabricating a bogus clause.
	obj := map[string]any{"expression": "definitely not a comparison"}
	entries := []map[string]any{{"queryName": "A", "having": obj}}
	coerceHavingInQueryMaps(entries)
	got, ok := entries[0]["having"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any (untouched), got %T", entries[0]["having"])
	}
	if got["expression"] != "definitely not a comparison" {
		t.Errorf("expected expression preserved, got %v", got["expression"])
	}
}

func TestCoerceHavingInQueryMaps_TopLevelOrUntouched(t *testing.T) {
	// HAVING is coerced to an AND-joined clause array; top-level OR (or `||`)
	// cannot be represented and must NOT be partially parsed. Leave the
	// original object so downstream validation surfaces a clear error.
	cases := []string{
		"count() > 5 OR count() < 1",
		"count() > 5 or count() < 1",
		"count() > 5 || count() < 1",
		"count() > 5 AND count() < 100 OR count() = 0",
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			obj := map[string]any{"expression": expr}
			entries := []map[string]any{{"queryName": "A", "having": obj}}
			coerceHavingInQueryMaps(entries)
			got, ok := entries[0]["having"].(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any (untouched), got %T", entries[0]["having"])
			}
			if got["expression"] != expr {
				t.Errorf("expected expression preserved, got %v", got["expression"])
			}
		})
	}
}

func TestCoerceHavingInQueryMaps_TopLevelOrPunctuationBoundaryUntouched(t *testing.T) {
	// OR / AND followed (or preceded) by punctuation rather than whitespace
	// must still be recognized as a logical operator. Prior to this fix the
	// boundary check only accepted whitespace, so `OR(` slipped through and
	// the entire remainder was coerced into a single bogus clause.
	cases := []string{
		"count() > 5 OR(count() < 1)",
		"count() > 5 OR\"x\" = 1",
		"(count() > 5)OR count() < 1",
		"count() > 5 AND(count() < 10) OR count() = 0",
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			obj := map[string]any{"expression": expr}
			entries := []map[string]any{{"queryName": "A", "having": obj}}
			coerceHavingInQueryMaps(entries)
			got, ok := entries[0]["having"].(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any (untouched), got %T", entries[0]["having"])
			}
			if got["expression"] != expr {
				t.Errorf("expected expression preserved, got %v", got["expression"])
			}
		})
	}
}

func TestCoerceHavingInQueryMaps_UnsupportedComparatorUntouched(t *testing.T) {
	// Unsupported two-character comparators (`<>`, `==`, `=<`, `=>`) must NOT
	// be silently coerced by matching the single-character prefix; the
	// original object form should be preserved so downstream validation can
	// surface a clear error.
	cases := []string{
		"count() <> 5",
		"count() == 5",
		"count() =< 5",
		"count() => 5",
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			obj := map[string]any{"expression": expr}
			entries := []map[string]any{{"queryName": "A", "having": obj}}
			coerceHavingInQueryMaps(entries)
			got, ok := entries[0]["having"].(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any (untouched), got %T", entries[0]["having"])
			}
			if got["expression"] != expr {
				t.Errorf("expected expression preserved, got %v", got["expression"])
			}
		})
	}
}

func TestCoerceHavingInQueryMaps_ParenthesizedClausesParsed(t *testing.T) {
	// Clauses wrapped in outer parentheses — either standalone or as
	// AND-joined operands — must be parsed, not left untouched. The strict
	// downstream `[]HavingClause` unmarshal would otherwise fail on
	// otherwise-valid dashboards.
	cases := []struct {
		name       string
		expression string
		want       []map[string]any
	}{
		{
			name:       "single fully wrapped",
			expression: "(count() > 1000)",
			want: []map[string]any{
				{"columnName": "count()", "op": ">", "value": int64(1000)},
			},
		},
		{
			name:       "double wrapped",
			expression: "((count() > 1000))",
			want: []map[string]any{
				{"columnName": "count()", "op": ">", "value": int64(1000)},
			},
		},
		{
			name:       "and-joined with each operand wrapped",
			expression: "(count() > 1000) AND (count() < 5000)",
			want: []map[string]any{
				{"columnName": "count()", "op": ">", "value": int64(1000)},
				{"columnName": "count()", "op": "<", "value": int64(5000)},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := []map[string]any{{"queryName": "A", "having": map[string]any{"expression": tc.expression}}}
			coerceHavingInQueryMaps(entries)
			got, ok := entries[0]["having"].([]any)
			if !ok {
				t.Fatalf("expected []any, got %T: %#v", entries[0]["having"], entries[0]["having"])
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d: %#v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				c, ok := got[i].(map[string]any)
				if !ok {
					t.Fatalf("clause[%d]: expected map, got %T", i, got[i])
				}
				for k, v := range w {
					if c[k] != v {
						t.Errorf("clause[%d][%q] = %#v, want %#v", i, k, c[k], v)
					}
				}
			}
		})
	}
}

func TestCoerceHavingInQueryMaps_GroupedBooleanOperandUntouched(t *testing.T) {
	// Grouped boolean operands joined by top-level AND must not be silently
	// mis-coerced. After splitTopLevelAnd, the first AND operand becomes
	// `(count() > 5 OR count() < 1)`, which after outer-paren stripping
	// would otherwise consume the first `>` and treat the OR'd remainder
	// as the value. Such expressions should be left as the original object
	// form for downstream error handling.
	cases := []string{
		"(count() > 5 OR count() < 1) AND count() != 0",
		"(count() > 5 || count() < 1) AND count() != 0",
		"count() != 0 AND (count() > 5 OR count() < 1)",
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			obj := map[string]any{"expression": expr}
			entries := []map[string]any{{"queryName": "A", "having": obj}}
			coerceHavingInQueryMaps(entries)
			got, ok := entries[0]["having"].(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any (untouched), got %T: %#v", entries[0]["having"], entries[0]["having"])
			}
			if got["expression"] != expr {
				t.Errorf("expected expression preserved, got %v", got["expression"])
			}
		})
	}
}

func TestCoerceHavingInQueryMaps_OrInsideQuotesOrParensParsed(t *testing.T) {
	// `OR` / `||` that is not at the top level (inside quotes or parentheses)
	// must not trip the OR rejection.
	entries := []map[string]any{
		{"queryName": "A", "having": map[string]any{"expression": "count() > 5 AND service.name = 'a OR b'"}},
		{"queryName": "B", "having": map[string]any{"expression": "OrderCount > 0"}},
	}
	coerceHavingInQueryMaps(entries)

	got0, ok := entries[0]["having"].([]any)
	if !ok || len(got0) != 2 {
		t.Fatalf("entry 0: expected two-clause []any, got %#v", entries[0]["having"])
	}
	c := got0[1].(map[string]any)
	if c["columnName"] != "service.name" || c["op"] != "=" || c["value"] != "a OR b" {
		t.Errorf("entry 0 clause[1] = %#v", c)
	}

	got1, ok := entries[1]["having"].([]any)
	if !ok || len(got1) != 1 {
		t.Fatalf("entry 1: expected single-clause []any, got %#v", entries[1]["having"])
	}
	c1 := got1[0].(map[string]any)
	if c1["columnName"] != "OrderCount" {
		t.Errorf("entry 1 clause = %#v (column with leading 'Or' should not be split)", c1)
	}
}

func TestCoerceHavingInQueryMaps_MalformedObjectUntouched(t *testing.T) {
	// Out-of-contract shapes must NOT be silently coerced to []; they should
	// be left as-is so downstream strict unmarshal can surface an error.
	cases := []struct {
		name string
		obj  map[string]any
	}{
		{"no expression key", map[string]any{"foo": "bar"}},
		{"expression is int", map[string]any{"expression": 123}},
		{"expression is nil", map[string]any{"expression": nil}},
		{"expression is array", map[string]any{"expression": []any{"count() > 10"}}},
		{"expression is map", map[string]any{"expression": map[string]any{"k": "v"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entries := []map[string]any{{"queryName": "A", "having": c.obj}}
			coerceHavingInQueryMaps(entries)
			got, ok := entries[0]["having"].(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any (untouched), got %T", entries[0]["having"])
			}
			if !reflect.DeepEqual(got, c.obj) {
				t.Errorf("having should be preserved unchanged, got %#v", got)
			}
		})
	}
}

func TestCoerceHavingInQueryMaps_MissingOrNil(t *testing.T) {
	entries := []map[string]any{
		{"queryName": "A"},                    // no having key
		{"queryName": "B", "having": nil},     // explicit nil
		nil,                                   // nil entry
	}
	coerceHavingInQueryMaps(entries)
	if _, exists := entries[0]["having"]; exists {
		t.Errorf("entry 0: should not add having key when missing")
	}
	if entries[1]["having"] != nil {
		t.Errorf("entry 1: nil having should remain nil, got %v", entries[1]["having"])
	}
}

func TestUppercaseFilterOpsInQueryMaps(t *testing.T) {
	entries := []map[string]any{
		{
			"queryName": "A",
			"filters": map[string]any{
				"op": "AND",
				"items": []any{
					map[string]any{"op": "in", "value": "x"},
					map[string]any{"op": "Not_In", "value": "y"},
					map[string]any{"op": "  like  ", "value": "%z%"},
					map[string]any{"op": "=", "value": 1},
					map[string]any{"op": "!=", "value": 2},
					map[string]any{"op": "EXISTS"},
				},
			},
		},
	}
	uppercaseFilterOpsInQueryMaps(entries)
	items := entries[0]["filters"].(map[string]any)["items"].([]any)
	want := []string{"IN", "NOT_IN", "LIKE", "=", "!=", "EXISTS"}
	for i, w := range want {
		got := items[i].(map[string]any)["op"]
		if got != w {
			t.Errorf("items[%d].op = %q, want %q", i, got, w)
		}
	}
}

func TestNormalizeFilterItems_HealsMalformedKey(t *testing.T) {
	// Mirrors the real CPU Used dashboard bug: key.dataType missing,
	// key.id collapsed to just the key name, value wrapped in a
	// single-element `$var` array.
	entries := []map[string]any{
		{
			"queryName": "A",
			"filters": map[string]any{
				"op": "AND",
				"items": []any{
					map[string]any{
						"id":  "f52f479c-f6ad-41b4-951f-9a03d982d30c",
						"key": map[string]any{
							"id":   "k8s.node.name",
							"key":  "k8s.node.name",
							"type": "",
						},
						"op":    "IN",
						"value": []any{"$k8s.node.name"},
					},
				},
			},
		},
	}
	normalizeFilterItemsInQueryMaps(entries)

	item := entries[0]["filters"].(map[string]any)["items"].([]any)[0].(map[string]any)
	key := item["key"].(map[string]any)
	if key["dataType"] != "string" {
		t.Errorf("dataType: want %q, got %v", "string", key["dataType"])
	}
	if key["id"] != "k8s.node.name--string--" {
		t.Errorf("id: want %q, got %v", "k8s.node.name--string--", key["id"])
	}
	if item["value"] != "$k8s.node.name" {
		t.Errorf("value: want unwrapped string, got %v (%T)", item["value"], item["value"])
	}
}

func TestNormalizeFilterItems_PreservesCanonical(t *testing.T) {
	// 3-part canonical id, dataType present, scalar string value — untouched.
	entries := []map[string]any{
		{
			"filters": map[string]any{
				"op": "AND",
				"items": []any{
					map[string]any{
						"key": map[string]any{
							"id":       "k8s.cluster.name--string--",
							"key":      "k8s.cluster.name",
							"type":     "",
							"dataType": "string",
						},
						"op":    "IN",
						"value": "$k8s.cluster.name",
					},
				},
			},
		},
	}
	before := fmt.Sprintf("%#v", entries[0]["filters"])
	normalizeFilterItemsInQueryMaps(entries)
	after := fmt.Sprintf("%#v", entries[0]["filters"])
	if before != after {
		t.Errorf("canonical filter item mutated:\n before: %s\n after:  %s", before, after)
	}
}

func TestNormalizeFilterItems_PreservesFourPartID(t *testing.T) {
	// 4-part id (e.g. with trailing isColumn segment) has `--` — should pass through.
	entries := []map[string]any{
		{
			"filters": map[string]any{
				"op": "AND",
				"items": []any{
					map[string]any{
						"key": map[string]any{
							"id":       "serviceName--string--tag--true",
							"key":      "serviceName",
							"type":     "tag",
							"dataType": "string",
						},
						"op":    "=",
						"value": "frontend",
					},
				},
			},
		},
	}
	normalizeFilterItemsInQueryMaps(entries)
	key := entries[0]["filters"].(map[string]any)["items"].([]any)[0].(map[string]any)["key"].(map[string]any)
	if key["id"] != "serviceName--string--tag--true" {
		t.Errorf("4-part id should be preserved, got %v", key["id"])
	}
}

func TestNormalizeFilterItems_InfersDataTypeFromID(t *testing.T) {
	// dataType missing but id is well-formed → infer dataType from id.
	entries := []map[string]any{
		{
			"filters": map[string]any{
				"op": "AND",
				"items": []any{
					map[string]any{
						"key": map[string]any{
							"id":   "http.status_code--int64--tag",
							"key":  "http.status_code",
							"type": "tag",
						},
						"op":    "=",
						"value": 500,
					},
				},
			},
		},
	}
	normalizeFilterItemsInQueryMaps(entries)
	key := entries[0]["filters"].(map[string]any)["items"].([]any)[0].(map[string]any)["key"].(map[string]any)
	if key["dataType"] != "int64" {
		t.Errorf("dataType inferred from id: want %q, got %v", "int64", key["dataType"])
	}
	if key["id"] != "http.status_code--int64--tag" {
		t.Errorf("id should be preserved, got %v", key["id"])
	}
}

func TestNormalizeFilterItems_ValueUnwrapOnlyForVariables(t *testing.T) {
	// Single-element arrays of non-variable values must NOT be unwrapped.
	entries := []map[string]any{
		{
			"filters": map[string]any{
				"op": "AND",
				"items": []any{
					map[string]any{
						"key": map[string]any{
							"id":       "env--string--tag",
							"key":      "env",
							"type":     "tag",
							"dataType": "string",
						},
						"op":    "IN",
						"value": []any{"production"}, // single real value — keep as array
					},
					map[string]any{
						"key": map[string]any{
							"id":       "env--string--tag",
							"key":      "env",
							"type":     "tag",
							"dataType": "string",
						},
						"op":    "IN",
						"value": []any{"production", "staging"}, // multi — keep
					},
				},
			},
		},
	}
	normalizeFilterItemsInQueryMaps(entries)
	items := entries[0]["filters"].(map[string]any)["items"].([]any)
	if _, isArr := items[0].(map[string]any)["value"].([]any); !isArr {
		t.Errorf("single non-var value should stay as array, got %T", items[0].(map[string]any)["value"])
	}
	if arr, _ := items[1].(map[string]any)["value"].([]any); len(arr) != 2 {
		t.Errorf("multi-element array should be preserved, got %v", items[1].(map[string]any)["value"])
	}
}

func TestNormalizeFilterItems_MissingOrNil(t *testing.T) {
	entries := []map[string]any{
		nil,
		{"queryName": "A"},
		{"queryName": "B", "filters": nil},
		{"queryName": "C", "filters": map[string]any{"items": []any{"not-a-map", map[string]any{}}}},
	}
	// Should not panic.
	normalizeFilterItemsInQueryMaps(entries)
}

func TestFilterItemsSlice(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int // length; nil → 0
		nilExpected bool
	}{
		{"nil input", nil, 0, true},
		{"empty []any", []any{}, 0, false},
		{"populated []any", []any{map[string]any{"op": "IN"}}, 1, false},
		{"[]map[string]any", []map[string]any{{"op": "IN"}, {"op": "=" }}, 2, false},
		{"wrong type string", "oops", 0, true},
		{"wrong type map", map[string]any{"a": 1}, 0, true},
		{"empty []map[string]any", []map[string]any{}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterItemsSlice(c.in)
			if c.nilExpected && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if !c.nilExpected && got == nil {
				t.Errorf("expected non-nil slice")
			}
			if len(got) != c.want {
				t.Errorf("len = %d, want %d", len(got), c.want)
			}
		})
	}
}

func TestUppercaseFilterOpsInQueryMaps_TypedSliceLiteral(t *testing.T) {
	// Go-idiomatic []map[string]any literal (not []any). Must still be
	// processed by the normalizer, same as the JSON-unmarshalled shape.
	entries := []map[string]any{
		{
			"filters": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{"op": "in", "value": "x"},
					{"op": "not_in", "value": "y"},
				},
			},
		},
	}
	uppercaseFilterOpsInQueryMaps(entries)
	items := entries[0]["filters"].(map[string]any)["items"].([]map[string]any)
	if items[0]["op"] != "IN" || items[1]["op"] != "NOT_IN" {
		t.Errorf("expected uppercase, got %v / %v", items[0]["op"], items[1]["op"])
	}
}

func TestNormalizeFilterItemsInQueryMaps_TypedSliceLiteral(t *testing.T) {
	// Same Go-idiomatic shape — must heal the malformed key even though
	// items is []map[string]any rather than []any.
	entries := []map[string]any{
		{
			"filters": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"key":   map[string]any{"id": "k8s.node.name", "key": "k8s.node.name", "type": ""},
						"op":    "IN",
						"value": []any{"$k8s.node.name"},
					},
				},
			},
		},
	}
	normalizeFilterItemsInQueryMaps(entries)
	item := entries[0]["filters"].(map[string]any)["items"].([]map[string]any)[0]
	key := item["key"].(map[string]any)
	if key["dataType"] != "string" {
		t.Errorf("dataType not filled: %v", key["dataType"])
	}
	if key["id"] != "k8s.node.name--string--" {
		t.Errorf("id not rebuilt: %v", key["id"])
	}
	if item["value"] != "$k8s.node.name" {
		t.Errorf("value not unwrapped: %v", item["value"])
	}
}

func TestUppercaseFilterOpsInQueryMaps_MissingOrNil(t *testing.T) {
	entries := []map[string]any{
		nil,                               // nil entry
		{"queryName": "A"},                // no filters key
		{"queryName": "B", "filters": nil}, // explicit nil
		{"queryName": "C", "filters": "oops"}, // wrong type
		{"queryName": "D", "filters": map[string]any{"op": "AND"}}, // no items
		{"queryName": "E", "filters": map[string]any{"items": "oops"}}, // wrong items type
		{"queryName": "F", "filters": map[string]any{"items": []any{
			"not-a-map",                        // non-map item
			map[string]any{},                   // item without op
			map[string]any{"op": 123},          // non-string op
		}}},
	}
	// Should not panic and should leave non-normalizable entries untouched.
	uppercaseFilterOpsInQueryMaps(entries)

	if _, exists := entries[1]["filters"]; exists {
		t.Errorf("entry 1: should not add filters key when missing")
	}
	if entries[2]["filters"] != nil {
		t.Errorf("entry 2: nil filters should remain nil")
	}
	if entries[3]["filters"] != "oops" {
		t.Errorf("entry 3: non-map filters should be untouched")
	}
	if _, exists := entries[4]["filters"].(map[string]any)["items"]; exists {
		t.Errorf("entry 4: should not add items key when missing")
	}
	fItems := entries[6]["filters"].(map[string]any)["items"].([]any)
	if fItems[0] != "not-a-map" {
		t.Errorf("entry 6 items[0]: non-map should be untouched")
	}
	if _, exists := fItems[1].(map[string]any)["op"]; exists {
		t.Errorf("entry 6 items[1]: missing op should not be added")
	}
	if fItems[2].(map[string]any)["op"] != 123 {
		t.Errorf("entry 6 items[2]: non-string op should be untouched")
	}
}
