package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

// extractBackendWarningMessages parses non-fatal QB v5 warning messages from a
// success response. It fails open: malformed or unexpected response shapes
// simply produce no notes and never block returning the raw backend payload.
func extractBackendWarningMessages(response json.RawMessage) []string {
	var resp struct {
		Data struct {
			Warning struct {
				Warnings []struct {
					Message string `json:"message"`
				} `json:"warnings"`
			} `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return nil
	}
	var messages []string
	for _, warning := range resp.Data.Warning.Warnings {
		if strings.TrimSpace(warning.Message) != "" {
			messages = append(messages, warning.Message)
		}
	}
	return messages
}

func backendWarningsNote(messages []string) string {
	var b strings.Builder
	b.WriteString("note: SigNoz backend returned non-fatal warnings:")
	for _, message := range messages {
		b.WriteString("\n- ")
		b.WriteString(message)
	}
	return b.String()
}

func warnBackendWarnings(ctx context.Context, logger *slog.Logger, toolName string, messages []string) {
	if len(messages) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"SigNoz query builder returned non-fatal warnings",
		slog.String("tool", toolName),
		slog.Int("warningCount", len(messages)),
	)
}

// warnUnparsedWarningEnvelope detects QB warning-envelope drift: it counts
// data.warning.warnings entries and warns only when entries exist but zero
// messages were extracted (a renamed message field). Empty/absent envelopes
// stay silent. Fails open.
func warnUnparsedWarningEnvelope(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, extractedCount int) {
	if extractedCount > 0 {
		return
	}
	var probe struct {
		Data struct {
			Warning struct {
				Warnings []json.RawMessage `json:"warnings"`
			} `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return
	}
	entries := 0
	for _, raw := range probe.Data.Warning.Warnings {
		switch string(bytes.TrimSpace(raw)) {
		case "", "null", "{}":
			continue // empty/degenerate entry — not drift
		}
		entries++
	}
	if entries == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"qb warning envelope unparsed",
		slog.String("tool", toolName),
	)
}
