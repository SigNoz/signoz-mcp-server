package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

// Shared descriptions for the result-shaping parameters the query tools expose.
const (
	// functionsIgnoredOnScalarNote tells the caller why functions had no effect.
	functionsIgnoredOnScalarNote = "note: functions were not applied: SigNoz applies them to time_series results only and returns scalar results unchanged. Set requestType to time_series to use them."

	// Notes on builder_ai_query results: what the AI values mean and why a
	// window can hide them, which agents otherwise misreport.
	aiTraceRowsNote     = "note: AI trace rows: filters, orderBy, and the limit use spans inside the time window, while each row's values cover the whole trace. estimated_total_cost is an estimate in USD from SigNoz's pricing rules, null for unpriced models (list them with signoz_get_ai_pricing, view=unpriced_models), and total_tokens already includes cache tokens."
	aiAggregateNote     = "note: AI aggregations count only spans inside the time window. Cost is an estimate in USD from SigNoz's pricing rules (null for unpriced models), and total tokens already include cache tokens."
	aiCostAggregateNote = "note: cost comes from SigNoz's pricing rules, so a null or 0 cost means a model has no price; list those models with signoz_get_ai_pricing (view=unpriced_models)."
	aiNoRowsNote        = "note: no AI traces matched in this time window. Widen timeRange; if the app's spans use other model or tool keys (such as llm.model_name), find them with signoz_get_field_keys on the default queryType, check signoz_list_ai_attribute_mappings for a mapping to gen_ai.*, and if none exists, add one on the Attribute Mapping tab of AI Observability."

	// maxSelectFields bounds how many columns one raw search can request.
	maxSelectFields = 50
)

// functionsProperty advertises the functions parameter: an array of the
// generated Function type, or a JSON-encoded string of that array for clients
// that stringify nested values.
func functionsProperty() mcp.ToolOption {
	return mcp.WithArray("functions",
		stringOrArrayType(),
		mcp.Items(mcp.SchemaFor[querybuilder.Function]()),
		mcp.Description(functionsParamDescription))
}

// decodeJSONArg decodes a nested argument that arrives as JSON (an array or an
// object) or as a JSON-encoded string into dst. present is false when the
// argument is absent or an empty string.
func decodeJSONArg(name string, raw any, dst any) (present bool, err error) {
	switch v := raw.(type) {
	case nil:
		return false, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return false, nil
		}
		if err := json.Unmarshal([]byte(v), dst); err != nil {
			return true, fmt.Errorf(`%s %q is not valid JSON: %v`, validationErrorPrefix, name, err)
		}
		return true, nil
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return true, fmt.Errorf(`%s %q could not be read: %v`, validationErrorPrefix, name, err)
		}
		if err := json.Unmarshal(body, dst); err != nil {
			return true, fmt.Errorf(`%s %q has the wrong shape: %v`, validationErrorPrefix, name, err)
		}
		return true, nil
	}
}

// parseFunctionsArg reads the functions parameter into the generated Function
// type. Only the shape is checked here; SigNoz validates names and args.
func parseFunctionsArg(raw any) ([]querybuilder.Function, error) {
	var functions []querybuilder.Function
	present, err := decodeJSONArg("functions", raw, &functions)
	if err != nil {
		return nil, fmt.Errorf(`%w; pass an array of {name, args} objects, for example [{"name": "cutOffMin", "args": [{"value": 10}]}]`, err)
	}
	if !present {
		return nil, nil
	}
	for i, function := range functions {
		if function.Name == nil || strings.TrimSpace(string(*function.Name)) == "" {
			return nil, fmt.Errorf(`%s "functions" item %d has no name; each item needs a name such as "cutOffMin" and optional args`, validationErrorPrefix, i+1)
		}
	}
	return functions, nil
}

// parseOrderBy splits "<key> <direction>" into its parts. A missing direction
// means desc.
func parseOrderBy(raw string) (key, direction string) {
	text := strings.TrimSpace(raw)
	lower := strings.ToLower(text)
	switch {
	case strings.HasSuffix(lower, " asc"):
		return strings.TrimSpace(text[:len(text)-4]), "asc"
	case strings.HasSuffix(lower, " desc"):
		return strings.TrimSpace(text[:len(text)-5]), "desc"
	default:
		return text, "desc"
	}
}

// parseFieldNameList reads a field-name list parameter given as an array of
// strings, a JSON-encoded string of one, or a comma-separated string. Blank
// names are dropped.
func parseFieldNameList(param string, raw any) ([]string, error) {
	var names []string
	switch v := raw.(type) {
	case nil:
	case string:
		// Some clients stringify arrays; splitting "[\"a\",\"b\"]" on commas
		// would send SigNoz keys with brackets and quotes in their names.
		// Decoding into []any keeps a null item visible to the array checks.
		if trimmed := strings.TrimSpace(v); strings.HasPrefix(trimmed, "[") {
			var items []any
			if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
				return nil, fmt.Errorf(`invalid %q value: not a valid JSON array of field names (%v); pass an array of field names or a comma-separated string, e.g. "http.route, db.statement"`, param, err)
			}
			return parseFieldNameList(param, items)
		}
		names = strings.Split(v, ",")
	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf(`invalid %q item %v: each item must be a field name string, e.g. ["http.route", "db.statement"]`, param, item)
			}
			names = append(names, name)
		}
	case []string:
		names = v
	default:
		return nil, fmt.Errorf(`invalid %q value %v: pass an array of field names or a comma-separated string, e.g. "http.route, db.statement"`, param, raw)
	}
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			cleaned = append(cleaned, name)
		}
	}
	return cleaned, nil
}

// selectFieldCollision rejects a selected field whose name a field of another
// context already holds: SigNoz keys each row by field name alone, so only one
// of them could come back. An unprefixed name defers to the held field.
func selectFieldCollision(item, name, heldContext, context string) error {
	if heldContext == "" || context == "" || heldContext == context {
		return nil
	}
	return fmt.Errorf(`%s invalid "selectFields" item %q: rows already carry the %s field %q, and a row holds one value per field name; select only one of them`, validationErrorPrefix, item, heldContext, name)
}
