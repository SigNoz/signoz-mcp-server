package contextutil

import "context"

type contextKey string

const apiKeyContextKey contextKey = "api_key"

// SetAPIKey stores the API key in the context
func SetAPIKey(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey, apiKey)
}

// GetAPIKey retrieves the API key from the context
func GetAPIKey(ctx context.Context) (string, bool) {
	apiKey, ok := ctx.Value(apiKeyContextKey).(string)
	return apiKey, ok
}
