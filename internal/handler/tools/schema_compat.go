package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	// InputValidationNoticePrefix marks the advisory text block appended to a
	// successful result whose arguments mismatched the advertised schema.
	inputValidationNoticePrefix = "Input validation notice:"
	maxValidationMetadataLength = 96
)

type compiledToolSchema struct {
	validator          *jsonschema.Schema
	topLevelProperties map[string]struct{}
}

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

func (h *Handler) addTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	normalizeToolSchemas(&tool)

	input, inputErr := compileToolSchema(tool.Name, "input", inputSchemaJSON(tool))
	if inputErr != nil {
		h.recordSchemaCompileFailure(context.Background(), tool.Name, "input", inputErr)
	}
	output, outputErr := compileToolSchema(tool.Name, "output", outputSchemaJSON(tool))
	if outputErr != nil {
		h.recordSchemaCompileFailure(context.Background(), tool.Name, "output", outputErr)
	}

	if input != nil || output != nil {
		handler = h.validationDecorator(tool.Name, input, output, handler)
	}
	h.registerTool(s, tool, handler)
}

// AddTool exposes the production registration path to server composition and
// end-to-end tests while keeping all built-in registrations on h.addTool.
func (h *Handler) AddTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	h.addTool(s, tool, handler)
}

func inputSchemaJSON(tool mcp.Tool) json.RawMessage {
	if len(tool.RawInputSchema) > 0 {
		return tool.RawInputSchema
	}
	if tool.InputSchema.Type == "" && len(tool.InputSchema.Properties) == 0 && len(tool.InputSchema.Required) == 0 && tool.InputSchema.AdditionalProperties == nil {
		return nil
	}
	b, _ := json.Marshal(tool.InputSchema)
	return b
}

func outputSchemaJSON(tool mcp.Tool) json.RawMessage {
	if len(tool.RawOutputSchema) > 0 {
		return tool.RawOutputSchema
	}
	if tool.OutputSchema.Type == "" && len(tool.OutputSchema.Properties) == 0 && len(tool.OutputSchema.Required) == 0 {
		return nil
	}
	b, _ := json.Marshal(tool.OutputSchema)
	return b
}

func compileToolSchema(toolName, direction string, raw json.RawMessage) (*compiledToolSchema, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	resourceURL := fmt.Sprintf("mem:///signoz/tools/%s/%s-schema.json", toolName, direction)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resourceURL, doc); err != nil {
		return nil, fmt.Errorf("register schema: %w", err)
	}
	validator, err := compiler.Compile(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return &compiledToolSchema{
		validator:          validator,
		topLevelProperties: schemaTopLevelProperties(raw),
	}, nil
}

func schemaTopLevelProperties(raw json.RawMessage) map[string]struct{} {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	node := resolveLocalSchemaRef(root, root)
	properties, _ := node["properties"].(map[string]any)
	out := make(map[string]struct{}, len(properties))
	for name := range properties {
		out[name] = struct{}{}
	}
	return out
}

func resolveLocalSchemaRef(root, node map[string]any) map[string]any {
	ref, _ := node["$ref"].(string)
	if !strings.HasPrefix(ref, "#/") {
		return node
	}
	var current any = root
	for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := current.(map[string]any)
		if !ok {
			return node
		}
		current, ok = m[strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")]
		if !ok {
			return node
		}
	}
	resolved, ok := current.(map[string]any)
	if !ok {
		return node
	}
	return resolved
}

// validationDecorator owns schema validation and never rejects a call.
// Input mismatches are served best-effort with an in-band notice appended to
// the successful result, so agents that read errors can self-correct while
// agents that don't still get a usable answer. Output mismatches and missing
// structured content are telemetry-only (fail open, never silent) — they are
// our defects, not the caller's.
func (h *Handler) validationDecorator(toolName string, input, output *compiledToolSchema, next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var notice string
		if input != nil {
			if err := validateArguments(input.validator, req); err != nil {
				path, constraint := validationMetadata(err, input.topLevelProperties)
				h.recordValidationMismatch(ctx, toolName, "input", path, constraint)
				notice = inputValidationNotice(err)
			}
		}

		result, err := next(ctx, req)
		if err != nil || result == nil {
			return result, err
		}
		if notice != "" && !result.IsError {
			result.Content = append(result.Content, mcp.NewTextContent(notice))
		}
		if result.IsError || output == nil {
			return result, nil
		}
		if result.StructuredContent == nil {
			h.recordMissingStructuredContent(ctx, toolName)
			return result, nil
		}
		if err := validateSchemaValue(output.validator, result.StructuredContent, false); err != nil {
			path, constraint := validationMetadata(err, output.topLevelProperties)
			h.recordValidationMismatch(ctx, toolName, "output", path, constraint)
		}
		return result, nil
	}
}

// validateArguments validates the exact wire bytes when the SDK preserved
// them, avoiding the marshal round-trip of the decoded argument tree.
func validateArguments(schema *jsonschema.Schema, req mcp.CallToolRequest) error {
	raw := req.Params.RawArguments
	if len(raw) == 0 {
		return validateSchemaValue(schema, req.Params.Arguments, true)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode validation value: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return schema.Validate(doc)
}

func validateSchemaValue(schema *jsonschema.Schema, value any, nilAsObject bool) error {
	if value == nil && nilAsObject {
		value = map[string]any{}
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode validation value: %w", err)
	}
	normalized, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("decode validation value: %w", err)
	}
	return schema.Validate(normalized)
}

// inputValidationNotice is appended to a successful result when the
// arguments did not match the advertised schema. Wording stays soft on
// purpose: the schema layer cannot know whether the handler ignored the
// value, replaced it with a default, or normalized it anyway (a too-narrow
// schema on our side also lands here until telemetry drives a widening fix).
func inputValidationNotice(err error) string {
	detail := strings.Join(strings.Fields(err.Error()), " ")
	return fmt.Sprintf(
		"%s the arguments did not fully match this tool's input schema (%s). The call still ran best-effort: mismatched values may have been ignored or replaced with defaults. Adjust the flagged parameter(s) and re-call if the results look off.",
		inputValidationNoticePrefix, boundedErrorDetail(detail))
}

func validationMetadata(err error, topLevelProperties map[string]struct{}) (string, string) {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return "<root>", "decode"
	}
	leaf := validationErr
	for len(leaf.Causes) > 0 {
		leaf = leaf.Causes[0]
	}
	path := boundedSchemaPath(leaf.InstanceLocation, topLevelProperties)
	constraint := "schema"
	if leaf.ErrorKind != nil {
		keywordPath := leaf.ErrorKind.KeywordPath()
		if len(keywordPath) > 0 {
			constraint = boundedMetadata(keywordPath[len(keywordPath)-1])
		}
	}
	return path, constraint
}

func boundedSchemaPath(segments []string, topLevelProperties map[string]struct{}) string {
	if len(segments) == 0 {
		return "<root>"
	}
	var parts []string
	if _, ok := topLevelProperties[segments[0]]; ok {
		parts = append(parts, segments[0])
	} else {
		parts = append(parts, "{}")
	}
	for _, segment := range segments[1:] {
		if _, err := strconv.Atoi(segment); err == nil {
			parts = append(parts, "[]")
		} else {
			parts = append(parts, "{}")
		}
	}
	return boundedMetadata("/" + strings.Join(parts, "/"))
}

func boundedMetadata(value string) string {
	value = strings.TrimSpace(strings.SplitN(value, "\n", 2)[0])
	if len(value) > maxValidationMetadataLength {
		value = value[:maxValidationMetadataLength]
	}
	return value
}

func (h *Handler) recordValidationMismatch(ctx context.Context, toolName, direction, path, constraint string) {
	if h.warnValidationOnce(toolName, direction, path, constraint) {
		h.logger.WarnContext(ctx, "tool schema validation mismatch (further identical mismatches suppressed; see mcp.tool.validation.mismatches)",
			slog.String("gen_ai.tool.name", toolName),
			slog.String("validation.direction", direction),
			slog.String("validation.path", path),
			slog.String("validation.constraint", constraint))
	}
	if h.meters != nil {
		h.meters.ToolValidationMismatches.Add(ctx, 1, metric.WithAttributes(
			attribute.String("gen_ai.tool.name", toolName),
			attribute.String("validation.direction", direction),
			attribute.String("validation.path", path),
			attribute.String("validation.constraint", constraint)))
	}
}

func (h *Handler) recordSchemaCompileFailure(ctx context.Context, toolName, direction string, err error) {
	h.logger.ErrorContext(ctx, "tool schema compilation failed; validation disabled for schema",
		slog.String("gen_ai.tool.name", toolName),
		slog.String("validation.direction", direction),
		slog.String("error", boundedMetadata(err.Error())))
	if h.meters != nil {
		h.meters.ToolSchemaCompileFailures.Add(ctx, 1, metric.WithAttributes(
			attribute.String("gen_ai.tool.name", toolName),
			attribute.String("validation.direction", direction)))
	}
}

func (h *Handler) recordMissingStructuredContent(ctx context.Context, toolName string) {
	if h.warnValidationOnce(toolName, "output", "<root>", "missing_structured_content") {
		h.logger.WarnContext(ctx, "successful schema-declaring tool returned no structured content (further occurrences suppressed; see mcp.tool.output.missing_structured_content)",
			slog.String("gen_ai.tool.name", toolName))
	}
	if h.meters != nil {
		h.meters.ToolOutputMissingStructuredContent.Add(ctx, 1, metric.WithAttributes(
			attribute.String("gen_ai.tool.name", toolName)))
	}
}

func normalizeToolSchemas(tool *mcp.Tool) {
	if len(tool.RawInputSchema) > 0 {
		tool.RawInputSchema = normalizeRawInputSchema(tool.RawInputSchema)
	} else {
		normalizeToolArgumentsSchema(&tool.InputSchema)
	}

	if len(tool.RawOutputSchema) > 0 {
		tool.RawOutputSchema = normalizeRawSchema(tool.RawOutputSchema)
	} else if tool.OutputSchema.Type != "" {
		normalizeToolOutputSchema(&tool.OutputSchema)
	}
}

func normalizeRawInputSchema(raw json.RawMessage) json.RawMessage {
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}
	normalized := openInputObjects(normalizeJSONSchema(schema))
	b, err := json.Marshal(normalized)
	if err != nil {
		return raw
	}
	return b
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
		schema.Properties[name] = openInputObjects(normalizeJSONSchema(propertySchema))
	}
	for name, defSchema := range schema.Defs {
		schema.Defs[name] = openInputObjects(normalizeJSONSchema(defSchema))
	}
	if schema.AdditionalProperties != nil {
		if closed, ok := schema.AdditionalProperties.(bool); ok && !closed {
			schema.AdditionalProperties = nil
		} else {
			schema.AdditionalProperties = openInputObjects(normalizeJSONSchema(schema.AdditionalProperties))
		}
	}
}

func openInputObjects(schema any) any {
	switch typed := schema.(type) {
	case map[string]any:
		if closed, ok := typed["additionalProperties"].(bool); ok && !closed {
			delete(typed, "additionalProperties")
		}
		for key, value := range typed {
			typed[key] = openInputObjects(value)
		}
	case []any:
		for i, value := range typed {
			typed[i] = openInputObjects(value)
		}
	}
	return schema
}

// warnValidationOnce reports whether this (tool, direction, path, constraint)
// key has not been logged yet this process. Metrics stay exact per event; only
// the WARN log is deduplicated so a looping client cannot flood logs.
func (h *Handler) warnValidationOnce(toolName, direction, path, constraint string) bool {
	key := toolName + "|" + direction + "|" + path + "|" + constraint
	_, seen := h.validationWarned.LoadOrStore(key, struct{}{})
	return !seen
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
