package tools

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var schemaMapFields = map[string]struct{}{
	"$defs":             {},
	"definitions":       {},
	"dependentSchemas":  {},
	"patternProperties": {},
	"properties":        {},
}

var schemaValueFields = map[string]struct{}{
	"additionalItems":       {},
	"additionalProperties":  {},
	"contains":              {},
	"else":                  {},
	"if":                    {},
	"items":                 {},
	"not":                   {},
	"propertyNames":         {},
	"then":                  {},
	"unevaluatedItems":      {},
	"unevaluatedProperties": {},
}

var schemaArrayFields = map[string]struct{}{
	"allOf":       {},
	"anyOf":       {},
	"oneOf":       {},
	"prefixItems": {},
}

func addTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	normalizeToolSchemas(&tool)
	s.AddTool(tool, handler)
}

func normalizeToolSchemas(tool *mcp.Tool) {
	if len(tool.RawInputSchema) > 0 {
		tool.RawInputSchema = normalizeRawSchema(tool.RawInputSchema)
	} else {
		normalizeToolArgumentsSchema(&tool.InputSchema)
	}

	if len(tool.RawOutputSchema) > 0 {
		tool.RawOutputSchema = normalizeRawSchema(tool.RawOutputSchema)
	} else if tool.OutputSchema.Type != "" {
		normalizeToolOutputSchema(&tool.OutputSchema)
	}
}

func normalizeRawSchema(raw json.RawMessage) json.RawMessage {
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}
	normalized := normalizeJSONSchema(schema)
	b, err := json.Marshal(normalized)
	if err != nil {
		return raw
	}
	return b
}

func normalizeToolArgumentsSchema(schema *mcp.ToolInputSchema) {
	for name, propertySchema := range schema.Properties {
		schema.Properties[name] = normalizeJSONSchema(propertySchema)
	}
	for name, defSchema := range schema.Defs {
		schema.Defs[name] = normalizeJSONSchema(defSchema)
	}
	if schema.AdditionalProperties != nil {
		schema.AdditionalProperties = normalizeJSONSchema(schema.AdditionalProperties)
	}
}

func normalizeToolOutputSchema(schema *mcp.ToolOutputSchema) {
	for name, propertySchema := range schema.Properties {
		schema.Properties[name] = normalizeJSONSchema(propertySchema)
	}
	for name, defSchema := range schema.Defs {
		schema.Defs[name] = normalizeJSONSchema(defSchema)
	}
	if schema.AdditionalProperties != nil {
		schema.AdditionalProperties = normalizeJSONSchema(schema.AdditionalProperties)
	}
}

func normalizeJSONSchema(schema any) any {
	switch typed := schema.(type) {
	case bool:
		if typed {
			return map[string]any{}
		}
		return typed
	case map[string]any:
		for key, value := range typed {
			switch {
			case isSchemaMapField(key):
				normalizeSchemaMap(value)
			case isSchemaValueField(key):
				typed[key] = normalizeJSONSchema(value)
			case isSchemaArrayField(key):
				normalizeSchemaArray(value)
			}
		}
	}
	return schema
}

func normalizeSchemaMap(value any) {
	schemas, ok := value.(map[string]any)
	if !ok {
		return
	}
	for name, schema := range schemas {
		schemas[name] = normalizeJSONSchema(schema)
	}
}

func normalizeSchemaArray(value any) {
	schemas, ok := value.([]any)
	if !ok {
		return
	}
	for i, schema := range schemas {
		schemas[i] = normalizeJSONSchema(schema)
	}
}

func isSchemaMapField(key string) bool {
	_, ok := schemaMapFields[key]
	return ok
}

func isSchemaValueField(key string) bool {
	_, ok := schemaValueFields[key]
	return ok
}

func isSchemaArrayField(key string) bool {
	_, ok := schemaArrayFields[key]
	return ok
}
