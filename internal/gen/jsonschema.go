package gen

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
)

// BuildToolSchemaSkeleton translates an Operation into a JSON Schema that
// describes its MCP tool input — with body shapes kept as {"$ref":
// "#/$defs/<ComponentName>"} rather than inlined. The returned defs slice
// is the *transitive* closure of component schemas the skeleton depends on
// (sorted, deduplicated). The caller combines skeleton + defs at runtime
// via a $defs block to produce the final self-contained schema.
func BuildToolSchemaSkeleton(doc *openapi3.T, op Operation) (json.RawMessage, []string, error) {
	c := &converter{doc: doc, preserveRefs: true, seen: map[string]bool{}}

	properties := map[string]any{}
	var required []string

	for _, p := range op.PathParams {
		properties[p.Name] = c.param(p)
		required = append(required, p.Name)
	}
	for _, p := range op.QueryParams {
		properties[p.Name] = c.param(p)
		if p.Required {
			required = append(required, p.Name)
		}
	}
	if op.HasBody {
		// When the body is a named component, emit {"$ref": "#/$defs/X"}
		// directly so the tool skeleton stays small and the def content
		// lives in its own zz_generated_def_X.json file. Inline bodies
		// (no ref name) still walk the schema contents.
		if op.BodyDesc != "" && op.BodySchema != nil {
			c.recordRef(op.BodyDesc)
			properties["body"] = map[string]any{"$ref": "#/$defs/" + op.BodyDesc}
		} else {
			body, err := c.convert(op.BodySchema)
			if err != nil {
				return nil, nil, fmt.Errorf("body schema: %w", err)
			}
			properties["body"] = body
		}
		if op.BodyRequired {
			required = append(required, "body")
		}
	}

	sort.Strings(required)
	out := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		out["required"] = required
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}

	closure, err := ClosureOf(doc, c.refs())
	if err != nil {
		return nil, nil, err
	}
	return raw, closure, nil
}

// BuildDefSchema returns the JSON Schema for a single OpenAPI component,
// keeping internal $refs to other components. Runtime compose then pulls
// matching defs side-by-side into a tool's $defs block.
func BuildDefSchema(doc *openapi3.T, name string) (json.RawMessage, error) {
	if doc.Components == nil {
		return nil, fmt.Errorf("no components")
	}
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		return nil, fmt.Errorf("component %q not found", name)
	}
	c := &converter{doc: doc, preserveRefs: true, seen: map[string]bool{}}
	out, err := c.convert(ref.Value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ClosureOf returns the transitive closure of component-schema names
// reachable from seed, walking through $refs nested inside each component.
// The returned slice is sorted and deduplicated. Missing refs are skipped
// silently.
func ClosureOf(doc *openapi3.T, seed []string) ([]string, error) {
	if doc.Components == nil {
		return nil, nil
	}
	found := map[string]bool{}
	queue := append([]string(nil), seed...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if found[name] {
			continue
		}
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			continue
		}
		found[name] = true

		// Walk this def to collect its own refs.
		c := &converter{doc: doc, preserveRefs: true, seen: map[string]bool{}}
		if _, err := c.convert(ref.Value); err != nil {
			return nil, fmt.Errorf("walk def %q: %w", name, err)
		}
		for _, r := range c.refs() {
			if !found[r] {
				queue = append(queue, r)
			}
		}
	}
	out := make([]string, 0, len(found))
	for n := range found {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// converter walks an OpenAPI schema and emits JSON Schema. When preserveRefs
// is true, $refs are kept as {"$ref": "#/$defs/<Name>"} and the ref names
// are collected in refsSeen for the caller.
type converter struct {
	doc          *openapi3.T
	seen         map[string]bool // cycle guard; only active when !preserveRefs
	preserveRefs bool
	refsSeen     map[string]bool
}

func (c *converter) recordRef(name string) {
	if c.refsSeen == nil {
		c.refsSeen = map[string]bool{}
	}
	c.refsSeen[name] = true
}

func (c *converter) refs() []string {
	out := make([]string, 0, len(c.refsSeen))
	for n := range c.refsSeen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (c *converter) param(p Param) map[string]any {
	if p.Schema == nil {
		return map[string]any{"type": "string"}
	}
	out, _ := c.convert(p.Schema)
	if p.Description != "" {
		if _, exists := out["description"]; !exists {
			out["description"] = p.Description
		}
	}
	return out
}

// convert produces the JSON-schema representation of an OpenAPI schema.
// In preserveRefs mode, $refs are kept as {"$ref": ...} rather than inlined.
func (c *converter) convert(s *openapi3.Schema) (map[string]any, error) {
	if s == nil {
		return map[string]any{}, nil
	}

	out := map[string]any{}

	if s.Type != nil && len(*s.Type) > 0 {
		types := []string(*s.Type)
		if s.Nullable {
			types = append(types, "null")
		}
		if len(types) == 1 {
			out["type"] = types[0]
		} else {
			out["type"] = types
		}
	} else if s.Nullable {
		out["type"] = []string{"null"}
	}

	if s.Format != "" {
		out["format"] = s.Format
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if s.Default != nil {
		out["default"] = s.Default
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if s.Pattern != "" {
		out["pattern"] = s.Pattern
	}
	if s.Min != nil {
		out["minimum"] = *s.Min
	}
	if s.Max != nil {
		out["maximum"] = *s.Max
	}
	if s.ExclusiveMin && s.Min != nil {
		out["exclusiveMinimum"] = *s.Min
		delete(out, "minimum")
	}
	if s.ExclusiveMax && s.Max != nil {
		out["exclusiveMaximum"] = *s.Max
		delete(out, "maximum")
	}
	if s.MinLength != 0 {
		out["minLength"] = s.MinLength
	}
	if s.MaxLength != nil {
		out["maxLength"] = *s.MaxLength
	}
	if s.MinItems != 0 {
		out["minItems"] = s.MinItems
	}
	if s.MaxItems != nil {
		out["maxItems"] = *s.MaxItems
	}
	if s.UniqueItems {
		out["uniqueItems"] = true
	}

	if s.Items != nil {
		sub, err := c.resolve(s.Items)
		if err != nil {
			return nil, err
		}
		out["items"] = sub
	}

	if len(s.Properties) > 0 {
		propNames := make([]string, 0, len(s.Properties))
		for n := range s.Properties {
			propNames = append(propNames, n)
		}
		sort.Strings(propNames)
		props := make(map[string]any, len(propNames))
		for _, n := range propNames {
			sub, err := c.resolve(s.Properties[n])
			if err != nil {
				return nil, err
			}
			props[n] = sub
		}
		out["properties"] = props
	}

	if len(s.Required) > 0 {
		sorted := append([]string(nil), s.Required...)
		sort.Strings(sorted)
		out["required"] = sorted
	}

	if s.AdditionalProperties.Has != nil {
		out["additionalProperties"] = *s.AdditionalProperties.Has
	} else if s.AdditionalProperties.Schema != nil {
		sub, err := c.resolve(s.AdditionalProperties.Schema)
		if err != nil {
			return nil, err
		}
		out["additionalProperties"] = sub
	}

	if len(s.OneOf) > 0 {
		arr, err := c.resolveList(s.OneOf)
		if err != nil {
			return nil, err
		}
		out["oneOf"] = arr
	}
	if len(s.AnyOf) > 0 {
		arr, err := c.resolveList(s.AnyOf)
		if err != nil {
			return nil, err
		}
		out["anyOf"] = arr
	}
	if len(s.AllOf) > 0 {
		arr, err := c.resolveList(s.AllOf)
		if err != nil {
			return nil, err
		}
		out["allOf"] = arr
	}

	return out, nil
}

// resolve handles a SchemaRef. In preserveRefs mode, named component refs
// are emitted as {"$ref": "#/$defs/<Name>"} and the name is recorded for
// the caller to include in the tool's def list. Otherwise the target is
// inlined (legacy path, still useful for standalone def walks).
func (c *converter) resolve(ref *openapi3.SchemaRef) (map[string]any, error) {
	if ref == nil {
		return map[string]any{}, nil
	}
	if ref.Ref != "" {
		name := schemaRefName(ref.Ref)
		if c.preserveRefs {
			c.recordRef(name)
			return map[string]any{"$ref": "#/$defs/" + name}, nil
		}
		if c.seen[name] {
			return map[string]any{"description": "ref: " + name}, nil
		}
		c.seen[name] = true
		defer delete(c.seen, name)
		if c.doc.Components != nil {
			if target := c.doc.Components.Schemas[name]; target != nil && target.Value != nil {
				return c.convert(target.Value)
			}
		}
	}
	return c.convert(ref.Value)
}

func (c *converter) resolveList(refs openapi3.SchemaRefs) ([]any, error) {
	out := make([]any, 0, len(refs))
	for _, r := range refs {
		sub, err := c.resolve(r)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, nil
}
