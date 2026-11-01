package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

type notificationCreateArgs struct {
	SearchContext string          `json:"searchContext,omitempty"`
	Name          string          `json:"name,omitempty"`
	GenerateName  bool            `json:"generateName,omitempty"`
	DisplayName   string          `json:"displayName,omitempty"`
	Config        json.RawMessage `json:"config"`
	Test          bool            `json:"test,omitempty"`
}

type notificationUpdateArgs struct {
	SearchContext string          `json:"searchContext,omitempty"`
	ID            string          `json:"id"`
	Config        json.RawMessage `json:"config"`
	Test          bool            `json:"test,omitempty"`
}

type notificationListArgs struct {
	SearchContext string `json:"searchContext,omitempty"`
	Query         string `json:"query,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Sort          string `json:"sort,omitempty"`
	Order         string `json:"order,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Offset        int    `json:"offset,omitempty"`
}

type notificationIDArgs struct {
	SearchContext string `json:"searchContext,omitempty"`
	ID            string `json:"id"`
}

// notificationScalarArgs lists top-level arguments that also accept string
// forms, matching intOrStringType and boolOrStringType on the other tools.
var notificationScalarArgs = map[string]string{"limit": "int", "offset": "int", "test": "bool"}

// describeArg quotes strings so embedded quote characters stay visible.
func describeArg(value any) string {
	if text, ok := value.(string); ok {
		return strconv.Quote(text)
	}
	return fmt.Sprint(value)
}

func normalizeNotificationScalars(arguments any) (any, error) {
	args, ok := arguments.(map[string]any)
	if !ok {
		return arguments, nil
	}
	normalized := make(map[string]any, len(args))
	for key, value := range args {
		normalized[key] = value
		switch notificationScalarArgs[key] {
		case "int":
			n, present, valid := looseInt(value)
			if !valid {
				return nil, fmt.Errorf("%q must be an integer; got %s", key, describeArg(value))
			}
			if present {
				normalized[key] = n
			}
		case "bool":
			if text, isString := value.(string); isString {
				b, err := strconv.ParseBool(strings.TrimSpace(text))
				if err != nil {
					return nil, fmt.Errorf("%q must be true or false; got %q", key, text)
				}
				normalized[key] = b
			}
		}
	}
	return normalized, nil
}

func decodeNotificationArgs(arguments any, target any) error {
	arguments, err := normalizeNotificationScalars(arguments)
	if err != nil {
		return err
	}
	body, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if field, found := strings.CutPrefix(err.Error(), `json: unknown field "`); found {
			return fmt.Errorf("%q is not a parameter of this tool; remove it", strings.TrimSuffix(field, `"`))
		}
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("arguments must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

var legacyNotificationFieldPrefixes = []string{"slack_", "webhook_", "pagerduty_", "email_", "opsgenie_", "msteams_"}

// rejectLegacyNotificationArgs turns the retired flat channel parameters into a
// migration hint instead of a bare unknown-field error.
func rejectLegacyNotificationArgs(arguments any, update bool) *mcp.CallToolResult {
	args, ok := arguments.(map[string]any)
	if !ok {
		return nil
	}
	fields := make([]string, 0, len(args))
	for field := range args {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		legacy := field == "type" || field == "send_resolved"
		for _, prefix := range legacyNotificationFieldPrefixes {
			legacy = legacy || strings.HasPrefix(field, prefix)
		}
		if legacy {
			return notificationValidationError(fmt.Errorf("%q is a retired flat parameter; put provider settings in config: {kind, spec}, for example {\"config\":{\"kind\":\"slack\",\"spec\":{\"apiUrl\":\"https://hooks.slack.com/services/...\",\"sendResolved\":true}}}. Read the tool description for the known kinds", field))
		}
		if update && (field == "name" || field == "displayName") {
			return notificationValidationError(fmt.Errorf("%q cannot be updated because channel names are immutable; send only id and config", field))
		}
	}
	return nil
}

func notificationSchemaOption(schema map[string]any) mcp.ToolOption {
	return func(tool *mcp.Tool) { tool.InputSchema = schema }
}

// boolOrStringProperties re-types the named top-level properties of an
// already-set input schema as boolean-or-string, matching the string coercion
// normalizeNotificationScalars applies before decoding. Apply it after
// mcp.WithInputSchema in the option list.
func boolOrStringProperties(names ...string) mcp.ToolOption {
	return func(tool *mcp.Tool) {
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			panic(fmt.Errorf("marshal input schema for tool %q: %w", tool.Name, err))
		}
		var schema map[string]any
		if err := json.Unmarshal(b, &schema); err != nil || schema == nil {
			panic(fmt.Errorf("decode input schema for tool %q as object: %w", tool.Name, err))
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			panic(fmt.Errorf("input schema for tool %q has no properties object", tool.Name))
		}
		for _, name := range names {
			property, ok := properties[name].(map[string]any)
			if !ok {
				panic(fmt.Errorf("input schema for tool %q has no property %q", tool.Name, name))
			}
			property["type"] = []any{"boolean", "string"}
		}
		tool.InputSchema = schema
	}
}

func notificationRoot(properties map[string]any, required ...string) map[string]any {
	properties["searchContext"] = map[string]any{
		"type": "string", "description": searchContextParamDescription,
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func notificationListSchema() map[string]any {
	return notificationRoot(map[string]any{
		"query":  map[string]any{"type": "string", "maxLength": types.NotificationChannelMaxQueryRunes, "description": "Case-insensitive display-name search."},
		"kind":   map[string]any{"type": "string", "description": "Return only this provider kind, for example \"slack\", \"email\", \"webhook\", or \"pagerduty\". SigNoz validates the value."},
		"sort":   map[string]any{"type": "string", "enum": []string{"updated_at", "created_at", "name"}, "default": "updated_at"},
		"order":  map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "desc"},
		"limit":  map[string]any{"type": []string{"integer", "string"}, "minimum": 0, "default": types.NotificationChannelDefaultListLimit, "description": fmt.Sprintf("Page size. 0 or omitted uses %d; values above %d are clamped to %d, and pagination.limit reports the size used.", types.NotificationChannelDefaultListLimit, types.NotificationChannelMaxListLimit, types.NotificationChannelMaxListLimit)},
		"offset": map[string]any{"type": []string{"integer", "string"}, "minimum": 0, "default": 0},
	})
}

func notificationIDSchema() map[string]any {
	return notificationRoot(map[string]any{
		"id": map[string]any{"type": "string", "format": "uuid", "description": "Notification channel UUID."},
	}, "id")
}
