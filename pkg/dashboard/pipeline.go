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
	// Pre-step: hard-reject v4 widget shapes before any normalization.
	// The dashboardbuilder/panelbuilder pipeline is v4-aware (it has its
	// own legacy validators) and would silently accept some v4 inputs.
	// This gate ensures the LLM-facing tools surface a clear v5 error.
	if errs := rejectV4Shapes(jsonBytes); len(errs) > 0 {
		return nil, fmt.Errorf(
			"dashboard rejected: v4 widget shapes are not supported (%d issue(s)):\n  %s\n\nSee signoz://dashboard/widgets-examples for v5 shape.",
			len(errs), strings.Join(errs, "\n  "))
	}

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

// rejectV4Shapes walks raw dashboard JSON and returns one error per v4
// marker found at the queryData/queryFormulas level. The marker set comes
// from signoz/pkg/transition/migrate_common.go (the SigNoz backend's own
// v4→v5 migration), which is the authoritative source.
//
// The walk is intentionally untyped (map[string]any) so v4 fields the
// caller actually sent are visible — unmarshalling into types.BuilderQuery
// (now v5-only) would silently drop them.
//
// Detection is non-recursive at the queryData level: queryData-level
// `temporality`/`timeAggregation`/`spaceAggregation`/`reduceTo` are v4,
// but the same names inside aggregations[] entries are v5-correct and
// must NOT be flagged.
func rejectV4Shapes(jsonBytes []byte) []string {
	var raw map[string]any
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		// Malformed JSON is the existing pipeline's problem to report.
		return nil
	}

	widgets, _ := raw["widgets"].([]any)
	var errs []string
	for i, w := range widgets {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		widgetID, _ := wm["id"].(string)

		query, ok := wm["query"].(map[string]any)
		if !ok {
			continue
		}
		builder, ok := query["builder"].(map[string]any)
		if !ok {
			continue
		}

		for _, listKey := range []string{"queryData", "queryFormulas"} {
			list, _ := builder[listKey].([]any)
			for j, q := range list {
				qm, ok := q.(map[string]any)
				if !ok {
					continue
				}
				pathPrefix := fmt.Sprintf("widgets[%d] (id=%s).query.builder.%s[%d]", i, widgetID, listKey, j)
				errs = append(errs, scanQueryDataV4(qm, pathPrefix)...)
			}
		}
	}
	return errs
}

// scanQueryDataV4 inspects one queryData/queryFormulas entry and returns
// one error per v4 marker present.
func scanQueryDataV4(qd map[string]any, path string) []string {
	var errs []string

	// Non-empty string keys (queryData-level metric-aggregation fields).
	nonEmptyString := []struct {
		key string
		v5  string
	}{
		{"aggregateOperator", "Use 'aggregations[].timeAggregation' / 'aggregations[].spaceAggregation' instead. See signoz://dashboard/widgets-examples."},
		{"temporality", "Move it inside each 'aggregations[].temporality' entry."},
		{"timeAggregation", "Move it inside each 'aggregations[].timeAggregation' entry."},
		{"spaceAggregation", "Move it inside each 'aggregations[].spaceAggregation' entry."},
		{"reduceTo", "Move it inside each 'aggregations[].reduceTo' entry."},
		{"seriesAggregation", "It is a v4 field with no v5 equivalent — remove it."},
	}
	for _, m := range nonEmptyString {
		if s, ok := qd[m.key].(string); ok && s != "" {
			errs = append(errs, fmt.Sprintf("%s: '%s' is a v4 field. %s", path, m.key, m.v5))
		}
	}

	// aggregateAttribute: object with any non-empty key/name/id field.
	if attr, ok := qd["aggregateAttribute"].(map[string]any); ok {
		for _, k := range []string{"key", "name", "id"} {
			if s, _ := attr[k].(string); s != "" {
				errs = append(errs, fmt.Sprintf("%s: 'aggregateAttribute' is a v4 field. Use 'aggregations[].metricName' instead.", path))
				break
			}
		}
	}

	// filters: object with non-empty items array.
	if f, ok := qd["filters"].(map[string]any); ok {
		if items, _ := f["items"].([]any); len(items) > 0 {
			errs = append(errs, fmt.Sprintf("%s: 'filters.items' is a v4 filter shape. Use 'filter.expression' instead. Example: {\"expression\": \"host.name IN $host.name\"}.", path))
		}
	}

	// having: array shape (any length).
	if _, ok := qd["having"].([]any); ok {
		errs = append(errs, fmt.Sprintf("%s: 'having' as an array is a v4 shape. Use 'having.expression' (object with single 'expression' string) instead.", path))
	}

	// Bare key-presence checks.
	for _, key := range []string{"ShiftBy", "IsAnomaly", "QueriesUsedInFormula"} {
		if _, present := qd[key]; present {
			errs = append(errs, fmt.Sprintf("%s: '%s' is a v4 field with no v5 equivalent — remove it.", path, key))
		}
	}

	return errs
}
