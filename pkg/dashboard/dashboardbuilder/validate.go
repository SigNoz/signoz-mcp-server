package dashboardbuilder

import (
	"fmt"
	"strconv"
	"strings"
)

// Validate checks the DashboardData for structural correctness.
// It accumulates all errors rather than failing on the first one.
// Call ApplyDefaults before Validate.
func Validate(d *DashboardData) *ValidationError {
	errs := &ValidationError{}

	validateDashboardLevel(d, errs)
	validateVariables(d.Variables, errs)

	widgetIDs := validateWidgets(d.Widgets, errs)
	validateLayout(d, widgetIDs, errs)
	validatePanelMap(d, widgetIDs, errs)

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// --- Dashboard level ---

func validateDashboardLevel(d *DashboardData, errs *ValidationError) {
	if strings.TrimSpace(d.Title) == "" {
		errs.Add("title", "is required and must not be empty")
	}
	if d.Version != "" && d.Version != DashboardVersion {
		errs.Addf("version", "must be %q, got %q", DashboardVersion, d.Version)
	}
}

// --- Variables ---

func validateVariables(vars map[string]*DashboardVariable, errs *ValidationError) {
	for key, v := range vars {
		prefix := fmt.Sprintf("variables.%s", key)

		if v.ID == "" {
			errs.Add(prefix+".id", "must not be empty")
		}
		if !isOneOf(v.Type, ValidVariableTypes) {
			errs.Addf(prefix+".type", "must be one of %v, got %q", ValidVariableTypes, v.Type)
		}
		if v.Sort != "" && !isOneOf(v.Sort, ValidSortTypes) {
			errs.Addf(prefix+".sort", "must be one of %v, got %q", ValidSortTypes, v.Sort)
		}
		if strings.Contains(key, " ") {
			errs.Addf(prefix, "variable key must not contain spaces")
		}

		switch v.Type {
		case VariableTypeQuery:
			if strings.TrimSpace(v.QueryValue) == "" {
				errs.Add(prefix+".queryValue", "is required for QUERY type variables")
			}
		case VariableTypeCustom:
			if strings.TrimSpace(v.CustomValue) == "" {
				errs.Add(prefix+".customValue", "is required for CUSTOM type variables")
			}
		case VariableTypeDynamic:
			if v.DynamicVariablesSource != "" && !isOneOf(v.DynamicVariablesSource, ValidDynamicVariableSources) {
				errs.Addf(prefix+".dynamicVariablesSource", "must be one of %v, got %q", ValidDynamicVariableSources, v.DynamicVariablesSource)
			}
		}
	}
}

// --- Widgets ---

func validateWidgets(widgets []WidgetOrRow, errs *ValidationError) map[string]bool {
	widgetIDs := make(map[string]bool)

	for i := range widgets {
		w := &widgets[i]
		prefix := fmt.Sprintf("widgets[%d]", i)

		if w.ID == "" {
			errs.Add(prefix+".id", "must not be empty")
		} else if widgetIDs[w.ID] {
			errs.Addf(prefix+".id", "duplicate widget id %q", w.ID)
		} else {
			widgetIDs[w.ID] = true
		}

		if w.PanelTypes == PanelTypeRow {
			// Row — no further validation needed.
			continue
		}

		if !isOneOf(w.PanelTypes, ValidPanelTypes) {
			errs.Addf(prefix+".panelTypes", "must be one of %v, got %q", ValidPanelTypes, w.PanelTypes)
		}

		if w.Query == nil {
			errs.Add(prefix+".query", "is required for non-row widgets")
		} else {
			validateQuery(prefix+".query", w.Query, errs)
		}

		// Validate optional display fields.
		if w.Opacity != "" {
			if _, err := strconv.ParseFloat(w.Opacity, 64); err != nil {
				errs.Addf(prefix+".opacity", "must be a valid number string, got %q", w.Opacity)
			}
		}
		if w.LegendPosition != "" && !isOneOf(w.LegendPosition, ValidLegendPositions) {
			errs.Addf(prefix+".legendPosition", "must be one of %v, got %q", ValidLegendPositions, w.LegendPosition)
		}
		if w.LineInterpolation != "" && !isOneOf(w.LineInterpolation, ValidLineInterpolations) {
			errs.Addf(prefix+".lineInterpolation", "must be one of %v, got %q", ValidLineInterpolations, w.LineInterpolation)
		}
		if w.LineStyle != "" && !isOneOf(w.LineStyle, ValidLineStyles) {
			errs.Addf(prefix+".lineStyle", "must be one of %v, got %q", ValidLineStyles, w.LineStyle)
		}
		if w.FillMode != "" && !isOneOf(w.FillMode, ValidFillModes) {
			errs.Addf(prefix+".fillMode", "must be one of %v, got %q", ValidFillModes, w.FillMode)
		}
	}

	return widgetIDs
}

// --- Query ---

func validateQuery(prefix string, q *Query, errs *ValidationError) {
	if !isOneOf(q.QueryType, ValidQueryTypes) {
		errs.Addf(prefix+".queryType", "must be one of %v, got %q", ValidQueryTypes, q.QueryType)
		return // Can't validate further without knowing the type.
	}

	switch q.QueryType {
	case QueryTypeBuilder:
		validateBuilderQuery(prefix, q, errs)
	case QueryTypeClickHouse:
		validateClickHouseQueries(prefix, q, errs)
	case QueryTypePromQL:
		validatePromQLQueries(prefix, q, errs)
	}
}

func validateBuilderQuery(prefix string, q *Query, errs *ValidationError) {
	if q.Builder == nil {
		errs.Add(prefix+".builder", "is required when queryType is \"builder\"")
		return
	}
	if len(q.Builder.QueryData) == 0 {
		errs.Add(prefix+".builder.queryData", "must have at least one query entry")
		return
	}

	for i, qd := range q.Builder.QueryData {
		qPrefix := fmt.Sprintf("%s.builder.queryData[%d]", prefix, i)

		name, _ := qd["queryName"].(string)
		if name == "" {
			errs.Add(qPrefix+".queryName", "is required and must be a non-empty string")
		}

		ds, _ := qd["dataSource"].(string)
		if ds == "" {
			errs.Add(qPrefix+".dataSource", "is required and must be a non-empty string")
		} else if !isOneOf(ds, ValidDataSources) {
			errs.Addf(qPrefix+".dataSource", "must be one of %v, got %q", ValidDataSources, ds)
		}

		expr, _ := qd["expression"].(string)
		if expr == "" {
			errs.Add(qPrefix+".expression", "is required and must be a non-empty string")
		}

		validateFilterExpressionConsistency(qPrefix, qd, errs)
	}

	for i, qf := range q.Builder.QueryFormulas {
		qPrefix := fmt.Sprintf("%s.builder.queryFormulas[%d]", prefix, i)
		validateFilterExpressionConsistency(qPrefix, qf, errs)
	}
}

// validateFilterExpressionConsistency rejects builder queries whose
// `filter.expression` string omits any key present in `filters.items[]`. Both
// the SigNoz list/graph renderer (which reads `filter.expression` directly)
// and the edit-modal expression-reconstruction path (which walks
// `filters.items[]`) must agree, or the two produce different query payloads:
// the list view silently drops predicates, and the edit modal trips on an
// item it cannot find a home for. The SigNoz backend tolerates this
// inconsistency on write (it does not reject mismatched payloads), so we
// enforce it here — permissive upstream does not mean correct.
//
// Only runs when both sides are non-empty: an empty `filter.expression` lets
// SigNoz build from items, and an empty `filters.items[]` means
// expression-only is intentional.
func validateFilterExpressionConsistency(prefix string, qd map[string]any, errs *ValidationError) {
	filterMap, _ := qd["filter"].(map[string]any)
	expression, _ := filterMap["expression"].(string)
	if strings.TrimSpace(expression) == "" {
		return
	}
	filtersMap, _ := qd["filters"].(map[string]any)
	itemsAny, _ := filtersMap["items"].([]any)
	if len(itemsAny) == 0 {
		return
	}
	var missing []string
	for _, it := range itemsAny {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		keyObj, ok := item["key"].(map[string]any)
		if !ok {
			continue
		}
		keyName, _ := keyObj["key"].(string)
		if keyName == "" {
			continue
		}
		if !strings.Contains(expression, keyName) {
			missing = append(missing, keyName)
		}
	}
	if len(missing) > 0 {
		errs.Addf(prefix+".filter.expression",
			"is inconsistent with filters.items[]: keys %v are present in items but missing from expression %q. Every filter item key must appear in filter.expression so the list view and edit modal render the same predicates.",
			missing, expression)
	}
}

func validateClickHouseQueries(prefix string, q *Query, errs *ValidationError) {
	if len(q.ClickhouseSQL) == 0 {
		errs.Add(prefix+".clickhouse_sql", "must have at least one query when queryType is \"clickhouse_sql\"")
		return
	}
	for i, ch := range q.ClickhouseSQL {
		qPrefix := fmt.Sprintf("%s.clickhouse_sql[%d]", prefix, i)
		if strings.TrimSpace(ch.Name) == "" {
			errs.Add(qPrefix+".name", "is required and must not be empty")
		}
		if strings.TrimSpace(ch.Query) == "" {
			errs.Add(qPrefix+".query", "is required and must not be empty")
		}
	}
}

func validatePromQLQueries(prefix string, q *Query, errs *ValidationError) {
	if len(q.PromQL) == 0 {
		errs.Add(prefix+".promql", "must have at least one query when queryType is \"promql\"")
		return
	}
	for i, pq := range q.PromQL {
		qPrefix := fmt.Sprintf("%s.promql[%d]", prefix, i)
		if strings.TrimSpace(pq.Name) == "" {
			errs.Add(qPrefix+".name", "is required and must not be empty")
		}
		if strings.TrimSpace(pq.Query) == "" {
			errs.Add(qPrefix+".query", "is required and must not be empty")
		}
	}
}

// --- Layout ---

func validateLayout(d *DashboardData, widgetIDs map[string]bool, errs *ValidationError) {
	if len(d.Layout) == 0 {
		return
	}

	layoutIDs := make(map[string]bool)
	for i, item := range d.Layout {
		prefix := fmt.Sprintf("layout[%d]", i)

		if item.I == "" {
			errs.Add(prefix+".i", "must not be empty")
		} else {
			if layoutIDs[item.I] {
				errs.Addf(prefix+".i", "duplicate layout id %q", item.I)
			}
			layoutIDs[item.I] = true

			if !widgetIDs[item.I] {
				errs.Addf(prefix+".i", "references unknown widget id %q", item.I)
			}
		}

		if item.X < 0 {
			errs.Addf(prefix+".x", "must be >= 0, got %d", item.X)
		}
		if item.Y < 0 {
			errs.Addf(prefix+".y", "must be >= 0, got %d", item.Y)
		}
		if item.W <= 0 {
			errs.Addf(prefix+".w", "must be > 0, got %d", item.W)
		}
		if item.H <= 0 {
			errs.Addf(prefix+".h", "must be > 0, got %d", item.H)
		}
		if item.X+item.W > GridColumns {
			errs.Addf(prefix, "x(%d) + w(%d) = %d exceeds grid width %d", item.X, item.W, item.X+item.W, GridColumns)
		}
	}

	// Every non-row widget must have a layout entry.
	for _, w := range d.Widgets {
		if w.PanelTypes == PanelTypeRow {
			continue
		}
		if w.ID != "" && !layoutIDs[w.ID] {
			errs.Addf("layout", "missing layout entry for widget %q", w.ID)
		}
	}
}

// --- PanelMap ---

func validatePanelMap(d *DashboardData, widgetIDs map[string]bool, errs *ValidationError) {
	for key, entry := range d.PanelMap {
		prefix := fmt.Sprintf("panelMap.%s", key)

		// Check that the panelMap key references a row widget.
		found := false
		for _, w := range d.Widgets {
			if w.ID == key && w.PanelTypes == PanelTypeRow {
				found = true
				break
			}
		}
		if !found {
			errs.Addf(prefix, "key must reference a row widget id, %q is not a row", key)
		}

		for i, item := range entry.Widgets {
			iPrefix := fmt.Sprintf("%s.widgets[%d]", prefix, i)
			if item.I != "" && !widgetIDs[item.I] {
				errs.Addf(iPrefix+".i", "references unknown widget id %q", item.I)
			}
		}
	}
}

// --- Helpers ---

func isOneOf(val string, allowed []string) bool {
	for _, a := range allowed {
		if val == a {
			return true
		}
	}
	return false
}
