package alert

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var formulaVariablePattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func TestAlertInstructionsUseNeutralChannelValidationGuidance(t *testing.T) {
	for _, required := range []string{
		"At least one valid channel name is required",
		"validates supplied names",
		"names are missing or invalid",
		"available channel names",
		"Present those names to the user",
		"Never guess",
	} {
		if !strings.Contains(Instructions, required) {
			t.Errorf("alert instructions missing notification-channel guidance %q", required)
		}
	}
	if strings.Contains(Instructions, "If the user explicitly names a channel, use it directly") {
		t.Error("alert instructions must not prescribe direct use of an unvalidated channel name")
	}
}

func TestAlertExamplesBoundAndOrderEveryBuilderQuery(t *testing.T) {
	parts := strings.Split(Examples, "```json")
	if len(parts) < 2 {
		t.Fatal("alert examples contain no JSON code blocks")
	}

	checked := 0
	for blockIndex, part := range parts[1:] {
		jsonText, _, found := strings.Cut(part, "```")
		if !found {
			t.Fatalf("JSON block %d is not closed", blockIndex+1)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(jsonText)), &payload); err != nil {
			t.Fatalf("JSON block %d is not executable: %v", blockIndex+1, err)
		}

		condition, _ := payload["condition"].(map[string]any)
		composite, _ := condition["compositeQuery"].(map[string]any)
		queries, _ := composite["queries"].([]any)
		formulaInputs := map[string]bool{}
		for _, rawQuery := range queries {
			query, _ := rawQuery.(map[string]any)
			if query["type"] != "builder_formula" {
				continue
			}
			spec, _ := query["spec"].(map[string]any)
			expression, _ := spec["expression"].(string)
			for _, variable := range formulaVariablePattern.FindAllString(expression, -1) {
				formulaInputs[variable] = true
			}
		}
		for queryIndex, rawQuery := range queries {
			query, _ := rawQuery.(map[string]any)
			queryType, _ := query["type"].(string)
			if queryType != "builder_query" && queryType != "builder_formula" {
				continue
			}
			spec, _ := query["spec"].(map[string]any)
			limit, _ := spec["limit"].(float64)
			if limit <= 0 {
				t.Errorf("JSON block %d query %d (%s) has no positive limit", blockIndex+1, queryIndex, queryType)
			}
			name, _ := spec["name"].(string)
			if queryType == "builder_query" && formulaInputs[name] && limit != 10000 {
				t.Errorf("JSON block %d formula input %s has limit %.0f, want 10000", blockIndex+1, name, limit)
			}
			if queryType == "builder_formula" && limit != 100 {
				t.Errorf("JSON block %d formula result %s has limit %.0f, want 100", blockIndex+1, name, limit)
			}
			order, _ := spec["order"].([]any)
			if len(order) == 0 {
				t.Errorf("JSON block %d query %d (%s) has no order", blockIndex+1, queryIndex, queryType)
			}
			checked++
		}
	}

	if checked == 0 {
		t.Fatal("alert examples contain no builder queries")
	}
}
