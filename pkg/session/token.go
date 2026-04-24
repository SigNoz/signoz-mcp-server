// Package session implements signed, stateless tokens that wrap the
// process-local session IDs used by the public docs path.
//
// The problem this solves: the MCP Streamable HTTP transport gives each
// client a process-local session ID on `initialize`. Our middleware then
// needs to recognize that session on later GET/DELETE requests so the
// public docs path bypasses tenant authentication. A naive in-memory map
// breaks in multi-replica Kubernetes Services — a client can initialize
// on pod A and stream on pod B — and leaks memory because unauthenticated
// initializes accumulate session IDs indefinitely.
//
// Stateless tokens fix both problems: the token carries its own expiry
// and HMAC signature, so any pod can verify it without shared storage,
// and memory usage is zero.
//
// Token wire format:
//
//	v1.<base64url(payload)>.<base64url(mac)>
//
// payload is JSON: {sid, iat, exp, kid}
//   - sid: the underlying mcp-go session ID (opaque string)
//   - iat: unix-seconds issued-at (for audit)
//   - exp: unix-seconds expiry (verified against wall clock)
//   - kid: 8-hex-char key fingerprint (first 4 bytes of SHA-256(key))
//
// mac is HMAC-SHA256(payload_bytes, key[kid]).
//
// Keys are addressed by fingerprint — not index — so that removing a key
// from the ring deterministically revokes every token that was minted
// with it. Index-based addressing would silently remap revoked tokens to
// the wrong key if an operator reordered the list.
package session

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// TokenPrefix is the scheme tag. Bump this (and handle both in Verify)
	// if the payload layout ever changes incompatibly.
	TokenPrefix = "v1."

	// minKeyBytes is the floor on signing-key length. HMAC-SHA256 has no
	// formal minimum, but rejecting anything below 128 bits keeps a typo'd
	// env var from silently becoming a weak signer.
	minKeyBytes = 16

	// DefaultTTL is the session lifetime when SignerConfig.TTL is zero.
	DefaultTTL = time.Hour
)

var (
	// ErrInvalidToken means the token couldn't be parsed at all — bad
	// prefix, bad base64, or bad JSON payload.
	ErrInvalidToken = errors.New("invalid session token")

	// ErrBadSignature means the payload parsed but the MAC didn't verify
	// against any known key — either the token is forged or the signing
	// key has been rotated out.
	ErrBadSignature = errors.New("session token signature invalid")

	// ErrExpired means the MAC verified but exp is in the past.
	ErrExpired = errors.New("session token expired")

	// ErrUnknownKey means the payload's kid doesn't match any key in the
	// current ring — either the key was rotated out or the token was
	// minted against a different deployment.
	ErrUnknownKey = errors.New("session token key not recognized")

	// ErrNoKey means the signer was constructed without any keys.
	ErrNoKey = errors.New("no session signing key configured")
)

// Signer mints and verifies signed session tokens.
//
// Zero-value Signer is not usable; callers must construct via NewSigner.
// Once built, a Signer is safe for concurrent use.
type Signer struct {
	// activeKey is the key used for NEW tokens. Its fingerprint goes in
	// the kid field.
	activeKey []byte
	activeKID string
	// byKID indexes every ACCEPTED key (including activeKey) so Verify
	// can rehash payload bytes with whichever key signed the token.
	byKID map[string][]byte
	ttl   time.Duration
	now   func() time.Time
}

// SignerConfig configures NewSigner.
type SignerConfig struct {
	// Keys is an ordered list of HMAC keys. Keys[0] is the active signer;
	// every entry (including Keys[0]) is accepted for verification. Each
	// key must be at least 16 bytes.
	//
	// Rotation is a two-step: prepend the new key, redeploy; then on a
	// later deploy drop the retired key from the tail.
	Keys [][]byte

	// TTL is how long a newly minted token is valid. Zero defers to
	// DefaultTTL (1 hour).
	TTL time.Duration

	// Now overrides time.Now for tests; nil uses time.Now.
	Now func() time.Time
}

// NewSigner builds a Signer. Returns ErrNoKey if no keys are supplied,
// or an error wrapping the offending index if any key is too short.
//
// Duplicate keys (identical bytes) are tolerated silently: the first
// occurrence wins. This keeps operators from accidentally breaking
// the signer by listing the same key twice during rotation.
//
// Distinct keys that happen to collide under the 32-bit kid
// fingerprint (~2^-32 per pair — cryptographically unlikely but not
// impossible) are rejected with an error. Silently dropping one would
// violate the "all entries accepted for verify" contract and revoke
// unrelated tokens.
func NewSigner(cfg SignerConfig) (*Signer, error) {
	if len(cfg.Keys) == 0 {
		return nil, ErrNoKey
	}
	byKID := make(map[string][]byte, len(cfg.Keys))
	var activeKey []byte
	var activeKID string
	for i, k := range cfg.Keys {
		if len(k) < minKeyBytes {
			return nil, fmt.Errorf("session signing key[%d] must be at least %d bytes (got %d)", i, minKeyBytes, len(k))
		}
		kid := keyFingerprint(k)
		if existing, ok := byKID[kid]; ok {
			if !bytes.Equal(existing, k) {
				// Distinct keys with the same kid. Astronomically
				// unlikely for the ring sizes operators work with,
				// but we refuse rather than silently dropping one —
				// the operator needs to know their ring is malformed.
				return nil, fmt.Errorf("session signing key[%d]: kid %s already taken by a distinct key", i, kid)
			}
			continue
		}
		// Defensive copy so mutations by the caller can't turn a signed
		// token into a MAC mismatch later.
		key := append([]byte(nil), k...)
		byKID[kid] = key
		if i == 0 {
			activeKey = key
			activeKID = kid
		}
	}
	if activeKey == nil {
		// Defensive: cfg.Keys[0] should always populate the active
		// slot unless the loop above took the "existing duplicate"
		// branch on i==0 (impossible — byKID is empty on the first
		// iteration). Handle it anyway rather than risk minting with
		// a zero key.
		return nil, ErrNoKey
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Signer{
		activeKey: activeKey,
		activeKID: activeKID,
		byKID:     byKID,
		ttl:       ttl,
		now:       now,
	}, nil
}

// payload is the JSON body of a session token. Field names are short on
// purpose — every byte is URL-safe-base64'd into the Mcp-Session-Id
// header, which flows through proxies that may have header-size limits.
type payload struct {
	SID string `json:"sid"`
	IAT int64  `json:"iat"`
	EXP int64  `json:"exp"`
	KID string `json:"kid"`
}

// Sign wraps sessionID in a signed token valid for s.TTL.
func (s *Signer) Sign(sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("session id must not be empty")
	}
	now := s.now()
	p := payload{
		SID: sessionID,
		IAT: now.Unix(),
		EXP: now.Add(s.ttl).Unix(),
		KID: s.activeKID,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal session payload: %w", err)
	}
	mac := hmacSHA256(s.activeKey, raw)
	var b strings.Builder
	b.Grow(len(TokenPrefix) + base64.RawURLEncoding.EncodedLen(len(raw)) + 1 + base64.RawURLEncoding.EncodedLen(len(mac)))
	b.WriteString(TokenPrefix)
	b.WriteString(base64.RawURLEncoding.EncodeToString(raw))
	b.WriteByte('.')
	b.WriteString(base64.RawURLEncoding.EncodeToString(mac))
	return b.String(), nil
}

// Verify parses token, checks its MAC against the key identified by kid,
// and confirms it has not expired. On success, returns the underlying
// session ID.
//
// Verify returns a sentinel error so callers can distinguish "tampered"
// from "expired" in logs without re-parsing the token.
func (s *Signer) Verify(token string) (string, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return "", ErrInvalidToken
	}
	rest := token[len(TokenPrefix):]
	dot := strings.IndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return "", ErrInvalidToken
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(rest[:dot])
	if err != nil {
		return "", ErrInvalidToken
	}
	mac, err := base64.RawURLEncoding.DecodeString(rest[dot+1:])
	if err != nil {
		return "", ErrInvalidToken
	}
	var p payload
	if err := json.Unmarshal(rawPayload, &p); err != nil {
		return "", ErrInvalidToken
	}
	if p.SID == "" || p.KID == "" || p.EXP == 0 {
		return "", ErrInvalidToken
	}
	key, ok := s.byKID[p.KID]
	if !ok {
		return "", ErrUnknownKey
	}
	expected := hmacSHA256(key, rawPayload)
	if !hmac.Equal(expected, mac) {
		return "", ErrBadSignature
	}
	if s.now().Unix() > p.EXP {
		return "", ErrExpired
	}
	return p.SID, nil
}

// ActiveKID returns the fingerprint of the key currently used to sign
// new tokens. Handy for emitting "signer=<kid>" in startup logs so
// operators can correlate pods on the same key ring.
func (s *Signer) ActiveKID() string {
	return s.activeKID
}

// GenerateKey returns a cryptographically random 32-byte HMAC key.
// Used when SIGNOZ_MCP_PUBLIC_SESSION_KEYS is unset and ephemeral
// public-session signing is allowed.
func GenerateKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	return k, nil
}

// ParseKeysFromEnv parses a comma-separated list of base64-encoded keys
// (as would be provided in SIGNOZ_MCP_PUBLIC_SESSION_KEYS). Both
// standard and URL-safe base64 are accepted, with or without padding,
// so operators don't have to remember which variant Kubernetes secrets
// happened to use.
//
// Empty entries are skipped silently. A malformed entry is returned as
// an error so CI catches it rather than silently degrading the ring.
func ParseKeysFromEnv(raw string) ([][]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out [][]byte
	for i, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, err := decodeFlexibleBase64(item)
		if err != nil {
			return nil, fmt.Errorf("session key[%d]: %w", i, err)
		}
		if len(key) < minKeyBytes {
			return nil, fmt.Errorf("session key[%d]: must decode to at least %d bytes (got %d)", i, minKeyBytes, len(key))
		}
		out = append(out, key)
	}
	return out, nil
}

func decodeFlexibleBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not valid base64")
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// fingerprintForTest is the test-overridable hook used by NewSigner.
// Production code calls the real SHA-256 fingerprint; unit tests can
// swap in a stub to force collisions that would otherwise be
// astronomically rare.
var fingerprintForTest = realKeyFingerprint

func keyFingerprint(key []byte) string {
	return fingerprintForTest(key)
}

func realKeyFingerprint(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}
