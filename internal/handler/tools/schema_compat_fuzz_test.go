package tools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// A malformed or unstable advertised schema can prevent clients discovering tools.
func FuzzSchemaNormalization(f *testing.F) {
	for _, raw := range []string{
		`{"type":"object","properties":{"anything":true,"flag":{"type":"boolean","default":true},"list":{"type":"array","items":true}},"additionalProperties":false}`,
		`{"$defs":{"value":{"anyOf":[true,false,{"type":"string"}]}},"properties":{"x":{"$ref":"#/$defs/value"}}}`,
		`true`, `false`, `null`, `{"properties":`,
	} {
		f.Add([]byte(raw))
	}
	for _, tool := range []mcp.Tool{
		mcp.NewTool("alert", mcp.WithInputSchema[types.CreateAlertInput]()),
		mcp.NewTool("dashboard", rawInputSchema(createDashboardSchema)),
	} {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		property, err := json.Marshal(map[string]any{
			"type": "boolean", "default": true, "enum": []bool{true, false}, "description": string(raw),
		})
		if err != nil {
			t.Fatal(err)
		}
		structured := append([]byte(`{"type":"object","properties":{"anything":true,"flag":`), property...)
		structured = append(structured, []byte(`}}`)...)
		for _, normalize := range []func(json.RawMessage) json.RawMessage{normalizeRawSchema, normalizeRawInputSchema} {
			var gotSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(normalize(structured), &gotSchema); err != nil {
				t.Fatal(err)
			}
			var wantProperty, gotProperty any
			if err := json.Unmarshal(property, &wantProperty); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(gotSchema.Properties["flag"], &gotProperty); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wantProperty, gotProperty) {
				t.Fatal("normalization changed a property's default, enum, or description")
			}
			if string(gotSchema.Properties["anything"]) != "{}" {
				t.Fatal("boolean schema was not made compatible with object-only clients")
			}
			got := normalize(raw)
			if !json.Valid(raw) {
				if !bytes.Equal(got, raw) {
					t.Fatal("malformed schema was changed rather than passed through")
				}
				continue
			}
			if !json.Valid(got) {
				t.Fatalf("normalization produced invalid JSON: %q", got)
			}
			if twice := normalize(got); !bytes.Equal(got, twice) {
				t.Fatalf("schema changes on repeated normalization: %s -> %s", got, twice)
			}
		}
	})
}
