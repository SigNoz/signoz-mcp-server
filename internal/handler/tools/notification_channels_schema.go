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

const notificationSearchContextDescription = "Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."

type notificationCreateArgs struct {
	SearchContext string                          `json:"searchContext,omitempty"`
	Name          string                          `json:"name,omitempty"`
	GenerateName  bool                            `json:"generateName,omitempty"`
	DisplayName   string                          `json:"displayName,omitempty"`
	Config        types.NotificationChannelConfig `json:"config"`
	Test          bool                            `json:"test,omitempty"`
}

type notificationUpdateArgs struct {
	SearchContext string                          `json:"searchContext,omitempty"`
	ID            string                          `json:"id"`
	Config        types.NotificationChannelConfig `json:"config"`
	Test          bool                            `json:"test,omitempty"`
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
			return notificationValidationError(fmt.Errorf("%q is a retired flat parameter; put provider settings in config: {kind, spec}, for example {\"config\":{\"kind\":\"slack\",\"spec\":{\"apiUrl\":\"https://hooks.slack.com/services/...\",\"sendResolved\":true}}}. The config.spec schema lists each provider's fields", field))
		}
		if update && (field == "name" || field == "displayName") {
			return notificationValidationError(fmt.Errorf("%q cannot be updated because channel names are immutable; send only id and config", field))
		}
	}
	return nil
}

// normalizeNotificationUpdateArguments removes only the empty strings that the
// v0.142.0 response encoder uses for unset UnsetOrNonEmptyString fields. Create
// requests remain strict, while get-merge-update can round-trip an ordinary
// upstream response without turning an unset field into authored input.
func normalizeNotificationUpdateArguments(arguments any) any {
	body, err := json.Marshal(arguments)
	if err != nil {
		return arguments
	}
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return arguments
	}
	config, ok := root["config"].(map[string]any)
	if !ok {
		return root
	}
	kind, _ := config["kind"].(string)
	spec, ok := config["spec"].(map[string]any)
	if !ok {
		return root
	}
	for _, field := range types.NotificationChannelUnsetTemplateFields(kind) {
		if value, present := spec[field]; present && value == "" {
			delete(spec, field)
		}
	}
	return root
}

func notificationSchemaOption(schema map[string]any) mcp.ToolOption {
	return func(tool *mcp.Tool) { tool.InputSchema = schema }
}

func notificationRoot(properties map[string]any, required ...string) map[string]any {
	properties["searchContext"] = map[string]any{
		"type": "string", "description": notificationSearchContextDescription,
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
		"kind":   map[string]any{"type": "string", "enum": types.NotificationChannelKinds(), "description": "Return only this provider kind."},
		"sort":   map[string]any{"type": "string", "enum": []string{"updated_at", "created_at", "name"}, "default": "updated_at"},
		"order":  map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "desc"},
		"limit":  map[string]any{"type": []string{"integer", "string"}, "minimum": 0, "default": types.NotificationChannelDefaultListLimit, "description": fmt.Sprintf("Page size. 0 or omitted uses %d; values above %d are clamped to %d, and pagination.limit reports the size used.", types.NotificationChannelDefaultListLimit, types.NotificationChannelMaxListLimit, types.NotificationChannelMaxListLimit)},
		"offset": map[string]any{"type": []string{"integer", "string"}, "minimum": 0, "default": 0},
	})
}

func notificationCreateSchema() map[string]any {
	schema := notificationRoot(map[string]any{
		"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 63, "pattern": "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", "description": "Immutable DNS-1123 machine identity. Alert routing references use displayName, not name."},
		"generateName": map[string]any{"type": "boolean", "default": false, "description": "Generate the immutable name from displayName."},
		"displayName":  map[string]any{"type": "string", "minLength": 1, "description": "Free-text channel label that alert routing references. Defaults to name when generateName is false. Immutable after creation."},
		"config":       notificationConfigSchema(false),
		"test":         map[string]any{"type": []string{"boolean", "string"}, "default": false, "description": "Send one live test notification after creation. The channel remains created if the test fails."},
	}, "config")
	schema["oneOf"] = []any{
		map[string]any{"required": []string{"name"}, "properties": map[string]any{"generateName": map[string]any{"enum": []bool{false}}}},
		map[string]any{"required": []string{"generateName", "displayName"}, "properties": map[string]any{"generateName": map[string]any{"const": true}}, "not": map[string]any{"required": []string{"name"}}},
	}
	return schema
}

func notificationUpdateSchema() map[string]any {
	return notificationRoot(map[string]any{
		"id":     map[string]any{"type": "string", "format": "uuid", "description": "Notification channel UUID."},
		"config": notificationConfigSchema(true),
		"test":   map[string]any{"type": []string{"boolean", "string"}, "default": false, "description": "Send one live test notification after the update. The update remains committed if the test fails."},
	}, "id", "config")
}

func notificationIDSchema() map[string]any {
	return notificationRoot(map[string]any{
		"id": map[string]any{"type": "string", "format": "uuid", "description": "Notification channel UUID."},
	}, "id")
}

func notificationConfigSchema(allowUpstreamUnset bool) map[string]any {
	optionalString := nonEmptyStringSchema
	if allowUpstreamUnset {
		optionalString = stringSchema
	}
	variants := []any{
		notificationConfigVariant("slack", []string{"apiUrl"}, map[string]any{
			"sendResolved": boolSchema(), "apiUrl": secretStringSchema(), "channel": stringSchema(), "title": optionalString(), "text": optionalString(),
		}),
		notificationConfigVariant("email", []string{"to"}, map[string]any{
			"sendResolved": boolSchema(), "to": stringSchema(), "html": optionalString(), "headers": stringMapSchema(),
		}),
		notificationConfigVariant("webhook", []string{"url"}, map[string]any{
			"sendResolved": boolSchema(), "url": secretStringSchema(), "username": stringSchema(), "password": secretStringSchema(), "bearerToken": secretStringSchema(),
		}),
		notificationConfigVariant("pagerduty", []string{"routingKey"}, map[string]any{
			"sendResolved": boolSchema(), "routingKey": secretStringSchema(), "url": stringSchema(), "source": optionalString(), "client": optionalString(), "clientUrl": optionalString(), "description": optionalString(), "severity": stringSchema(), "component": stringSchema(), "group": stringSchema(), "class": stringSchema(), "details": stringMapSchema(),
		}),
		notificationConfigVariant("opsgenie", []string{"apiKey"}, map[string]any{
			"sendResolved": boolSchema(), "apiKey": secretStringSchema(), "apiUrl": stringSchema(), "message": optionalString(), "description": optionalString(), "source": optionalString(), "details": stringMapSchema(), "priority": stringSchema(),
		}),
		notificationConfigVariant("msteams", []string{"webhookUrl"}, map[string]any{
			"sendResolved": boolSchema(), "webhookUrl": secretStringSchema(), "title": optionalString(), "text": optionalString(),
		}),
		notificationConfigVariant("googlechat", []string{"webhookUrl"}, map[string]any{
			"sendResolved": boolSchema(), "webhookUrl": secretStringSchema(), "title": optionalString(), "text": optionalString(),
		}),
		notificationConfigVariant("jira", []string{"site", "project", "issueType", "email", "apiToken"}, map[string]any{
			"sendResolved": boolSchema(), "site": stringSchema(), "project": stringSchema(), "issueType": stringSchema(), "summary": optionalString(), "description": optionalString(), "priority": stringSchema(), "labels": stringArraySchema(), "resolveTransition": stringSchema(), "reopenTransition": stringSchema(), "reopenDuration": optionalString(), "wontFixResolution": stringSchema(), "customFields": map[string]any{"type": "object"}, "email": stringSchema(), "apiToken": secretStringSchema(),
		}),
		notificationConfigVariant("jsmops", []string{"apiKey"}, map[string]any{
			"sendResolved": boolSchema(), "apiKey": secretStringSchema(), "message": optionalString(), "description": optionalString(), "priority": stringSchema(), "tags": optionalString(),
		}),
		notificationConfigVariant("incidentio", []string{"url", "token"}, map[string]any{
			"sendResolved": boolSchema(), "url": stringSchema(), "token": secretStringSchema(), "title": optionalString(), "description": optionalString(), "metadata": stringMapSchema(),
		}),
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind": map[string]any{"type": "string"},
			"spec": map[string]any{"type": "object"},
		},
		"required": []string{"kind", "spec"}, "oneOf": variants, "additionalProperties": false,
		"description": `Complete provider configuration. Select one branch with kind; spec rejects fields from every other provider. Example: {"kind":"email","spec":{"to":"oncall@example.com"}}`,
	}
}

func notificationConfigVariant(kind string, required []string, specProperties map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind": map[string]any{"type": "string", "const": kind},
			"spec": map[string]any{"type": "object", "properties": specProperties, "required": required, "additionalProperties": false},
		},
		"required":             []string{"kind", "spec"},
		"additionalProperties": false,
	}
}

func stringSchema() map[string]any         { return map[string]any{"type": "string"} }
func nonEmptyStringSchema() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
func secretStringSchema() map[string]any {
	return map[string]any{"type": "string", "format": "password"}
}
func boolSchema() map[string]any { return map[string]any{"type": "boolean"} }
func stringMapSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
}
func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}
