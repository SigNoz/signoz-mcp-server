// Package auth provides authentication helpers for the MCP server.
package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// ClaudeManagedAgentTokenPrefix is the literal wire prefix that marks a
// Claude managed-agent token. The `mcp_` string is preserved on the wire
// for backwards compatibility with existing agent builds.
const ClaudeManagedAgentTokenPrefix = "mcp_"

// claudeManagedAgentTokenPayload is the JSON shape carried inside the
// base64-encoded body of a Claude managed-agent token.
type claudeManagedAgentTokenPayload struct {
	Headers map[string]string `json:"headers"`
}

// ParseClaudeManagedAgentToken parses a bearer token of the form
// `Bearer mcp_<base64url(json)>` and returns the SigNoz URL and API key
// carried inside it. The token is minted by Claude managed agents and
// bundles URL + key so agent clients don't need to send both headers.
//
// The caller is responsible for deciding whether the token applies (prefix match
// after stripping `Bearer `). On any decode, parse, or validation failure it
// returns a non-nil error; the returned URL and key are empty in that case.
func ParseClaudeManagedAgentToken(authHeader string) (signozURL, apiKey string, err error) {
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if !strings.HasPrefix(raw, ClaudeManagedAgentTokenPrefix) {
		return "", "", errors.New("not a claude managed-agent token")
	}
	body := strings.TrimPrefix(raw, ClaudeManagedAgentTokenPrefix)

	decoded, err := decodeBase64(body)
	if err != nil {
		return "", "", errors.New("invalid claude managed-agent token: bad base64")
	}

	var payload claudeManagedAgentTokenPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return "", "", errors.New("invalid claude managed-agent token: bad json")
	}
	if payload.Headers == nil {
		return "", "", errors.New("invalid claude managed-agent token: missing headers object")
	}

	rawURL := strings.TrimSpace(payload.Headers["X-SigNoz-URL"])
	if rawURL == "" {
		return "", "", errors.New("invalid claude managed-agent token: missing or invalid X-SigNoz-URL")
	}
	normalized, nerr := util.NormalizeSigNozURL(rawURL)
	if nerr != nil {
		return "", "", errors.New("invalid claude managed-agent token: missing or invalid X-SigNoz-URL")
	}

	key := strings.TrimSpace(payload.Headers["KEY"])
	if key == "" {
		return "", "", errors.New("invalid claude managed-agent token: missing KEY")
	}

	return normalized, key, nil
}

// decodeBase64 accepts both unpadded (RawURLEncoding) and padded (URLEncoding)
// base64url inputs.
func decodeBase64(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
