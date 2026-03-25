package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestValidatePKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if !ValidatePKCE(verifier, challenge, "S256") {
		t.Fatalf("ValidatePKCE() = false, want true")
	}
}

func TestValidatePKCERejectsPlainMethod(t *testing.T) {
	if ValidatePKCE("verifier", "verifier", "plain") {
		t.Fatalf("ValidatePKCE() = true, want false")
	}
}

func TestValidatePKCERejectsWrongVerifier(t *testing.T) {
	if ValidatePKCE("wrong-verifier-that-is-at-least-43-characters-long-xxxxx", "expected", "S256") {
		t.Fatalf("ValidatePKCE() = true, want false")
	}
}

func TestValidatePKCERejectsTooShortVerifier(t *testing.T) {
	// RFC 7636 §4.1: verifier must be 43-128 characters
	shortVerifier := "too-short"
	sum := sha256.Sum256([]byte(shortVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if ValidatePKCE(shortVerifier, challenge, "S256") {
		t.Fatalf("ValidatePKCE() = true for short verifier, want false")
	}
}

func TestValidatePKCERejectsTooLongVerifier(t *testing.T) {
	// RFC 7636 §4.1: verifier must be 43-128 characters
	longVerifier := string(make([]byte, 129))
	for i := range longVerifier {
		longVerifier = longVerifier[:i] + "a" + longVerifier[i+1:]
	}
	sum := sha256.Sum256([]byte(longVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if ValidatePKCE(longVerifier, challenge, "S256") {
		t.Fatalf("ValidatePKCE() = true for long verifier, want false")
	}
}
