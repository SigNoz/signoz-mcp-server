package tools

import (
	"testing"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// These tests pin the K5 contract: every CRUD resource id is read from the
// canonical "id" param. Alert and view legacy keys remain aliases; dashboard
// tools make the v0.142.0 hard cut and reject uuid with a correction to id.

// ---------------------------------------------------------------------------
// Neither id nor legacy key supplied → a clean "id is required" validation
// error (NOT a panic, NOT a silent backend call). Pins the handler-side
// presence check that replaces the dropped schema-level `required`.
// ---------------------------------------------------------------------------

// TestUpdateStructs_IDNotSchemaRequired pins the remaining alert alias and the
// dashboard hard cut. Dashboard update advertises only canonical id; both
// handlers validate id presence at runtime.
func TestUpdateStructs_IDNotSchemaRequired(t *testing.T) {
	cases := []struct {
		name        string
		legacyKey   string
		allowLegacy bool
		tool        mcp.Tool
	}{
		{"update_alert", "ruleId", true, mcp.NewTool("signoz_update_alert", mcp.WithInputSchema[types.UpdateAlertInput]())},
		{"update_dashboard", "uuid", false, mcp.NewTool("signoz_update_dashboard", rawInputSchema(updateDashboardSchema))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := inputSchemaProperties(t, tc.tool)
			if _, ok := props["id"]; !ok {
				t.Fatalf("%s: id must remain an advertised property: %#v", tc.name, props)
			}
			_, hasLegacy := props[tc.legacyKey]
			if hasLegacy != tc.allowLegacy {
				t.Fatalf("%s: legacy alias %q advertised=%v, want %v: %#v", tc.name, tc.legacyKey, hasLegacy, tc.allowLegacy, props)
			}
			required := inputSchemaRequiredFields(t, tc.tool)
			if containsString(required, "id") {
				t.Fatalf("%s: id must NOT be in the required list (legacy-only calls must stay schema-valid), got: %#v", tc.name, required)
			}
			if tc.allowLegacy && containsString(required, tc.legacyKey) {
				t.Fatalf("%s: legacy alias %q must NOT be required, got: %#v", tc.name, tc.legacyKey, required)
			}
		})
	}
}
