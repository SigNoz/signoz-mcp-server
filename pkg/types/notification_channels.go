package types

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

const (
	NotificationChannelDefaultListLimit = 20
	NotificationChannelMaxListLimit     = 200
	NotificationChannelMaxQueryRunes    = 1024
)

// CreateNotificationChannelInput is the signoz_create_notification_channel
// input schema: the upstream-generated postable channel (regenerated from the
// pinned SigNoz release spec by make gen) plus the MCP-only test and
// searchContext fields. Never hand-maintain a copy of the channel config shape
// here; provider-config teaching lives in the tool descriptions.
type CreateNotificationChannelInput struct {
	apitypes.AlertmanagertypesPostableNotificationChannel
	// Config shadows the generated union field (an opaque json.RawMessage that
	// would render as a closed empty object) so the schema stays an open
	// object; SigNoz is the only validator of its shape.
	Config        map[string]any `json:"config" jsonschema:"Provider configuration: an object with kind and spec. SigNoz validates the kind and every spec field."`
	Test          any            `json:"test,omitempty" jsonschema:"Send one live test notification after creation; accepts true/false or their string forms (default false). The channel remains created if the test fails."`
	SearchContext string         `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

// UpdateNotificationChannelInput is the signoz_update_notification_channel
// input schema: the generated updatable channel (config only; name and
// displayName are immutable) plus the target id and the MCP-only fields.
type UpdateNotificationChannelInput struct {
	ID string `json:"id" jsonschema:"Notification channel UUID. Obtain it from signoz_list_notification_channels."`
	apitypes.AlertmanagertypesUpdatableNotificationChannel
	// Config shadows the generated union field; see CreateNotificationChannelInput.
	Config        map[string]any `json:"config" jsonschema:"Provider configuration: an object with kind and spec, replacing the stored config entirely. SigNoz validates the kind and every spec field."`
	Test          any            `json:"test,omitempty" jsonschema:"Send one live test notification after the update; accepts true/false or their string forms (default false). The update remains committed if the test fails."`
	SearchContext string         `json:"searchContext,omitempty" jsonschema:"Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses."`
}

type ListedNotificationChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

func (n *ListedNotificationChannel) Validate() error {
	if err := ValidateUUID(n.ID); err != nil {
		return err
	}
	if n.Name == "" || n.DisplayName == "" || n.CreatedAt == "" || n.UpdatedAt == "" {
		return fmt.Errorf("notification channel summary is incomplete")
	}
	return nil
}

type NotificationChannelListParams struct {
	Query  string
	Kind   string
	Sort   string
	Order  string
	Limit  int
	Offset int
}

func (p *NotificationChannelListParams) Normalize() error {
	if utf8.RuneCountInString(p.Query) > NotificationChannelMaxQueryRunes {
		return fmt.Errorf("query cannot be longer than %d characters", NotificationChannelMaxQueryRunes)
	}
	p.Kind = strings.TrimSpace(p.Kind)
	switch p.Sort {
	case "", "updated_at", "created_at", "name":
	default:
		return fmt.Errorf("sort %q is invalid; allowed values: updated_at, created_at, name", p.Sort)
	}
	switch p.Order {
	case "", "asc", "desc":
	default:
		return fmt.Errorf("order %q is invalid; allowed values: asc, desc", p.Order)
	}
	if p.Limit < 0 {
		return fmt.Errorf("limit must be a positive integer")
	}
	if p.Offset < 0 {
		return fmt.Errorf("offset must be a non-negative integer")
	}
	if p.Sort == "" {
		p.Sort = "updated_at"
	}
	if p.Order == "" {
		p.Order = "desc"
	}
	if p.Limit == 0 {
		p.Limit = NotificationChannelDefaultListLimit
	}
	if p.Limit > NotificationChannelMaxListLimit {
		p.Limit = NotificationChannelMaxListLimit
	}
	return nil
}

type NotificationChannelList struct {
	Channels []ListedNotificationChannel `json:"channels"`
	Total    int                         `json:"total"`
}

func ValidateUUID(value string) error {
	if _, err := uuid.Parse(value); err != nil {
		return fmt.Errorf("id %q is not a valid UUID", value)
	}
	return nil
}
