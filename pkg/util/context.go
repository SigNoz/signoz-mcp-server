package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

type contextKey string

const (
	apiKeyContextKey    contextKey = "api_key"
	signozURLContextKey contextKey = "signoz_url"
)

// SetAPIKey stores the API key in the context
func SetAPIKey(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey, apiKey)
}

// GetAPIKey retrieves the API key from the context
func GetAPIKey(ctx context.Context) (string, bool) {
	apiKey, ok := ctx.Value(apiKeyContextKey).(string)
	return apiKey, ok
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

// HashTenantKey returns a SHA-256 hash of apiKey:signozURL, suitable for use
// as a cache/map key without exposing the raw API key in memory.
func HashTenantKey(apiKey, signozURL string) string {
	h := sha256.Sum256([]byte(apiKey + ":" + signozURL))
	return hex.EncodeToString(h[:])
}