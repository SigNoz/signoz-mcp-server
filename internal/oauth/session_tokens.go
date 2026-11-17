package oauth

import (
	"encoding/base64"
	"strings"
	"time"
)

// Session is a SigNoz UI session obtained by password or SSO sign-in on one
// instance.
type Session struct {
	SignozURL       string    `json:"signoz_url"`
	AccessToken     string    `json:"access_token"`
	RefreshToken    string    `json:"refresh_token"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
}

type sessionAuthorizationCodePayload struct {
	Session             Session   `json:"session"`
	ClientID            string    `json:"client_id"`
	RedirectURI         string    `json:"redirect_uri"`
	CodeChallenge       string    `json:"code_challenge"`
	CodeChallengeMethod string    `json:"code_challenge_method"`
	ExpiresAt           time.Time `json:"expires_at"`
}

// SessionAuthorizationCode is the decrypted content of a session-backed code.
type SessionAuthorizationCode = sessionAuthorizationCodePayload

type sessionRefreshTokenPayload struct {
	Session   Session   `json:"session"`
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PendingAuthorization is an authorization request parked while the browser
// signs in through the instance's SSO provider.
type PendingAuthorization struct {
	Nonce               string    `json:"nonce"`
	ClientID            string    `json:"client_id"`
	RedirectURI         string    `json:"redirect_uri"`
	State               string    `json:"state"`
	CodeChallenge       string    `json:"code_challenge"`
	CodeChallengeMethod string    `json:"code_challenge_method"`
	Scope               string    `json:"scope"`
	SignozURL           string    `json:"signoz_url"`
	ExpiresAt           time.Time `json:"expires_at"`
}

func EncryptSessionAuthorizationCode(code SessionAuthorizationCode, secret []byte) (string, error) {
	code.ExpiresAt = code.ExpiresAt.UTC()
	return encryptTypedBlob(blobTypeSessionAuthorizationCode, code, secret)
}

func DecryptSessionAuthorizationCode(value string, secret []byte) (SessionAuthorizationCode, error) {
	var payload SessionAuthorizationCode
	if err := decryptTypedBlob(value, blobTypeSessionAuthorizationCode, secret, &payload); err != nil {
		return SessionAuthorizationCode{}, err
	}
	if time.Now().UTC().After(payload.ExpiresAt) {
		return payload, ErrExpiredToken
	}
	return payload, nil
}

// EncryptSessionRefreshToken packs both SigNoz tokens because SigNoz rotation
// needs the access token as well as the refresh token, and an OAuth refresh
// grant carries only the refresh token.
func EncryptSessionRefreshToken(session Session, clientID string, expiresAt time.Time, secret []byte) (string, error) {
	return encryptTypedBlob(blobTypeSessionRefreshToken, sessionRefreshTokenPayload{
		Session:   session,
		ClientID:  clientID,
		ExpiresAt: expiresAt.UTC(),
	}, secret)
}

func DecryptSessionRefreshToken(value string, secret []byte) (session Session, clientID string, err error) {
	var payload sessionRefreshTokenPayload
	if err := decryptTypedBlob(value, blobTypeSessionRefreshToken, secret, &payload); err != nil {
		return Session{}, "", err
	}
	if time.Now().UTC().After(payload.ExpiresAt) {
		return payload.Session, payload.ClientID, ErrExpiredToken
	}
	return payload.Session, payload.ClientID, nil
}

func EncryptPendingAuthorization(pending PendingAuthorization, secret []byte) (string, error) {
	pending.ExpiresAt = pending.ExpiresAt.UTC()
	return encryptTypedBlob(blobTypePendingAuthorization, pending, secret)
}

func DecryptPendingAuthorization(value string, secret []byte) (PendingAuthorization, error) {
	var payload PendingAuthorization
	if err := decryptTypedBlob(value, blobTypePendingAuthorization, secret, &payload); err != nil {
		return PendingAuthorization{}, err
	}
	if time.Now().UTC().After(payload.ExpiresAt) {
		return payload, ErrExpiredToken
	}
	return payload, nil
}

// sessionAccessTokenPrefix marks an access token that wraps a SigNoz session
// token. Legacy encrypted tokens are unpadded base64url and never contain '.'.
const sessionAccessTokenPrefix = "sz1"

// FormatSessionAccessToken returns the MCP access token for a SigNoz session:
// "sz1.<base64url(instance URL)>.<SigNoz access token>". The instance is
// readable so /mcp can route without decryption; it is not trusted and is
// re-checked against the allowlist on every request.
func FormatSessionAccessToken(signozURL, accessToken string) string {
	return sessionAccessTokenPrefix + "." +
		base64.RawURLEncoding.EncodeToString([]byte(signozURL)) + "." + accessToken
}

// ParseSessionAccessToken splits a session access token. ok is false for any
// other token shape, including JWTs and legacy encrypted tokens.
func ParseSessionAccessToken(token string) (signozURL, accessToken string, ok bool) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 || parts[0] != sessionAccessTokenPrefix || parts[2] == "" {
		return "", "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(decoded) == 0 {
		return "", "", false
	}
	return string(decoded), parts[2], true
}
