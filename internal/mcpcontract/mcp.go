// Package mcpcontract contains the small set of descriptor builders and request
// adapters used by the SigNoz tool catalog. Protocol execution is delegated to
// the official MCP Go SDK.
package mcpcontract

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	official "github.com/modelcontextprotocol/go-sdk/mcp"
)

type (
	Server               = official.Server
	Tool                 = official.Tool
	ToolOption           func(*Tool)
	PropertyOption       func(map[string]any)
	CallToolResult       = official.CallToolResult
	Content              = official.Content
	TextContent          = official.TextContent
	Resource             = official.Resource
	ResourceTemplate     = official.ResourceTemplate
	ResourceContents     = official.ResourceContents
	TextResourceContents = official.ResourceContents
	Prompt               = official.Prompt
	PromptMessage        = official.PromptMessage
	GetPromptResult      = official.GetPromptResult
	Role                 = official.Role
	Icon                 = official.Icon
)

const RoleUser Role = "user"

type CallToolParams struct {
	Name         string
	Arguments    any
	RawArguments json.RawMessage
}

type CallToolRequest struct {
	Params CallToolParams
}

func (r CallToolRequest) GetArguments() map[string]any {
	args, _ := r.Params.Arguments.(map[string]any)
	if args == nil {
		return map[string]any{}
	}
	return args
}

type ReadResourceParams struct{ URI string }
type ReadResourceRequest struct{ Params ReadResourceParams }

type GetPromptParams struct {
	Name      string
	Arguments map[string]string
}
type GetPromptRequest struct{ Params GetPromptParams }

type ToolHandlerFunc func(context.Context, CallToolRequest) (*CallToolResult, error)
type ResourceHandlerFunc func(context.Context, ReadResourceRequest) ([]ResourceContents, error)
type ResourceTemplateHandlerFunc = ResourceHandlerFunc
type PromptHandlerFunc func(context.Context, GetPromptRequest) (*GetPromptResult, error)

type decodedToolArguments struct{ value any }

// CacheToolArguments lets the server observer and tool adapter share one JSON
// decode. Focused handler tests that bypass the observer still decode here.
func CacheToolArguments(ctx context.Context, raw json.RawMessage) (context.Context, any) {
	var decoded any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return context.WithValue(ctx, decodedToolArguments{}, decodedToolArguments{value: decoded}), decoded
}

func AdaptToolHandler(next ToolHandlerFunc) official.ToolHandler {
	return func(ctx context.Context, req *official.CallToolRequest) (*official.CallToolResult, error) {
		raw := json.RawMessage(nil)
		name := ""
		if req != nil && req.Params != nil {
			name = req.Params.Name
			raw = req.Params.Arguments
		}
		cached, ok := ctx.Value(decodedToolArguments{}).(decodedToolArguments)
		if !ok {
			ctx, cached.value = CacheToolArguments(ctx, raw)
		}
		return next(ctx, CallToolRequest{Params: CallToolParams{
			Name:         name,
			Arguments:    cached.value,
			RawArguments: raw,
		}})
	}
}

func AdaptResourceHandler(next ResourceHandlerFunc) official.ResourceHandler {
	return func(ctx context.Context, req *official.ReadResourceRequest) (*official.ReadResourceResult, error) {
		var uri string
		if req != nil && req.Params != nil {
			uri = req.Params.URI
		}
		contents, err := next(ctx, ReadResourceRequest{Params: ReadResourceParams{URI: uri}})
		if err != nil {
			return nil, err
		}
		out := make([]*official.ResourceContents, len(contents))
		for i := range contents {
			content := contents[i]
			out[i] = &content
		}
		return &official.ReadResourceResult{Contents: out}, nil
	}
}

func AdaptPromptHandler(next PromptHandlerFunc) official.PromptHandler {
	return func(ctx context.Context, req *official.GetPromptRequest) (*official.GetPromptResult, error) {
		var params GetPromptParams
		if req != nil && req.Params != nil {
			params.Name = req.Params.Name
			params.Arguments = req.Params.Arguments
		}
		return next(ctx, GetPromptRequest{Params: params})
	}
}

func NewTool(name string, opts ...ToolOption) Tool {
	trueValue := true
	t := Tool{
		Name: name,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{}, "required": []string{},
		},
		Annotations: &official.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &trueValue,
			IdempotentHint: false, OpenWorldHint: &trueValue,
		},
	}
	for _, opt := range opts {
		opt(&t)
	}
	return t
}

func WithDescription(description string) ToolOption {
	return func(t *Tool) { t.Description = description }
}

func WithInputSchema[T any]() ToolOption {
	return func(t *Tool) {
		schema, err := jsonschema.For[T](&jsonschema.ForOptions{IgnoreInvalidTypes: true})
		if err != nil {
			panic(fmt.Errorf("infer input schema for tool %q from Go type %s: %w", t.Name, reflect.TypeFor[T](), err))
		}
		t.InputSchema = schema
	}
}

func WithOutputSchema[T any]() ToolOption {
	return func(t *Tool) {
		schema, err := jsonschema.For[T](&jsonschema.ForOptions{IgnoreInvalidTypes: true})
		if err != nil {
			panic(fmt.Errorf("infer output schema for tool %q from Go type %s: %w", t.Name, reflect.TypeFor[T](), err))
		}
		wire := mustSchemaMap(schema, t.Name, "output")
		if wire["type"] == "object" {
			if _, ok := wire["properties"]; !ok {
				wire["properties"] = map[string]any{}
			}
			if _, ok := wire["required"]; !ok {
				wire["required"] = []string{}
			}
		}
		t.OutputSchema = wire
	}
}

func WithReadOnlyHintAnnotation(value bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).ReadOnlyHint = value }
}
func WithDestructiveHintAnnotation(value bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).DestructiveHint = boolPtr(value) }
}
func WithIdempotentHintAnnotation(value bool) ToolOption {
	return func(t *Tool) { ensureAnnotations(t).IdempotentHint = value }
}

func ensureAnnotations(t *Tool) *official.ToolAnnotations {
	if t.Annotations == nil {
		t.Annotations = &official.ToolAnnotations{}
	}
	return t.Annotations
}

func boolPtr(v bool) *bool { return &v }

func Description(desc string) PropertyOption {
	return func(schema map[string]any) { schema["description"] = desc }
}
func Required() PropertyOption {
	return func(schema map[string]any) { schema["required"] = true }
}
func DefaultString(value string) PropertyOption {
	return func(schema map[string]any) { schema["default"] = value }
}
func Enum(values ...string) PropertyOption {
	return func(schema map[string]any) { schema["enum"] = values }
}
func Properties(properties map[string]any) PropertyOption {
	return func(schema map[string]any) { schema["properties"] = properties }
}
func AdditionalProperties(value any) PropertyOption {
	return func(schema map[string]any) { schema["additionalProperties"] = value }
}
func WithStringItems(opts ...PropertyOption) PropertyOption {
	return func(schema map[string]any) {
		item := map[string]any{"type": "string"}
		for _, opt := range opts {
			opt(item)
		}
		schema["items"] = item
	}
}

func WithString(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "string", false, opts...)
}
func WithBoolean(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "boolean", false, opts...)
}
func WithNumber(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "number", false, opts...)
}
func WithObject(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "object", true, opts...)
}
func WithArray(name string, opts ...PropertyOption) ToolOption {
	return withProperty(name, "array", false, opts...)
}

func withProperty(name, typ string, object bool, opts ...PropertyOption) ToolOption {
	return func(t *Tool) {
		root := inputSchemaMap(t)
		property := map[string]any{"type": typ}
		if object {
			property["properties"] = map[string]any{}
		}
		for _, opt := range opts {
			opt(property)
		}
		if required, _ := property["required"].(bool); required {
			delete(property, "required")
			root["required"] = append(requiredStrings(root["required"]), name)
		}
		properties, _ := root["properties"].(map[string]any)
		if properties == nil {
			properties = map[string]any{}
			root["properties"] = properties
		}
		properties[name] = property
		t.InputSchema = root
	}
}

func inputSchemaMap(t *Tool) map[string]any {
	if schema, ok := t.InputSchema.(map[string]any); ok {
		return schema
	}
	return mustSchemaMap(t.InputSchema, t.Name, "input")
}

func mustSchemaMap(schema any, toolName, direction string) map[string]any {
	b, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Errorf("marshal %s schema for tool %q (type %T): %w", direction, toolName, schema, err))
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		panic(fmt.Errorf("decode %s schema for tool %q (type %T) as object: %w", direction, toolName, schema, err))
	}
	if wire == nil {
		panic(fmt.Errorf("decode %s schema for tool %q (type %T) as object: got null", direction, toolName, schema))
	}
	return wire
}

func requiredStrings(v any) []string {
	switch values := v.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

type ResourceOption func(*Resource)

func NewResource(uri, name string, opts ...ResourceOption) Resource {
	r := Resource{URI: uri, Name: name}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}
func WithResourceDescription(v string) ResourceOption { return func(r *Resource) { r.Description = v } }
func WithMIMEType(v string) ResourceOption            { return func(r *Resource) { r.MIMEType = v } }
func WithResourceSize(v int64) ResourceOption {
	return func(r *Resource) {
		if v >= 0 {
			r.Size = v
		}
	}
}

type ResourceTemplateOption func(*ResourceTemplate)

func NewResourceTemplate(uri, name string, opts ...ResourceTemplateOption) ResourceTemplate {
	t := ResourceTemplate{URITemplate: uri, Name: name}
	for _, opt := range opts {
		opt(&t)
	}
	return t
}
func WithTemplateDescription(v string) ResourceTemplateOption {
	return func(t *ResourceTemplate) { t.Description = v }
}
func WithTemplateMIMEType(v string) ResourceTemplateOption {
	return func(t *ResourceTemplate) { t.MIMEType = v }
}

type PromptOption func(*Prompt)
type ArgumentOption func(*official.PromptArgument)

func NewPrompt(name string, opts ...PromptOption) Prompt {
	p := Prompt{Name: name}
	for _, opt := range opts {
		opt(&p)
	}
	return p
}
func WithPromptDescription(v string) PromptOption { return func(p *Prompt) { p.Description = v } }
func WithArgument(name string, opts ...ArgumentOption) PromptOption {
	return func(p *Prompt) {
		a := &official.PromptArgument{Name: name}
		for _, opt := range opts {
			opt(a)
		}
		p.Arguments = append(p.Arguments, a)
	}
}
func ArgumentDescription(v string) ArgumentOption {
	return func(a *official.PromptArgument) { a.Description = v }
}
func RequiredArgument() ArgumentOption { return func(a *official.PromptArgument) { a.Required = true } }

func NewTextContent(text string) *TextContent { return &TextContent{Text: text} }
func AsTextContent(content Content) (*TextContent, bool) {
	value, ok := content.(*TextContent)
	return value, ok
}
func NewToolResultText(text string) *CallToolResult {
	return &CallToolResult{Content: []Content{NewTextContent(text)}}
}
func NewToolResultError(text string) *CallToolResult {
	return &CallToolResult{Content: []Content{NewTextContent(text)}, IsError: true}
}
func NewToolResultStructured(structured any, fallbackText string) *CallToolResult {
	return &CallToolResult{Content: []Content{NewTextContent(fallbackText)}, StructuredContent: structured}
}
