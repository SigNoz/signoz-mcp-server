package types

import "encoding/json"

// Receiver is the top-level structure sent to SigNoz's /api/v1/channels
// and /api/v1/testChannel endpoints. Only one of the *_configs fields
// should be populated per receiver.
type Receiver struct {
	Name              string              `json:"name"`
	SlackConfigs      []SlackConfig       `json:"slack_configs,omitempty"`
	WebhookConfigs    []WebhookConfig     `json:"webhook_configs,omitempty"`
	PagerdutyConfigs  []PagerdutyConfig   `json:"pagerduty_configs,omitempty"`
	EmailConfigs      []EmailConfig       `json:"email_configs,omitempty"`
	OpsgenieConfigs   []OpsgenieConfig    `json:"opsgenie_configs,omitempty"`
	MSTeamsV2Configs  []MSTeamsV2Config   `json:"msteamsv2_configs,omitempty"`
}

// MarshalReceiver serialises a Receiver to JSON bytes suitable for the
// SigNoz API request body.
func MarshalReceiver(r Receiver) ([]byte, error) {
	return json.Marshal(r)
}

// SlackConfig mirrors the SigNoz/Alertmanager slack_configs entry.
type SlackConfig struct {
	SendResolved bool   `json:"send_resolved"`
	APIURL       string `json:"api_url"`
	Channel      string `json:"channel,omitempty"`
	Title        string `json:"title,omitempty"`
	Text         string `json:"text,omitempty"`
}

// WebhookConfig mirrors the SigNoz/Alertmanager webhook_configs entry.
type WebhookConfig struct {
	SendResolved bool              `json:"send_resolved"`
	URL          string            `json:"url"`
	HTTPConfig   *WebhookHTTPConfig `json:"http_config,omitempty"`
}

// WebhookHTTPConfig holds optional basic auth for webhook channels.
type WebhookHTTPConfig struct {
	BasicAuth *WebhookBasicAuth `json:"basic_auth,omitempty"`
}

// WebhookBasicAuth holds username/password for webhook basic auth.
type WebhookBasicAuth struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// PagerdutyConfig mirrors the SigNoz/Alertmanager pagerduty_configs entry.
type PagerdutyConfig struct {
	SendResolved bool   `json:"send_resolved"`
	RoutingKey   string `json:"routing_key"`
	Description  string `json:"description,omitempty"`
	Severity     string `json:"severity,omitempty"`
}

// EmailConfig mirrors the SigNoz/Alertmanager email_configs entry.
type EmailConfig struct {
	SendResolved bool   `json:"send_resolved"`
	To           string `json:"to"`
	HTML         string `json:"html,omitempty"`
}

// OpsgenieConfig mirrors the SigNoz/Alertmanager opsgenie_configs entry.
type OpsgenieConfig struct {
	SendResolved bool   `json:"send_resolved"`
	APIKey       string `json:"api_key"`
	Message      string `json:"message,omitempty"`
	Description  string `json:"description,omitempty"`
	Priority     string `json:"priority,omitempty"`
}

// MSTeamsV2Config mirrors the SigNoz/Alertmanager msteamsv2_configs entry.
type MSTeamsV2Config struct {
	SendResolved bool   `json:"send_resolved"`
	WebhookURL   string `json:"webhook_url"`
	Title        string `json:"title,omitempty"`
	Text         string `json:"text,omitempty"`
}
