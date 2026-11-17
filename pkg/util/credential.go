package util

import "context"

// Credential is how a request authenticates to SigNoz. Header is the header
// SigNoz reads it from and Value is sent verbatim, so outbound code never
// branches on which ingress path produced it.
type Credential struct {
	Header string
	Value  string
}

const (
	apiKeyHeader        = "SIGNOZ-API-KEY"
	authorizationHeader = "Authorization"
)

// APIKeyCredential is a service-account API key sent as SIGNOZ-API-KEY.
func APIKeyCredential(apiKey string) Credential {
	return Credential{Header: apiKeyHeader, Value: apiKey}
}

// BearerCredential is a SigNoz session (or other bearer) token sent as
// Authorization: Bearer.
func BearerCredential(token string) Credential {
	return Credential{Header: authorizationHeader, Value: "Bearer " + token}
}

// IsZero reports whether the credential carries no secret.
func (c Credential) IsZero() bool { return c.Value == "" }

// SetCredential stores c on ctx where the SigNoz client factory reads it.
func SetCredential(ctx context.Context, c Credential) context.Context {
	return SetAuthHeader(SetAPIKey(ctx, c.Value), c.Header)
}

// GetCredential returns the credential stored on ctx, defaulting the header
// to SIGNOZ-API-KEY as the client factory does.
func GetCredential(ctx context.Context) Credential {
	value, _ := GetAPIKey(ctx)
	header, _ := GetAuthHeader(ctx)
	if header == "" {
		header = apiKeyHeader
	}
	return Credential{Header: header, Value: value}
}
