package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/prometheus/common/model"
)

const (
	NotificationChannelDefaultListLimit = 20
	NotificationChannelMaxListLimit     = 200
	NotificationChannelMaxQueryRunes    = 1024
)

var notificationChannelKinds = []string{
	"slack", "email", "webhook", "pagerduty", "opsgenie", "msteams",
	"googlechat", "jira", "jsmops", "incidentio",
}

func NotificationChannelKinds() []string {
	return append([]string(nil), notificationChannelKinds...)
}

func IsValidNotificationChannelKind(kind string) bool {
	for _, candidate := range notificationChannelKinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

type NotificationChannelSpec interface {
	Validate() error
}

type NotificationChannelConfig struct {
	Kind string                  `json:"kind"`
	Spec NotificationChannelSpec `json:"spec"`
}

func (c NotificationChannelConfig) Validate() error {
	if !IsValidNotificationChannelKind(c.Kind) {
		return fmt.Errorf("config.kind %q is invalid; allowed values: %s", c.Kind, strings.Join(notificationChannelKinds, ", "))
	}
	if c.Spec == nil {
		return fmt.Errorf("config.spec is required")
	}
	return c.Spec.Validate()
}

func (c *NotificationChannelConfig) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Kind string          `json:"kind"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := strictJSONDecode(data, &envelope); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if envelope.Kind == "" {
		return fmt.Errorf("config.kind is required")
	}
	if !IsValidNotificationChannelKind(envelope.Kind) {
		return fmt.Errorf("config.kind %q is invalid; allowed values: %s", envelope.Kind, strings.Join(notificationChannelKinds, ", "))
	}
	if len(envelope.Spec) == 0 || string(envelope.Spec) == "null" {
		return fmt.Errorf("config.spec is required")
	}

	var spec NotificationChannelSpec
	switch envelope.Kind {
	case "slack":
		spec = &NotificationChannelSlackSpec{}
	case "email":
		spec = &NotificationChannelEmailSpec{}
	case "webhook":
		spec = &NotificationChannelWebhookSpec{}
	case "pagerduty":
		spec = &NotificationChannelPagerdutySpec{}
	case "opsgenie":
		spec = &NotificationChannelOpsgenieSpec{}
	case "msteams":
		spec = &NotificationChannelMSTeamsSpec{}
	case "googlechat":
		spec = &NotificationChannelGoogleChatSpec{}
	case "jira":
		spec = &NotificationChannelJiraSpec{}
	case "jsmops":
		spec = &NotificationChannelJSMOpsSpec{}
	case "incidentio":
		spec = &NotificationChannelIncidentIOSpec{}
	}
	if err := strictJSONDecode(envelope.Spec, spec); err != nil {
		return fmt.Errorf("config.spec: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	c.Kind = envelope.Kind
	c.Spec = spec
	return nil
}

type NotificationChannelSlackSpec struct {
	SendResolved *bool   `json:"sendResolved,omitempty"`
	APIURL       string  `json:"apiUrl"`
	Channel      string  `json:"channel,omitempty"`
	Title        *string `json:"title,omitempty"`
	Text         *string `json:"text,omitempty"`
}

func (s *NotificationChannelSlackSpec) Validate() error {
	if s.APIURL == "" {
		return fmt.Errorf("config.spec.apiUrl is required for a slack channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{"title": s.Title, "text": s.Text})
}

type NotificationChannelEmailSpec struct {
	SendResolved *bool             `json:"sendResolved,omitempty"`
	To           string            `json:"to"`
	HTML         *string           `json:"html,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
}

func (s *NotificationChannelEmailSpec) Validate() error {
	if s.To == "" {
		return fmt.Errorf("config.spec.to is required for an email channel")
	}
	if err := validateNonEmptyOptionalStrings(map[string]*string{"html": s.HTML}); err != nil {
		return err
	}
	names := make([]string, 0, len(s.Headers))
	for name := range s.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if canonical := textproto.CanonicalMIMEHeaderKey(name); canonical != name {
			return fmt.Errorf("config.spec.headers name %q must be written as %q", name, canonical)
		}
	}
	return nil
}

type NotificationChannelWebhookSpec struct {
	SendResolved *bool  `json:"sendResolved,omitempty"`
	URL          string `json:"url"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	BearerToken  string `json:"bearerToken"`
}

func (s *NotificationChannelWebhookSpec) Validate() error {
	if s.URL == "" {
		return fmt.Errorf("config.spec.url is required for a webhook channel")
	}
	basicAuth := s.Username != "" || s.Password != ""
	if basicAuth && s.BearerToken != "" {
		return fmt.Errorf("config.spec.bearerToken cannot be combined with config.spec.username or config.spec.password")
	}
	if basicAuth && (s.Username == "" || s.Password == "") {
		return fmt.Errorf("config.spec.username and config.spec.password must both be set for basic auth")
	}
	return nil
}

type NotificationChannelPagerdutySpec struct {
	SendResolved *bool             `json:"sendResolved,omitempty"`
	RoutingKey   string            `json:"routingKey"`
	URL          string            `json:"url,omitempty"`
	Source       *string           `json:"source,omitempty"`
	Client       *string           `json:"client,omitempty"`
	ClientURL    *string           `json:"clientUrl,omitempty"`
	Description  *string           `json:"description,omitempty"`
	Severity     string            `json:"severity,omitempty"`
	Component    string            `json:"component,omitempty"`
	Group        string            `json:"group,omitempty"`
	Class        string            `json:"class,omitempty"`
	Details      map[string]string `json:"details,omitempty"`
}

func (s *NotificationChannelPagerdutySpec) Validate() error {
	if s.RoutingKey == "" {
		return fmt.Errorf("config.spec.routingKey is required for a pagerduty channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{
		"source": s.Source, "client": s.Client, "clientUrl": s.ClientURL,
		"description": s.Description,
	})
}

type NotificationChannelOpsgenieSpec struct {
	SendResolved *bool             `json:"sendResolved,omitempty"`
	APIKey       string            `json:"apiKey"`
	APIURL       string            `json:"apiUrl"`
	Message      *string           `json:"message,omitempty"`
	Description  *string           `json:"description,omitempty"`
	Source       *string           `json:"source,omitempty"`
	Details      map[string]string `json:"details,omitempty"`
	Priority     string            `json:"priority,omitempty"`
}

func (s *NotificationChannelOpsgenieSpec) Validate() error {
	if s.APIKey == "" {
		return fmt.Errorf("config.spec.apiKey is required for an opsgenie channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{
		"message": s.Message, "description": s.Description, "source": s.Source,
	})
}

type NotificationChannelMSTeamsSpec struct {
	SendResolved *bool   `json:"sendResolved,omitempty"`
	WebhookURL   string  `json:"webhookUrl"`
	Title        *string `json:"title,omitempty"`
	Text         *string `json:"text,omitempty"`
}

func (s *NotificationChannelMSTeamsSpec) Validate() error {
	if s.WebhookURL == "" {
		return fmt.Errorf("config.spec.webhookUrl is required for an msteams channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{"title": s.Title, "text": s.Text})
}

type NotificationChannelGoogleChatSpec struct {
	SendResolved *bool   `json:"sendResolved,omitempty"`
	WebhookURL   string  `json:"webhookUrl"`
	Title        *string `json:"title,omitempty"`
	Text         *string `json:"text,omitempty"`
}

func (s *NotificationChannelGoogleChatSpec) Validate() error {
	if s.WebhookURL == "" {
		return fmt.Errorf("config.spec.webhookUrl is required for a googlechat channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{"title": s.Title, "text": s.Text})
}

type NotificationChannelJiraSpec struct {
	SendResolved      *bool          `json:"sendResolved,omitempty"`
	Site              string         `json:"site"`
	Project           string         `json:"project"`
	IssueType         string         `json:"issueType"`
	Summary           *string        `json:"summary,omitempty"`
	Description       *string        `json:"description,omitempty"`
	Priority          string         `json:"priority,omitempty"`
	Labels            []string       `json:"labels,omitempty"`
	ResolveTransition string         `json:"resolveTransition,omitempty"`
	ReopenTransition  string         `json:"reopenTransition,omitempty"`
	ReopenDuration    *string        `json:"reopenDuration,omitempty"`
	WontFixResolution string         `json:"wontFixResolution,omitempty"`
	CustomFields      map[string]any `json:"customFields,omitempty"`
	Email             string         `json:"email"`
	APIToken          string         `json:"apiToken"`
}

func (s *NotificationChannelJiraSpec) Validate() error {
	for _, required := range []struct {
		value string
		field string
	}{
		{s.Site, "site"}, {s.Project, "project"}, {s.IssueType, "issueType"},
		{s.Email, "email"}, {s.APIToken, "apiToken"},
	} {
		if required.value == "" {
			return fmt.Errorf("config.spec.%s is required for a jira channel", required.field)
		}
	}
	if err := validateNonEmptyOptionalStrings(map[string]*string{
		"summary": s.Summary, "description": s.Description,
		"reopenDuration": s.ReopenDuration,
	}); err != nil {
		return err
	}
	if s.ReopenDuration != nil {
		if err := ValidateCanonicalPrometheusDuration(*s.ReopenDuration); err != nil {
			return err
		}
	}
	return nil
}

type NotificationChannelJSMOpsSpec struct {
	SendResolved *bool   `json:"sendResolved,omitempty"`
	APIKey       string  `json:"apiKey"`
	Message      *string `json:"message,omitempty"`
	Description  *string `json:"description,omitempty"`
	Priority     string  `json:"priority,omitempty"`
	Tags         *string `json:"tags,omitempty"`
}

func (s *NotificationChannelJSMOpsSpec) Validate() error {
	if s.APIKey == "" {
		return fmt.Errorf("config.spec.apiKey is required for a jsmops channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{
		"message": s.Message, "description": s.Description, "tags": s.Tags,
	})
}

type NotificationChannelIncidentIOSpec struct {
	SendResolved *bool             `json:"sendResolved,omitempty"`
	URL          string            `json:"url"`
	Token        string            `json:"token"`
	Title        *string           `json:"title,omitempty"`
	Description  *string           `json:"description,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func (s *NotificationChannelIncidentIOSpec) Validate() error {
	if s.URL == "" {
		return fmt.Errorf("config.spec.url is required for an incidentio channel")
	}
	if s.Token == "" {
		return fmt.Errorf("config.spec.token is required for an incidentio channel")
	}
	return validateNonEmptyOptionalStrings(map[string]*string{
		"title": s.Title, "description": s.Description,
	})
}

type NotificationChannelCreate struct {
	Name         string                    `json:"name,omitempty"`
	GenerateName bool                      `json:"generateName,omitempty"`
	DisplayName  string                    `json:"displayName,omitempty"`
	Config       NotificationChannelConfig `json:"config"`
}

func (c *NotificationChannelCreate) UnmarshalJSON(data []byte) error {
	type createAlias NotificationChannelCreate
	var value createAlias
	if err := strictJSONDecode(data, &value); err != nil {
		return err
	}
	*c = NotificationChannelCreate(value)
	if !c.GenerateName && c.DisplayName == "" {
		c.DisplayName = c.Name
	}
	return c.Validate()
}

func (c *NotificationChannelCreate) Validate() error {
	if c.GenerateName {
		if c.Name != "" {
			return fmt.Errorf("name must be empty when generateName is true")
		}
		if c.DisplayName == "" {
			return fmt.Errorf("displayName is required when generateName is true")
		}
	} else if c.Name == "" {
		return fmt.Errorf("name is required unless generateName is true")
	} else if err := ValidateDNS1123Label(c.Name); err != nil {
		return err
	}
	if c.DisplayName == "" {
		return fmt.Errorf("displayName is required")
	}
	if c.Name == "default-receiver" || c.DisplayName == "default-receiver" {
		return fmt.Errorf("default-receiver is reserved")
	}
	return c.Config.Validate()
}

type NotificationChannelUpdate struct {
	Config NotificationChannelConfig `json:"config"`
}

func (u *NotificationChannelUpdate) UnmarshalJSON(data []byte) error {
	type updateAlias NotificationChannelUpdate
	var value updateAlias
	if err := strictJSONDecode(data, &value); err != nil {
		return err
	}
	*u = NotificationChannelUpdate(value)
	return u.Validate()
}

func (u *NotificationChannelUpdate) Validate() error {
	return u.Config.Validate()
}

type NotificationChannel struct {
	Name        string                    `json:"name"`
	DisplayName string                    `json:"displayName"`
	Config      NotificationChannelConfig `json:"config"`
	ID          string                    `json:"id"`
	CreatedAt   string                    `json:"createdAt"`
	UpdatedAt   string                    `json:"updatedAt"`
}

func (n *NotificationChannel) Validate() error {
	if err := ValidateUUID(n.ID); err != nil {
		return err
	}
	if n.Name == "" || n.DisplayName == "" {
		return fmt.Errorf("name and displayName are required")
	}
	if n.CreatedAt == "" || n.UpdatedAt == "" {
		return fmt.Errorf("createdAt and updatedAt are required")
	}
	return n.Config.Validate()
}

func (n *NotificationChannel) UnmarshalJSON(data []byte) error {
	type channelAlias NotificationChannel
	var value channelAlias
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("must contain exactly one JSON value")
		}
		return err
	}
	*n = NotificationChannel(value)
	return n.Validate()
}

type UnknownJSONFieldError struct {
	Field string
}

func (e *UnknownJSONFieldError) Error() string {
	return "unknown field " + strconv.Quote(e.Field)
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
	if p.Kind != "" && !IsValidNotificationChannelKind(p.Kind) {
		return fmt.Errorf("kind %q is invalid; allowed values: %s", p.Kind, strings.Join(notificationChannelKinds, ", "))
	}
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

func strictJSONDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if field, ok := unknownJSONField(err); ok {
			return &UnknownJSONFieldError{Field: field}
		}
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func unknownJSONField(err error) (string, bool) {
	message := err.Error()
	start := strings.LastIndex(message, "unknown field \"")
	if start < 0 {
		return "", false
	}
	field := message[start+len("unknown field \""):]
	if end := strings.IndexByte(field, '"'); end >= 0 {
		field = field[:end]
	}
	if field == "" {
		return "", false
	}
	return field, true
}

func validateNonEmptyOptionalStrings(fields map[string]*string) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if value := fields[name]; value != nil && strings.TrimSpace(*value) == "" {
			return fmt.Errorf("config.spec.%s cannot be empty when present", name)
		}
	}
	return nil
}

var dns1123LabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func ValidateDNS1123Label(value string) error {
	if len(value) > 63 || !dns1123LabelPattern.MatchString(value) {
		return fmt.Errorf("name %q must be a lowercase DNS-1123 label of at most 63 characters", value)
	}
	return nil
}

func ValidateUUID(value string) error {
	if _, err := uuid.Parse(value); err != nil {
		return fmt.Errorf("id %q is not a valid UUID", value)
	}
	return nil
}

func ValidateCanonicalPrometheusDuration(value string) error {
	duration, err := model.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("config.spec.reopenDuration %q is not a valid duration", value)
	}
	if canonical := duration.String(); canonical != value {
		return fmt.Errorf("config.spec.reopenDuration %q must be written as %q", value, canonical)
	}
	return nil
}
