package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type contextKey string

const (
	apiKeyContextKey               contextKey = "api_key"
	authHeaderContextKey           contextKey = "auth_header"
	signozURLContextKey            contextKey = "signoz_url"
	searchContextContextKey        contextKey = "search_context"
	sessionIDContextKey            contextKey = "session_id"
	toolNameContextKey             contextKey = "tool_name"
	clientSourceContextKey         contextKey = "client_source"
	assistantThreadIDContextKey    contextKey = "assistant_thread_id"
	assistantExecutionIDContextKey contextKey = "assistant_execution_id"
)

// ClientSourceUserClient is the default for client_source when the header
// is absent or blank — emitting a concrete value keeps downstream group-bys
// free of null-handling.
const ClientSourceUserClient = "user-client"

// CallerCorrelationHeaderMaxLen bounds advisory caller-correlation header
// values. They flow into every log record, span attribute, and Segment
// payload, so an oversized header multiplies downstream.
const CallerCorrelationHeaderMaxLen = 256

// NormalizeCallerCorrelationValue trims surrounding whitespace and caps the
// result to CallerCorrelationHeaderMaxLen runes (not bytes — multi-byte tail
// characters must not be split).
func NormalizeCallerCorrelationValue(s string) string {
	s = strings.TrimSpace(s)
	// Bytes ≥ runes always, so a passing byte-length check skips the rune
	// allocation for the typical UUID/short-label case.
	if len(s) <= CallerCorrelationHeaderMaxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= CallerCorrelationHeaderMaxLen {
		return s
	}
	return string(runes[:CallerCorrelationHeaderMaxLen])
}

// SetAPIKey stores the API key in the context
func SetAPIKey(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey, apiKey)
}

// GetAPIKey retrieves the API key from the context
func GetAPIKey(ctx context.Context) (string, bool) {
	apiKey, ok := ctx.Value(apiKeyContextKey).(string)
	return apiKey, ok
}

// SetAuthHeader stores the auth header name in the context (e.g. "Authorization" or "SIGNOZ-API-KEY").
func SetAuthHeader(ctx context.Context, header string) context.Context {
	return context.WithValue(ctx, authHeaderContextKey, header)
}

// GetAuthHeader retrieves the auth header name from the context.
func GetAuthHeader(ctx context.Context) (string, bool) {
	header, ok := ctx.Value(authHeaderContextKey).(string)
	return header, ok
}

// SetSigNozURL stores the SigNoz URL in the context
func SetSigNozURL(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, signozURLContextKey, url)
}

// GetSigNozURL retrieves the SigNoz URL from the context
func GetSigNozURL(ctx context.Context) (string, bool) {
	url, ok := ctx.Value(signozURLContextKey).(string)
	return url, ok
}

// SetSearchContext stores the user's search text in the context.
func SetSearchContext(ctx context.Context, text string) context.Context {
	return context.WithValue(ctx, searchContextContextKey, text)
}

// GetSearchContext retrieves the user's search text from the context.
func GetSearchContext(ctx context.Context) (string, bool) {
	text, ok := ctx.Value(searchContextContextKey).(string)
	return text, ok
}

// SetSessionID stores the MCP session ID in the context.
func SetSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey, id)
}

// GetSessionID retrieves the MCP session ID from the context.
func GetSessionID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionIDContextKey).(string)
	return id, ok
}

// SetToolName stores the MCP tool name in the context.
func SetToolName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, toolNameContextKey, name)
}

// GetToolName retrieves the MCP tool name from the context.
func GetToolName(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(toolNameContextKey).(string)
	return name, ok
}

// SetClientSource stores the MCP caller category in the context.
func SetClientSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, clientSourceContextKey, source)
}

// GetClientSource retrieves the MCP caller category from the context.
func GetClientSource(ctx context.Context) (string, bool) {
	source, ok := ctx.Value(clientSourceContextKey).(string)
	return source, ok
}

// SetAssistantThreadID stores the SigNoz AI Assistant thread ID in the context.
func SetAssistantThreadID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, assistantThreadIDContextKey, id)
}

// GetAssistantThreadID retrieves the assistant thread ID from the context.
func GetAssistantThreadID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(assistantThreadIDContextKey).(string)
	return id, ok
}

// SetAssistantExecutionID stores the SigNoz AI Assistant execution ID in the context.
func SetAssistantExecutionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, assistantExecutionIDContextKey, id)
}

// GetAssistantExecutionID retrieves the assistant execution ID from the context.
func GetAssistantExecutionID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(assistantExecutionIDContextKey).(string)
	return id, ok
}

// HashTenantKey returns a SHA-256 hash of apiKey and signozURL, suitable for
// use as a cache/map key without exposing the raw API key in memory.
// A null-byte separator is used to prevent collisions between different
// (apiKey, signozURL) pairs that contain colons.
func HashTenantKey(apiKey, signozURL string) string {
	h := sha256.Sum256([]byte(apiKey + "\x00" + signozURL))
	return hex.EncodeToString(h[:])
}
