package mcpcontract

import (
	"encoding/json"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// A generated union hides its variants behind an unexported json.RawMessage,
// so the typed schema must list the variants' JSON types instead of the empty
// object reflection would produce, or a schema-checking client rejects a
// variable value such as "checkout".
func TestWithInputSchema_GeneratedUnionListsItsVariantTypes(t *testing.T) {
	type args struct {
		Value    *apitypes.Querybuildertypesv5VariableItem_Value `json:"value"`
		Envelope apitypes.Querybuildertypesv5QueryEnvelope       `json:"envelope"`
	}
	tool := NewTool("x", WithInputSchema[args]())
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Type       json.RawMessage `json:"type"`
			Properties json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if got, want := string(schema.Properties["value"].Type), `["null","array","boolean","number","string"]`; got != want {
		t.Errorf("scalar union type = %s, want %s", got, want)
	}
	if got, want := string(schema.Properties["envelope"].Type), `"object"`; got != want {
		t.Errorf("object union type = %s, want %s", got, want)
	}
	if schema.Properties["envelope"].Properties != nil {
		t.Errorf("object union must stay open, got properties %s", schema.Properties["envelope"].Properties)
	}
}
