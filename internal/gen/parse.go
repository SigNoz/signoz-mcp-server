package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Operation is the generator-normalized shape of a single OpenAPI operation.
// It drops metadata the emitter doesn't use and pre-computes fields
// (ToolName, HandlerFunc, InputType) that both the handler and types emitters
// need to agree on.
type Operation struct {
	OperationID string
	Method      string // "GET"/"POST"/...
	Path        string // "/api/v1/channels/{id}"
	Tag         string
	Summary     string
	Description string

	PathParams  []Param
	QueryParams []Param

	// HasBody is true when the operation accepts a JSON request body.
	// BodySchema is the resolved openapi3 schema for the body (may be a
	// $ref or inline); the JSON Schema translator walks it directly so
	// every OpenAPI validator (enum, format, pattern, min/max, required,
	// additionalProperties, oneOf/anyOf) reaches the emitted schema.
	HasBody      bool
	BodyRequired bool
	BodyDesc     string
	BodySchema   *openapi3.Schema

	// HasResponse is true when the operation declares an
	// application/json response body on the success status (200 or 201).
	// ResponseSchema / ResponseSchemaRef mirror the body fields and feed
	// the output-schema emitter.
	HasResponse       bool
	ResponseSchemaRef string
	ResponseSchema    *openapi3.Schema

	// Derived identifiers.
	ToolName    string // "signoz_get_channel_by_id"
	HandlerFunc string // "handleSignozGetChannelByID"
	InputType   string // "GetChannelByIDInput"

	// Hints used to set MCP tool annotations.
	ReadOnly    bool
	Destructive bool
}

// Param describes a single path or query parameter. GoType/Required drive
// the generated *Input struct; Schema is the raw OpenAPI schema carried
// through for the JSON Schema translator so validators (enum, format,
// pattern, min/max) reach the emitted schema with full fidelity.
type Param struct {
	Name        string // OpenAPI name (may contain dashes or dots)
	GoName      string // exported Go identifier for the struct field
	GoType      string // "string", "int64", "float64", "bool"
	Required    bool
	Description string

	// Schema is the resolved OpenAPI parameter schema; used by the JSON
	// Schema translator. May be nil for parameters without a schema.
	Schema *openapi3.Schema
}

// Load parses the spec at path and returns the emittable operations sorted by
// (tag, operationId) for deterministic output plus the raw kin-openapi doc so
// downstream stages (schema collection, etc.) can resolve $refs against
// components without reparsing. Excluded operations, deprecated operations,
// operations whose tool name collides with a curated tool, and operations
// with non-JSON request bodies are dropped silently.
func Load(path string) ([]Operation, *openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("load spec: %w", err)
	}

	var ops []Operation
	for rawPath, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			o, ok, err := normalize(rawPath, method, op, item)
			if err != nil {
				return nil, nil, fmt.Errorf("%s %s: %w", method, rawPath, err)
			}
			if !ok {
				continue
			}
			ops = append(ops, o)
		}
	}

	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Tag != ops[j].Tag {
			return ops[i].Tag < ops[j].Tag
		}
		return ops[i].OperationID < ops[j].OperationID
	})
	return ops, doc, nil
}

func normalize(path, method string, op *openapi3.Operation, item *openapi3.PathItem) (Operation, bool, error) {
	if op.OperationID == "" {
		return Operation{}, false, nil
	}
	if isDeprecated(op.OperationID) || excludedOperationIDs[op.OperationID] {
		return Operation{}, false, nil
	}
	name := toolName(op.OperationID)
	if curatedToolNames[name] {
		return Operation{}, false, nil
	}

	tag := "misc"
	if len(op.Tags) > 0 {
		tag = op.Tags[0]
	}

	out := Operation{
		OperationID: op.OperationID,
		Method:      strings.ToUpper(method),
		Path:        path,
		Tag:         tag,
		Summary:     op.Summary,
		Description: op.Description,
		ToolName:    name,
		HandlerFunc: "genHandle" + goIdent(op.OperationID),
		InputType:   goIdent(op.OperationID) + "Input",
		ReadOnly:    strings.EqualFold(method, "get"),
		Destructive: strings.EqualFold(method, "delete"),
	}

	// Parameters on the path item are inherited by every operation.
	for _, p := range item.Parameters {
		if err := addParam(&out, p.Value); err != nil {
			return Operation{}, false, err
		}
	}
	for _, p := range op.Parameters {
		if err := addParam(&out, p.Value); err != nil {
			return Operation{}, false, err
		}
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		body := op.RequestBody.Value
		// Only wrap operations that accept JSON. Form-encoded bodies need
		// bespoke handling that the generator does not emit.
		if mt, ok := body.Content["application/json"]; ok {
			out.HasBody = true
			out.BodyRequired = body.Required
			if mt.Schema != nil {
				if mt.Schema.Ref != "" {
					out.BodyDesc = schemaRefName(mt.Schema.Ref)
				}
				out.BodySchema = mt.Schema.Value
			}
			if out.BodyDesc == "" && body.Description != "" {
				out.BodyDesc = body.Description
			}
		} else {
			// Non-JSON body (form/multipart/etc.) — skip this operation.
			return Operation{}, false, nil
		}
	}

	// Pull the success response's JSON schema. Prefer 200, fall back to
	// 201 (creates). 204 (no content) and other status codes are skipped.
	for _, status := range []string{"200", "201"} {
		respRef := op.Responses.Status(parseStatus(status))
		if respRef == nil || respRef.Value == nil {
			continue
		}
		mt, ok := respRef.Value.Content["application/json"]
		if !ok || mt.Schema == nil {
			continue
		}
		out.HasResponse = true
		if mt.Schema.Ref != "" {
			out.ResponseSchemaRef = schemaRefName(mt.Schema.Ref)
		}
		out.ResponseSchema = mt.Schema.Value
		break
	}

	return out, true, nil
}

// parseStatus turns "200"/"201" into the int form openapi3.Responses.Status
// expects. Returns 0 on failure (caller treats nil ref as "no response").
func parseStatus(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func addParam(out *Operation, p *openapi3.Parameter) error {
	if p == nil {
		return nil
	}
	goType := "string"
	var schema *openapi3.Schema
	if p.Schema != nil && p.Schema.Value != nil {
		schema = p.Schema.Value
		goType = schemaGoType(schema)
	}
	param := Param{
		Name:        p.Name,
		GoName:      goIdent(p.Name),
		GoType:      goType,
		Required:    p.Required,
		Description: p.Description,
		Schema:      schema,
	}
	switch p.In {
	case "path":
		param.Required = true // path params are always required
		out.PathParams = append(out.PathParams, param)
	case "query":
		out.QueryParams = append(out.QueryParams, param)
	case "header", "cookie":
		// Header and cookie parameters aren't supported in the generated
		// handler surface — they're typically auth-related and already
		// injected by the client layer. Ignore silently.
	}
	return nil
}

// schemaGoType maps primitive OpenAPI schema types to Go types used in
// generated request structs. Everything non-primitive falls back to string.
func schemaGoType(s *openapi3.Schema) string {
	if s == nil || s.Type == nil {
		return "string"
	}
	// openapi3.Types is a slice; most params specify exactly one type.
	for _, t := range *s.Type {
		switch t {
		case "integer":
			return "int64"
		case "number":
			return "float64"
		case "boolean":
			return "bool"
		case "string":
			return "string"
		}
	}
	return "string"
}

func schemaRefName(ref string) string {
	// "#/components/schemas/ConfigReceiver" -> "ConfigReceiver"
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}
