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
	havingParamDescription = "Condition on the aggregation results, in the filter syntax, applied after grouping. " +
		"Reference an aggregation by its expression, for example \"count() > 100\" or \"p99(duration_nano) > 500000000\". SigNoz validates it."

	functionsParamDescription = "Post-query transforms applied in order to each time_series result, as an array of {name, args} objects, or a JSON-encoded string of that array. " +
		"SigNoz applies them to time_series results only and returns scalar results unchanged, so set requestType to time_series. " +
		"Names are the Query Builder function names, for example cutOffMin, cutOffMax, clampMin, clampMax, absolute, runningDiff, cumulativeSum, ewma3, median5, timeShift, or fillZero; " +
		"args carries the function's parameters, for example [{\"value\": 10}] for cutOffMin. SigNoz validates names and args."

	// functionsIgnoredOnScalarNote tells the caller why functions had no effect.
	functionsIgnoredOnScalarNote = "note: functions were not applied: SigNoz applies them to time_series results only and returns scalar results unchanged. Set requestType to time_series to use them."

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
// strings or a comma-separated string. Blank names are dropped.
func parseFieldNameList(param string, raw any) ([]string, error) {
	var names []string
	switch v := raw.(type) {
	case nil:
	case string:
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
