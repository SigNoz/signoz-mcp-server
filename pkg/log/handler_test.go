package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewContextHandler(base))
}

func TestContextHandler_SuppressesSearchContextForSecretBearingTools(t *testing.T) {
	for _, toolName := range []string{"signoz_create_notification_channel", "signoz_update_notification_channel"} {
		t.Run(toolName, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newTestLogger(&buf)
			ctx := util.SetSearchContext(context.Background(), "credential-url-canary")
			ctx = util.SetToolName(ctx, toolName)

			logger.InfoContext(ctx, "ping")

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("parse log record: %v", err)
			}
			if _, present := rec["mcp.search_context"]; present || strings.Contains(buf.String(), "credential-url-canary") {
				t.Fatalf("secret-bearing tool log retained search context: %s", buf.String())
			}
			if rec["gen_ai.tool.name"] != toolName {
				t.Fatalf("gen_ai.tool.name = %v, want %s", rec["gen_ai.tool.name"], toolName)
			}
		})
	}
}

func TestRedactedTruncAnyRedactsCredentialKeysAtAnyDepth(t *testing.T) {
	slackURL := "https://hooks.slack.com/services/T1/B2/secret-slack"
	webhookURL := "https://alerts.example.com/hook/secret-webhook"
	teamsURL := "https://teams.example.com/webhook/secret-teams"
	routingKey := "pagerduty-routing-secret"
	payload := map[string]any{
		"name": "shadow_probe",
		"arguments": map[string]any{
			"webhook_password":      "secret-canary",
			"slack_api_url":         slackURL,
			"webhook_url":           webhookURL,
			"msteams_webhook_url":   teamsURL,
			"pagerduty_routing_key": routingKey,
			"searchContext":         "configure notification channels",
			"nested": []any{
				map[string]any{"clientSecret": "nested-canary", "filter": "service.name = 'api'"},
			},
		},
	}

	got := RedactedTruncAny(payload)
	for _, secret := range []string{"secret-canary", "nested-canary", slackURL, webhookURL, teamsURL, routingKey} {
		if strings.Contains(got, secret) {
			t.Fatalf("RedactedTruncAny leaked credential %q: %s", secret, got)
		}
	}
	for _, want := range []string{
		`"name":"shadow_probe"`,
		`"webhook_password":"[REDACTED]"`,
		`"slack_api_url":"[REDACTED]"`,
		`"webhook_url":"[REDACTED]"`,
		`"msteams_webhook_url":"[REDACTED]"`,
		`"pagerduty_routing_key":"[REDACTED]"`,
		`"searchContext":"configure notification channels"`,
		`"clientSecret":"[REDACTED]"`,
		`"filter":"service.name = 'api'"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RedactedTruncAny = %s, want %s", got, want)
		}
	}
}

func TestRedactedTruncAnyRedactsNotificationChannelArguments(t *testing.T) {
	arguments := map[string]any{
		"config": map[string]any{
			"kind": "incidentio",
			"spec": map[string]any{
				"apiUrl":       "https://api.example.com/api-url-canary",
				"url":          "https://hooks.example.com/url-canary",
				"token":        "token-canary",
				"apiToken":     "api-token-canary",
				"password":     "password-canary",
				"headers":      map[string]any{"X-Canary": "header-canary"},
				"customFields": map[string]any{"credential": "custom-field-canary"},
			},
		},
		"searchContext":   "use https://hooks.example.com/search-context-canary with token search-token-canary",
		"rejectedUnknown": "unknown-argument-canary",
	}
	encodedArguments, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		toolName string
		payload  any
	}{
		{
			name:     "nested adapted create request",
			toolName: "signoz_create_notification_channel",
			payload: map[string]any{
				"request": mcpcontract.CallToolRequest{Params: mcpcontract.CallToolParams{
					Name:         "signoz_create_notification_channel",
					Arguments:    arguments,
					RawArguments: encodedArguments,
				}},
			},
		},
		{
			name:     "nested adapted update request",
			toolName: "signoz_update_notification_channel",
			payload: map[string]any{
				"request": mcpcontract.CallToolRequest{Params: mcpcontract.CallToolParams{
					Name:         "signoz_update_notification_channel",
					Arguments:    arguments,
					RawArguments: encodedArguments,
				}},
			},
		},
		{
			name:     "nested SDK request",
			toolName: "signoz_create_notification_channel",
			payload: map[string]any{
				"request": &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
					Name:      "signoz_create_notification_channel",
					Arguments: encodedArguments,
				}},
			},
		},
		{
			name:     "flat create params",
			toolName: "signoz_create_notification_channel",
			payload: &mcp.CallToolParams{
				Name:      "signoz_create_notification_channel",
				Arguments: arguments,
			},
		},
		{
			name:     "flat update params",
			toolName: "signoz_update_notification_channel",
			payload: &mcp.CallToolParams{
				Name:      "signoz_update_notification_channel",
				Arguments: arguments,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactedTruncAny(tt.payload)
			for _, secret := range []string{
				"api-url-canary",
				"url-canary",
				"token-canary",
				"api-token-canary",
				"password-canary",
				"header-canary",
				"custom-field-canary",
				"search-context-canary",
				"search-token-canary",
				"unknown-argument-canary",
			} {
				if strings.Contains(got, secret) {
					t.Fatalf("RedactedTruncAny leaked %q: %s", secret, got)
				}
			}
			if !strings.Contains(strings.ToLower(got), `"name":"`+tt.toolName+`"`) {
				t.Fatalf("RedactedTruncAny = %s, want tool name preserved", got)
			}
		})
	}
}

func TestRedactedTruncAnyPreservesOrdinaryToolURLs(t *testing.T) {
	payload := &mcp.CallToolParams{
		Name: "signoz_create_dashboard",
		Arguments: map[string]any{
			"url":    "https://dashboard.example.com/url-canary",
			"apiUrl": "https://dashboard.example.com/api-url-canary",
			"webUrl": "https://dashboard.example.com/web-url-canary",
		},
	}

	got := RedactedTruncAny(payload)
	for _, want := range []string{"url-canary", "api-url-canary", "web-url-canary"} {
		if !strings.Contains(got, want) {
			t.Fatalf("RedactedTruncAny = %s, want ordinary dashboard URL %q preserved", got, want)
		}
	}
}
