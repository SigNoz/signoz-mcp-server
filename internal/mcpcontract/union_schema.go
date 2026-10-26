package mcpcontract

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

var rawMessageType = reflect.TypeFor[json.RawMessage]()

// unionTypeSchemas maps every oapi-codegen union type reachable from t to a
// schema listing the JSON types of its variants. The generated union hides its
// variants behind one unexported json.RawMessage field, which reflection would
// otherwise render as an empty object and so reject a string or number value.
func unionTypeSchemas(t reflect.Type) map[reflect.Type]*jsonschema.Schema {
	schemas := map[reflect.Type]*jsonschema.Schema{}
	visited := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		if visited[t] {
			return
		}
		visited[t] = true
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t.Elem())
		case reflect.Map:
			walk(t.Key())
			walk(t.Elem())
		case reflect.Struct:
			if isGeneratedUnion(t) {
				schemas[t] = unionVariantSchema(t)
				return
			}
			for field := range t.Fields() {
				if field.IsExported() || field.Anonymous {
					walk(field.Type)
				}
			}
		}
	}
	walk(t)
	return schemas
}

func isGeneratedUnion(t reflect.Type) bool {
	return t.NumField() == 1 && t.Field(0).Name == "union" && t.Field(0).Type == rawMessageType
}

// unionVariantSchema reads the variant types off the union's generated As*
// accessors. A union with no readable variant accepts any JSON value.
func unionVariantSchema(t reflect.Type) *jsonschema.Schema {
	var types []string
	for method := range t.Methods() {
		if !strings.HasPrefix(method.Name, "As") || method.Type.NumOut() != 2 {
			continue
		}
		if jsonType := jsonTypeOf(method.Type.Out(0)); jsonType != "" && !slices.Contains(types, jsonType) {
			types = append(types, jsonType)
		}
	}
	slices.Sort(types)
	switch len(types) {
	case 0:
		return &jsonschema.Schema{}
	case 1:
		return &jsonschema.Schema{Type: types[0]}
	default:
		return &jsonschema.Schema{Types: types}
	}
}

func jsonTypeOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct, reflect.Map:
		return "object"
	case reflect.Pointer:
		return jsonTypeOf(t.Elem())
	default:
		return ""
	}
}
