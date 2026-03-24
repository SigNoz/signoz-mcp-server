package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// ValidatePKCE validates the PKCE code verifier against the challenge using S256.
// Per RFC 7636 section 4.1, the verifier must be 43-128 characters from [A-Z a-z 0-9 - . _ ~].
func ValidatePKCE(codeVerifier, codeChallenge, method string) bool {
	if method != "S256" || codeVerifier == "" || codeChallenge == "" {
		return false
	}

	// RFC 7636 §4.1: code_verifier must be 43-128 unreserved characters.
	if len(codeVerifier) < 43 || len(codeVerifier) > 128 {
		return false
	}

	sum := sha256.Sum256([]byte(codeVerifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(codeChallenge)) == 1
}
