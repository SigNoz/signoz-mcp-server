package tools

import (
	"encoding/json"
	"strings"
	"testing"

	gentools "github.com/SigNoz/signoz-mcp-server/pkg/types/gentools"
)

// TestGeneratedSchema_EnumFromSpec asserts that OpenAPI enums at the
// parameter level reach the LLM-visible JSON schema verbatim. GetFieldsKeys
// has a stable `signal` query parameter with enum ["traces","logs","metrics"]
// in the SigNoz spec; the direct OpenAPI→JSON Schema translator must
// preserve it on every regeneration. Regression here means validator
// propagation has broken.
func TestGeneratedSchema_EnumFromSpec(t *testing.T) {
	raw := gentools.SchemaGetFieldsKeys
	if len(raw) == 0 {
		t.Fatal("SchemaGetFieldsKeys is empty")
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	signal, _ := props["signal"].(map[string]any)
	if signal == nil {
		t.Fatalf("signal property missing: %s", string(raw))
	}
	if _, hasEnum := signal["enum"]; !hasEnum {
		t.Fatalf("signal.enum missing from schema: %s", string(raw))
	}
	for _, want := range []string{"traces", "logs", "metrics"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("schema missing expected enum value %q", want)
		}
	}
}

// TestGeneratedSchema_NestedBodyFields asserts the composer injects $defs
// with the transitive closure of components, so the LLM sees nested body
// field shape directly via $ref resolution. Regression here means the
// ComposeSchema runtime walker has broken.
func TestGeneratedSchema_NestedBodyFields(t *testing.T) {
	raw := gentools.SchemaCreateChannel
	if len(raw) == 0 {
		t.Fatal("SchemaCreateChannel is empty")
	}
	if !strings.Contains(string(raw), `"body"`) {
		t.Error("body property missing from schema")
	}
	// ConfigReceiver references slack_configs, email_configs, etc. After
	// composition, those field names appear inside the $defs block.
	for _, want := range []string{"slack_configs", "email_configs", "discord_configs"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("expected nested body field %q not found in schema", want)
		}
	}
}
