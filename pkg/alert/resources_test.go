package alert

import (
	"encoding/json"
	"strings"
	"testing"
)

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
