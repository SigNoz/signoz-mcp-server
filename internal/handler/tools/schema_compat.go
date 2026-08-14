package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/google/jsonschema-go/jsonschema"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	// InputValidationNoticePrefix marks the advisory text block appended to a
	// successful result whose arguments mismatched the advertised schema.
	inputValidationNoticePrefix = "Input validation notice:"
	maxValidationMetadataLength = 96
	validationLogInterval       = time.Minute
	inputValidationNoticeAdvice = " The call still ran best-effort: mismatched values may have been ignored or replaced with defaults. Adjust the flagged parameter(s) and re-call if the results look off."
)

type validationLogState struct {
	nextAllowedUnixNano atomic.Int64
}

type compiledToolSchema struct {
	validator      *jsonschema.Resolved
	diagnostic     *jsonschema.Resolved
	properties     []string
	requiredFields []string
}

type validationMismatch struct {
	parameters []string
	path       string
	constraint string
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

func (h *Handler) addTool(s *mcp.Server, tool mcp.Tool, handler mcp.ToolHandlerFunc) {
	tool = cloneTool(tool)
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
	handler = h.errorCodeDecorator(tool.Name, handler)
	handler = h.toolInvocationDecorator(tool.Name, handler)
	h.registerTool(s, tool, handler)
}

// toolInvocationDecorator is the repository-owned safety boundary around every
// registered tool. The official SDK intentionally does not recover panics.
// Keep panic details operator-side and return the same coded error channel used
// by ordinary tool failures instead of leaking a panic value as JSON-RPC text.
func (h *Handler) toolInvocationDecorator(toolName string, next mcp.ToolHandlerFunc) mcp.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		ctx = util.SetToolName(ctx, toolName)
		if h.logger != nil {
			h.logger.DebugContext(ctx, "tool call started")
		}
		defer func() {
			if recover() == nil {
				return
			}
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "tool handler panic recovered",
					slog.String("gen_ai.tool.name", toolName),
					logpkg.ErrAttr(fmt.Errorf("tool handler panic")),
					slog.String("stack", logpkg.TruncBody(debug.Stack())))
			}
			result = InternalErrorResult("Internal server error: tool handler failed unexpectedly. Retry once; if it persists, report this as a server bug.")
			err = nil
		}()
		return next(ctx, req)
	}
}

func cloneTool(tool mcp.Tool) mcp.Tool {
	clone := tool
	clone.InputSchema = cloneSchemaValue(tool.InputSchema)
	clone.OutputSchema = cloneSchemaValue(tool.OutputSchema)
	if tool.Annotations != nil {
		annotations := *tool.Annotations
		if tool.Annotations.DestructiveHint != nil {
			value := *tool.Annotations.DestructiveHint
			annotations.DestructiveHint = &value
		}
		if tool.Annotations.OpenWorldHint != nil {
			value := *tool.Annotations.OpenWorldHint
			annotations.OpenWorldHint = &value
		}
		clone.Annotations = &annotations
	}
	clone.Icons = append([]mcp.Icon(nil), tool.Icons...)
	return clone
}

func cloneSchemaValue(schema any) any {
	if schema == nil {
		return nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Errorf("clone tool schema (type %T): marshal: %w", schema, err))
	}
	return append(json.RawMessage(nil), b...)
}

// AddTool exposes the production registration path to server composition and
// end-to-end tests while keeping all built-in registrations on h.addTool.
func (h *Handler) AddTool(s *mcp.Server, tool mcp.Tool, handler mcp.ToolHandlerFunc) {
	h.addTool(s, tool, handler)
}

func inputSchemaJSON(tool mcp.Tool) json.RawMessage {
	if tool.InputSchema == nil {
		return nil
	}
	b, _ := json.Marshal(tool.InputSchema)
	return b
}

func outputSchemaJSON(tool mcp.Tool) json.RawMessage {
	if tool.OutputSchema == nil {
		return nil
	}
	b, _ := json.Marshal(tool.OutputSchema)
	return b
}

func compileToolSchema(toolName, direction string, raw json.RawMessage) (*compiledToolSchema, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	resourceURL := fmt.Sprintf("mem:///signoz/tools/%s/%s-schema.json", toolName, direction)
	validator, err := schema.Resolve(&jsonschema.ResolveOptions{BaseURI: resourceURL})
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}

	properties := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		properties = append(properties, name)
	}
	sort.Strings(properties)
	requiredFields := append([]string(nil), schema.Required...)
	sort.Strings(requiredFields)

	var diagnostic *jsonschema.Resolved
	if len(properties) > 0 {
		// Keep only the keywords needed to validate one declared top-level
		// property at a time. Local definitions remain available so schemas such
		// as the dashboard spec can still resolve their internal references.
		probe := &jsonschema.Schema{
			Schema:      schema.Schema,
			Type:        "object",
			Properties:  schema.Properties,
			Defs:        schema.Defs,
			Definitions: schema.Definitions,
		}
		probeURL := fmt.Sprintf("mem:///signoz/tools/%s/%s-diagnostic-schema.json", toolName, direction)
		// Diagnosis is advisory. If a future schema cannot compile independently,
		// retain the primary validator and fall back to <root>/schema metadata.
		diagnostic, _ = probe.Resolve(&jsonschema.ResolveOptions{BaseURI: probeURL})
	}

	return &compiledToolSchema{
		validator:      validator,
		diagnostic:     diagnostic,
		properties:     properties,
		requiredFields: requiredFields,
	}, nil
}

// validationDecorator owns schema validation and never rejects a call.
// Input mismatches are served best-effort with an in-band notice appended to
// the successful result, so agents that read errors can self-correct while
// agents that don't still get a usable answer. Output mismatches and missing
// structured content are telemetry-only (fail open, never silent) — they are
// our defects, not the caller's.
func (h *Handler) validationDecorator(toolName string, input, output *compiledToolSchema, next mcp.ToolHandlerFunc) mcp.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var notice string
		if input != nil {
			if err := validateArguments(input.validator, req); err != nil {
				mismatch := diagnoseValidationMismatch(input, req.Params.Arguments, true)
				h.recordValidationMismatch(ctx, req, toolName, "input", mismatch.path, mismatch.constraint)
				notice = inputValidationNotice(mismatch)
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
			h.recordMissingStructuredContent(ctx, req, toolName)
			return result, nil
		}
		if err := validateSchemaValue(output.validator, result.StructuredContent, false); err != nil {
			mismatch := diagnoseValidationMismatch(output, result.StructuredContent, false)
			h.recordValidationMismatch(ctx, req, toolName, "output", mismatch.path, mismatch.constraint)
		}
		return result, nil
	}
}

func (h *Handler) errorCodeDecorator(toolName string, next mcp.ToolHandlerFunc) mcp.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := next(ctx, req)
		if err != nil || result == nil || !result.IsError {
			return result, err
		}
		result, appliedFallback := ensureCodedToolError(result)
		appendAuthorizationOperation(result, toolName)
		if !appliedFallback {
			return result, nil
		}
		if h.logger != nil {
			h.logger.WarnContext(ctx, "tool returned an uncoded error result; applying fallback",
				slog.String("gen_ai.tool.name", toolName),
				slog.String("fallback.code", CodeInternalError))
		}
		return result, nil
	}
}

// validateArguments receives the ordinary JSON tree decoded once at the
// server boundary (or by the adapter in focused handler tests).
func validateArguments(schema *jsonschema.Resolved, req mcp.CallToolRequest) error {
	value := req.Params.Arguments
	if value == nil {
		value = map[string]any{}
	}
	return schema.Validate(value)
}

func validateSchemaValue(schema *jsonschema.Resolved, value any, nilAsObject bool) error {
	if value == nil && nilAsObject {
		value = map[string]any{}
	}
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode validation value: %w", err)
	}
	if err := json.Unmarshal(b, &value); err != nil {
		return fmt.Errorf("decode validation value: %w", err)
	}
	return schema.Validate(value)
}

// diagnoseValidationMismatch attributes a failed full-schema validation using
// only repository-owned schema metadata. It never parses validator error text
// and never reflects client-provided keys into responses or metric dimensions.
func diagnoseValidationMismatch(schema *compiledToolSchema, value any, nilAsObject bool) validationMismatch {
	if value == nil && nilAsObject {
		value = map[string]any{}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return validationMismatch{path: "<root>", constraint: "type"}
	}

	constraints := make(map[string]string)
	for _, name := range schema.requiredFields {
		if _, present := object[name]; !present {
			constraints[name] = "required"
		}
	}
	if schema.diagnostic != nil {
		for _, name := range schema.properties {
			propertyValue, present := object[name]
			if !present {
				continue
			}
			if err := schema.diagnostic.Validate(map[string]any{name: propertyValue}); err != nil {
				constraints[name] = "schema"
			}
		}
	}

	parameters := make([]string, 0, len(constraints))
	for name := range constraints {
		parameters = append(parameters, name)
	}
	sort.Strings(parameters)
	if len(parameters) == 0 {
		return validationMismatch{path: "<root>", constraint: "schema"}
	}
	if len(parameters) == 1 {
		name := parameters[0]
		return validationMismatch{
			parameters: parameters,
			path:       boundedMetadata(name),
			constraint: constraints[name],
		}
	}

	constraint := constraints[parameters[0]]
	for _, name := range parameters[1:] {
		if constraints[name] != constraint {
			constraint = "schema"
			break
		}
	}
	return validationMismatch{parameters: parameters, path: "<multiple>", constraint: constraint}
}

// inputValidationNotice is appended to a successful result when the arguments
// did not match the advertised schema. Parameter names come only from the
// advertised schema; values and validator-library error text never reach the
// client. Wording stays soft because handlers may still normalize the input.
func inputValidationNotice(mismatch validationMismatch) string {
	var detail string
	switch len(mismatch.parameters) {
	case 0:
		detail = " the arguments did not fully match this tool's input schema."
	case 1:
		if mismatch.constraint == "required" {
			detail = fmt.Sprintf(" required parameter %q was missing.", mismatch.parameters[0])
		} else {
			detail = fmt.Sprintf(" parameter %q did not fully match its advertised schema.", mismatch.parameters[0])
		}
	default:
		if mismatch.constraint == "required" {
			detail = fmt.Sprintf(" required parameters %s were missing.", quotedParameterList(mismatch.parameters))
		} else {
			detail = fmt.Sprintf(" parameters %s did not fully match their advertised schemas.", quotedParameterList(mismatch.parameters))
		}
	}
	return inputValidationNoticePrefix + detail + inputValidationNoticeAdvice
}

func quotedParameterList(parameters []string) string {
	quoted := make([]string, len(parameters))
	for i, parameter := range parameters {
		quoted[i] = fmt.Sprintf("%q", parameter)
	}
	if len(quoted) == 2 {
		return quoted[0] + " and " + quoted[1]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
}

func boundedMetadata(value string) string {
	value = strings.TrimSpace(strings.SplitN(value, "\n", 2)[0])
	if len(value) > maxValidationMetadataLength {
		value = value[:maxValidationMetadataLength]
	}
	return value
}

func (h *Handler) recordValidationMismatch(ctx context.Context, req mcp.CallToolRequest, toolName, direction, path, constraint string) {
	if h.logger.Enabled(ctx, slog.LevelWarn) {
		if h.shouldLogValidationRequest(toolName, direction, path, constraint) {
			h.logger.WarnContext(ctx, "tool schema validation mismatch (repeats rate-limited; see mcp.tool.validation.mismatches)",
				slog.String("gen_ai.tool.name", toolName),
				slog.String("validation.direction", direction),
				slog.String("validation.path", path),
				slog.String("validation.constraint", constraint),
				slog.String("mcp.request", logpkg.RedactedTruncAny(req)))
		}
	}
	if h.meters != nil {
		attrs := []attribute.KeyValue{
			attribute.String("gen_ai.tool.name", toolName),
			attribute.String("validation.direction", direction),
			attribute.String("validation.path", path),
			attribute.String("validation.constraint", constraint),
		}
		attrs = otelpkg.AppendClientSource(ctx, attrs)
		h.meters.ToolValidationMismatches.Add(ctx, 1, metric.WithAttributes(attrs...))
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

func (h *Handler) recordMissingStructuredContent(ctx context.Context, req mcp.CallToolRequest, toolName string) {
	if h.logger.Enabled(ctx, slog.LevelWarn) {
		if h.shouldLogValidationRequest(toolName, "output", "<root>", "missing_structured_content") {
			h.logger.WarnContext(ctx, "successful schema-declaring tool returned no structured content (repeats rate-limited; see mcp.tool.output.missing_structured_content)",
				slog.String("gen_ai.tool.name", toolName),
				slog.String("mcp.request", logpkg.RedactedTruncAny(req)))
		}
	}
	if h.meters != nil {
		attrs := []attribute.KeyValue{attribute.String("gen_ai.tool.name", toolName)}
		attrs = otelpkg.AppendClientSource(ctx, attrs)
		h.meters.ToolOutputMissingStructuredContent.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}

func (h *Handler) shouldLogValidationRequest(toolName, direction, path, constraint string) bool {
	key := toolName + "|" + direction + "|" + path + "|" + constraint
	value, _ := h.validationLogs.LoadOrStore(key, &validationLogState{})
	state := value.(*validationLogState)

	now := time.Now().UnixNano()
	for {
		next := state.nextAllowedUnixNano.Load()
		if now < next {
			return false
		}
		if state.nextAllowedUnixNano.CompareAndSwap(next, now+validationLogInterval.Nanoseconds()) {
			return true
		}
	}
}

func normalizeToolSchemas(tool *mcp.Tool) {
	tool.InputSchema = normalizeSchemaValue(tool.InputSchema, true)
	tool.OutputSchema = normalizeSchemaValue(tool.OutputSchema, false)
}

func normalizeSchemaValue(schema any, input bool) any {
	if schema == nil {
		return nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	if input {
		return normalizeRawInputSchema(b)
	}
	return normalizeRawSchema(b)
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
