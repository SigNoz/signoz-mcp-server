// Package dashboard provides a unified validation and normalization pipeline
// for SigNoz dashboard JSON payloads. It chains two complementary layers:
//
//   - dashboardbuilder: structural validation + defaults (layout, variables, nil-slice init, auto-layout)
//   - panelvalidator:   semantic validation per widget (operators, data sources, panel-type rules)
//
// Call Validate before sending dashboard JSON to the SigNoz API.
package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard/dashboardbuilder"
	panelvalidator "github.com/SigNoz/signoz-mcp-server/pkg/dashboard/panelbuilder"
)

// Validate parses raw dashboard JSON, applies defaults, runs structural and
// semantic validation, and returns clean JSON bytes ready for the SigNoz API.
//
// The pipeline:
//  1. dashboardbuilder.ParseFromJSON — parse, apply defaults (nil-slice init,
//     variable normalization, auto-layout, version), structural validation
//  2. panelvalidator.ValidatePanel — per-widget semantic validation (operators,
//     data-source constraints, panel-type rules, formula references)
//  3. Return normalized JSON bytes
//
// Errors from both layers are collected into a single error message.
func Validate(jsonBytes []byte) ([]byte, error) {
	// Step 1: structural parse + defaults + validation via dashboardbuilder.
	dd, err := dashboardbuilder.ParseFromJSON(jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("dashboard validation: %w", err)
	}

	// Step 2: semantic validation of each non-row widget via panelvalidator.
	var panelErrors []string
	for i, w := range dd.Widgets {
		if w.PanelTypes == dashboardbuilder.PanelTypeRow {
			continue
		}

		panel, err := widgetToPanel(w)
		if err != nil {
			panelErrors = append(panelErrors, fmt.Sprintf("widgets[%d] (%s): conversion error: %s", i, w.ID, err))
			continue
		}

		result := panelvalidator.ValidatePanel(panel)
		if !result.Valid {
			for _, e := range result.Errors {
				panelErrors = append(panelErrors, fmt.Sprintf("widgets[%d] (%s): %s", i, w.ID, e))
			}
		}
	}

	if len(panelErrors) > 0 {
		return nil, fmt.Errorf("panel validation failed with %d error(s):\n  %s",
			len(panelErrors), strings.Join(panelErrors, "\n  "))
	}

	// Step 3: serialize the validated+defaulted dashboard back to JSON.
	out, err := dd.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("dashboard serialization: %w", err)
	}
	return out, nil
}

// ValidateFromMap is a convenience wrapper that accepts the raw map[string]any
// from MCP tool arguments, validates, and returns clean JSON bytes.
func ValidateFromMap(m map[string]any) ([]byte, error) {
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal arguments: %w", err)
	}
	return Validate(jsonBytes)
}

// widgetToPanel converts a dashboardbuilder.WidgetOrRow to a panelvalidator.Panel
// via JSON round-trip. This is intentionally loose — both types serialize to the
// same JSON shape, and any fields the panelvalidator doesn't care about are ignored.
func widgetToPanel(w dashboardbuilder.WidgetOrRow) (panelvalidator.Panel, error) {
	data, err := json.Marshal(w)
	if err != nil {
		return panelvalidator.Panel{}, fmt.Errorf("marshal widget: %w", err)
	}
	var panel panelvalidator.Panel
	if err := json.Unmarshal(data, &panel); err != nil {
		return panelvalidator.Panel{}, fmt.Errorf("unmarshal to panel: %w", err)
	}
	return panel, nil
}
