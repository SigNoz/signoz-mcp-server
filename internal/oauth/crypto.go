package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrExpiredToken = errors.New("oauth token expired")
	ErrInvalidToken = errors.New("invalid oauth token")
)

const (
	blobTypeAccessToken byte = iota + 1
	blobTypeClientID
	blobTypeAuthorizationCode
	blobTypeRefreshToken
)

type encryptedTokenPayload struct {
	APIKey    string    `json:"api_key"`
	SignozURL string    `json:"signoz_url"`
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type encryptedClientIDPayload struct {
	RedirectURIs []string  `json:"redirect_uris"`
	ClientName   string    `json:"client_name"`
	CreatedAt    time.Time `json:"created_at"`
}

type encryptedAuthorizationCodePayload struct {
	APIKey              string    `json:"api_key"`
	SignozURL           string    `json:"signoz_url"`
	ClientID            string    `json:"client_id"`
	RedirectURI         string    `json:"redirect_uri"`
	CodeChallenge       string    `json:"code_challenge"`
	CodeChallengeMethod string    `json:"code_challenge_method"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type encryptedRefreshTokenPayload struct {
	APIKey    string    `json:"api_key"`
	SignozURL string    `json:"signoz_url"`
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func EncryptToken(apiKey, signozURL, clientID string, expiresAt time.Time, secret []byte) (string, error) {
	return encryptTypedBlob(blobTypeAccessToken, encryptedTokenPayload{
		APIKey:    apiKey,
		SignozURL: signozURL,
		ClientID:  clientID,
		ExpiresAt: expiresAt.UTC(),
	}, secret)
}

func DecryptToken(token string, secret []byte) (apiKey, signozURL, clientID string, expiresAt time.Time, err error) {
	var payload encryptedTokenPayload
	if err := decryptTypedBlob(token, blobTypeAccessToken, secret, &payload); err != nil {
		return "", "", "", time.Time{}, err
	}

	if time.Now().UTC().After(payload.ExpiresAt) {
		return payload.APIKey, payload.SignozURL, payload.ClientID, payload.ExpiresAt, ErrExpiredToken
	}

	return payload.APIKey, payload.SignozURL, payload.ClientID, payload.ExpiresAt, nil
}

func EncryptClientID(redirectURIs []string, clientName string, createdAt time.Time, secret []byte) (string, error) {
	return encryptTypedBlob(blobTypeClientID, encryptedClientIDPayload{
		RedirectURIs: redirectURIs,
		ClientName:   clientName,
		CreatedAt:    createdAt.UTC(),
	}, secret)
}

func DecryptClientID(clientID string, secret []byte) (redirectURIs []string, clientName string, createdAt time.Time, err error) {
	var payload encryptedClientIDPayload
	if err := decryptTypedBlob(clientID, blobTypeClientID, secret, &payload); err != nil {
		return nil, "", time.Time{}, err
	}
	return payload.RedirectURIs, payload.ClientName, payload.CreatedAt, nil
}

func EncryptAuthorizationCode(apiKey, signozURL, clientID, redirectURI, codeChallenge, codeChallengeMethod string, expiresAt time.Time, secret []byte) (string, error) {
	return encryptTypedBlob(blobTypeAuthorizationCode, encryptedAuthorizationCodePayload{
		APIKey:              apiKey,
		SignozURL:           signozURL,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           expiresAt.UTC(),
	}, secret)
}

func DecryptAuthorizationCode(code string, secret []byte) (apiKey, signozURL, clientID, redirectURI, codeChallenge, codeChallengeMethod string, expiresAt time.Time, err error) {
	var payload encryptedAuthorizationCodePayload
	if err := decryptTypedBlob(code, blobTypeAuthorizationCode, secret, &payload); err != nil {
		return "", "", "", "", "", "", time.Time{}, err
	}
	if time.Now().UTC().After(payload.ExpiresAt) {
		return payload.APIKey, payload.SignozURL, payload.ClientID, payload.RedirectURI, payload.CodeChallenge, payload.CodeChallengeMethod, payload.ExpiresAt, ErrExpiredToken
	}
	return payload.APIKey, payload.SignozURL, payload.ClientID, payload.RedirectURI, payload.CodeChallenge, payload.CodeChallengeMethod, payload.ExpiresAt, nil
}

func EncryptRefreshToken(apiKey, signozURL, clientID string, expiresAt time.Time, secret []byte) (string, error) {
	return encryptTypedBlob(blobTypeRefreshToken, encryptedRefreshTokenPayload{
		APIKey:    apiKey,
		SignozURL: signozURL,
		ClientID:  clientID,
		ExpiresAt: expiresAt.UTC(),
	}, secret)
}

func DecryptRefreshToken(token string, secret []byte) (apiKey, signozURL, clientID string, expiresAt time.Time, err error) {
	var payload encryptedRefreshTokenPayload
	if err := decryptTypedBlob(token, blobTypeRefreshToken, secret, &payload); err != nil {
		return "", "", "", time.Time{}, err
	}
	if time.Now().UTC().After(payload.ExpiresAt) {
		return payload.APIKey, payload.SignozURL, payload.ClientID, payload.ExpiresAt, ErrExpiredToken
	}
	return payload.APIKey, payload.SignozURL, payload.ClientID, payload.ExpiresAt, nil
}

func encryptTypedBlob(blobType byte, payload any, secret []byte) (string, error) {
	plaintextPayload, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal token payload: %w", err)
	}

	plaintext := append([]byte{blobType}, plaintextPayload...)

	block, err := aes.NewCipher(tokenKey(secret))
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM cipher: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	encoded := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decryptTypedBlob(token string, expectedType byte, secret []byte, dest any) error {
	plaintext, err := decryptRawToken(token, secret)
	if err != nil {
		return err
	}
	if len(plaintext) == 0 || plaintext[0] != expectedType {
		return fmt.Errorf("unexpected token type: %w", ErrInvalidToken)
	}
	if err := json.Unmarshal(plaintext[1:], dest); err != nil {
		return fmt.Errorf("unmarshal token payload: %w", ErrInvalidToken)
	}
	return nil
}

func decryptRawToken(token string, secret []byte) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("decode token: %w", ErrInvalidToken)
	}

	block, err := aes.NewCipher(tokenKey(secret))
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM cipher: %w", err)
	}

	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("token payload too short: %w", ErrInvalidToken)
	}

	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", ErrInvalidToken)
	}

	return plaintext, nil
}

func tokenKey(secret []byte) []byte {
	sum := sha256.Sum256(secret)
	return sum[:]
}
