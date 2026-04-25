package tools

import (
	"encoding/json"
	"testing"
)

func TestNormalizeRawSchemaReplacesSchemaTrueWithEmptyObject(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"anything": true,
			"flag": {
				"type": "boolean",
				"default": true
			},
			"list": {
				"type": "array",
				"items": true
			},
			"object": {
				"type": "object",
				"additionalProperties": true
			}
		}
	}`)

	var got map[string]any
	if err := json.Unmarshal(normalizeRawSchema(raw), &got); err != nil {
		t.Fatalf("normalized schema should remain JSON: %v", err)
	}

	properties := got["properties"].(map[string]any)
	if len(properties["anything"].(map[string]any)) != 0 {
		t.Fatalf("properties.anything = %#v, want empty schema object", properties["anything"])
	}

	flag := properties["flag"].(map[string]any)
	if flag["type"] != "boolean" || flag["default"] != true {
		t.Fatalf("flag schema = %#v, want boolean schema with default true preserved", flag)
	}

	list := properties["list"].(map[string]any)
	if len(list["items"].(map[string]any)) != 0 {
		t.Fatalf("list.items = %#v, want empty schema object", list["items"])
	}

	object := properties["object"].(map[string]any)
	if len(object["additionalProperties"].(map[string]any)) != 0 {
		t.Fatalf("object.additionalProperties = %#v, want empty schema object", object["additionalProperties"])
	}
}
