// Package auth provides authentication helpers for the MCP server.
package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// MCPTestTokenPrefix is the literal prefix that marks a test token.
const MCPTestTokenPrefix = "mcp_"

// mcpTestTokenPayload is the JSON shape carried inside the base64-encoded body.
type mcpTestTokenPayload struct {
	Headers map[string]string `json:"headers"`
}

// ParseMCPTestToken parses a bearer token of the form `Bearer mcp_<base64url(json)>`
// and returns the SigNoz URL and API key carried inside it.
//
// The caller is responsible for deciding whether the token applies (prefix match
// after stripping `Bearer `). On any decode, parse, or validation failure it
// returns a non-nil error; the returned URL and key are empty in that case.
//
// This format is testing-only: the payload is neither signed nor encrypted.
func ParseMCPTestToken(authHeader string) (signozURL, apiKey string, err error) {
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, MCPTestTokenPrefix) {
		return "", "", errors.New("not an mcp_ token")
	}
	body := strings.TrimPrefix(raw, MCPTestTokenPrefix)

	decoded, err := decodeBase64(body)
	if err != nil {
		return "", "", errors.New("invalid mcp_ token: bad base64")
	}

	var payload mcpTestTokenPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return "", "", errors.New("invalid mcp_ token: bad json")
	}
	if payload.Headers == nil {
		return "", "", errors.New("invalid mcp_ token: missing headers object")
	}

	rawURL := strings.TrimSpace(payload.Headers["X-SigNoz-URL"])
	if rawURL == "" {
		return "", "", errors.New("invalid mcp_ token: missing or invalid X-SigNoz-URL")
	}
	normalized, nerr := util.NormalizeSigNozURL(strings.TrimSuffix(rawURL, "/"))
	if nerr != nil {
		return "", "", errors.New("invalid mcp_ token: missing or invalid X-SigNoz-URL")
	}

	key := strings.TrimSpace(payload.Headers["KEY"])
	if key == "" {
		return "", "", errors.New("invalid mcp_ token: missing KEY")
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
