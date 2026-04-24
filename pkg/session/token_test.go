package session

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeSigner builds a Signer with a controllable clock. now is a pointer
// so individual tests can bump time forward without rebuilding the
// signer — matching how real-time token expiry works.
func makeSigner(t *testing.T, keys [][]byte, ttl time.Duration, now *time.Time) *Signer {
	t.Helper()
	s, err := NewSigner(SignerConfig{
		Keys: keys,
		TTL:  ttl,
		Now:  func() time.Time { return *now },
	})
	require.NoError(t, err)
	return s
}

func TestSigner_SignVerifyRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := makeSigner(t, [][]byte{bytes16('a')}, time.Hour, &now)

	token, err := s.Sign("session-abc")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, "v1."))

	sid, err := s.Verify(token)
	require.NoError(t, err)
	require.Equal(t, "session-abc", sid)
}

func TestSigner_Verify_ExpiredReturnsSentinel(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := makeSigner(t, [][]byte{bytes16('a')}, time.Hour, &now)

	token, err := s.Sign("sid-1")
	require.NoError(t, err)

	now = now.Add(2 * time.Hour)
	_, err = s.Verify(token)
	require.ErrorIs(t, err, ErrExpired)
}

func TestSigner_Verify_RejectsTamperedPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := makeSigner(t, [][]byte{bytes16('a')}, time.Hour, &now)

	token, err := s.Sign("sid-1")
	require.NoError(t, err)

	// Flip a byte in the payload section. A MAC over the original
	// payload must no longer verify against the tampered one.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	payloadBytes[0] ^= 0xff
	parts[1] = base64.RawURLEncoding.EncodeToString(payloadBytes)
	tampered := strings.Join(parts, ".")

	_, err = s.Verify(tampered)
	// Tampering either corrupts JSON → ErrInvalidToken, changes kid →
	// ErrUnknownKey, or keeps kid intact but breaks the MAC →
	// ErrBadSignature. Any of the three is a correct rejection.
	require.True(t,
		errors.Is(err, ErrBadSignature) ||
			errors.Is(err, ErrInvalidToken) ||
			errors.Is(err, ErrUnknownKey),
		"unexpected verify error: %v", err)
}

func TestSigner_Verify_RejectsTamperedMAC(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := makeSigner(t, [][]byte{bytes16('a')}, time.Hour, &now)

	token, err := s.Sign("sid-1")
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	mac[len(mac)-1] ^= 0x01
	parts[2] = base64.RawURLEncoding.EncodeToString(mac)

	_, err = s.Verify(strings.Join(parts, "."))
	require.ErrorIs(t, err, ErrBadSignature)
}

func TestSigner_Verify_RejectsUnknownPrefix(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := makeSigner(t, [][]byte{bytes16('a')}, time.Hour, &now)

	_, err := s.Verify("v2.abc.def")
	require.ErrorIs(t, err, ErrInvalidToken)

	_, err = s.Verify("plain-uuid")
	require.ErrorIs(t, err, ErrInvalidToken)

	_, err = s.Verify("v1.only-one-part")
	require.ErrorIs(t, err, ErrInvalidToken)

	_, err = s.Verify("v1..")
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestSigner_KeyRotation_NewKeyMintsOldKeyStillVerifies is the headline
// property: a pod running with keys [new, old] must accept tokens
// minted by a peer that still has old at the head. This is what makes
// rolling deploys safe.
func TestSigner_KeyRotation_NewKeyMintsOldKeyStillVerifies(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	oldKey := bytes16('a')
	newKey := bytes16('b')

	// Pod A is still on the old ring — it signs with oldKey.
	podA := makeSigner(t, [][]byte{oldKey}, time.Hour, &now)
	// Pod B has been rolled — it signs with newKey but still accepts oldKey.
	podB := makeSigner(t, [][]byte{newKey, oldKey}, time.Hour, &now)

	token, err := podA.Sign("sid-rolling")
	require.NoError(t, err)

	sid, err := podB.Verify(token)
	require.NoError(t, err)
	require.Equal(t, "sid-rolling", sid)
}

// TestSigner_KeyRotation_RemovedKeyRevokes verifies the reverse: once an
// operator drops a key from the ring entirely, tokens minted against it
// fail with ErrUnknownKey — not silently accepted under a new key.
func TestSigner_KeyRotation_RemovedKeyRevokes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	oldKey := bytes16('a')
	newKey := bytes16('b')

	oldSigner := makeSigner(t, [][]byte{oldKey}, time.Hour, &now)
	tokenFromOld, err := oldSigner.Sign("sid-revoked")
	require.NoError(t, err)

	// Operator has now rotated to ONLY the new key (tail dropped).
	newOnly := makeSigner(t, [][]byte{newKey}, time.Hour, &now)
	_, err = newOnly.Verify(tokenFromOld)
	require.ErrorIs(t, err, ErrUnknownKey)
}

// TestSigner_ActiveKIDChangesOnRotation documents the guarantee that
// the active kid is the fingerprint of Keys[0], not whatever got
// alphabetically sorted. A rolling deploy needs to know which pods have
// switched — this is what gets logged at boot.
func TestSigner_ActiveKIDChangesOnRotation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	keyA := bytes16('a')
	keyB := bytes16('b')

	before := makeSigner(t, [][]byte{keyA}, time.Hour, &now)
	after := makeSigner(t, [][]byte{keyB, keyA}, time.Hour, &now)

	require.NotEqual(t, before.ActiveKID(), after.ActiveKID())
}

func TestSigner_DuplicateKeysTolerated(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	k := bytes16('a')

	// Operator double-pasted the same key during rotation. Should not
	// error; the first occurrence is the active signer.
	s, err := NewSigner(SignerConfig{
		Keys: [][]byte{k, k},
		TTL:  time.Hour,
		Now:  func() time.Time { return now },
	})
	require.NoError(t, err)

	token, err := s.Sign("sid-dup")
	require.NoError(t, err)
	sid, err := s.Verify(token)
	require.NoError(t, err)
	require.Equal(t, "sid-dup", sid)
}

// TestSigner_KIDCollisionWithDistinctKeyIsError proves we don't
// silently drop a distinct key whose kid happens to collide with an
// earlier one. Since natural 32-bit collisions are astronomically
// unlikely we synthesize one by installing a stub keyFingerprint for
// the duration of the test — kept local via a private hook.
//
// Rationale: if we silently dropped the second key, the "all entries
// accepted for verify" contract would be violated and tokens minted
// against the second key on a peer pod would 401 with ErrUnknownKey.
func TestSigner_KIDCollisionWithDistinctKeyIsError(t *testing.T) {
	orig := fingerprintForTest
	t.Cleanup(func() { fingerprintForTest = orig })
	// Force every key to the same kid so the collision check fires.
	fingerprintForTest = func(_ []byte) string { return "deadbeef" }

	_, err := NewSigner(SignerConfig{Keys: [][]byte{bytes16('a'), bytes16('b')}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "kid")
}

func TestSigner_RejectsShortKey(t *testing.T) {
	_, err := NewSigner(SignerConfig{Keys: [][]byte{[]byte("too-short")}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 16 bytes")
}

func TestSigner_RejectsNoKeys(t *testing.T) {
	_, err := NewSigner(SignerConfig{})
	require.ErrorIs(t, err, ErrNoKey)
}

func TestSigner_Sign_RejectsEmptySessionID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := makeSigner(t, [][]byte{bytes16('a')}, time.Hour, &now)
	_, err := s.Sign("")
	require.Error(t, err)
}

// TestSigner_CrossDeploymentTokensRejected simulates a malicious or
// accidental token minted against a completely different deployment's
// key ring. We must reject with ErrUnknownKey — never accept.
func TestSigner_CrossDeploymentTokensRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	attacker := makeSigner(t, [][]byte{bytes16('x')}, time.Hour, &now)
	forged, err := attacker.Sign("attacker-sid")
	require.NoError(t, err)

	victim := makeSigner(t, [][]byte{bytes16('v')}, time.Hour, &now)
	_, err = victim.Verify(forged)
	require.ErrorIs(t, err, ErrUnknownKey)
}

func TestParseKeysFromEnv_StdBase64(t *testing.T) {
	raw := bytes16('a')
	encoded := base64.StdEncoding.EncodeToString(raw)
	keys, err := ParseKeysFromEnv(encoded)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, raw, keys[0])
}

func TestParseKeysFromEnv_URLBase64Unpadded(t *testing.T) {
	raw := bytes16('a')
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	keys, err := ParseKeysFromEnv(encoded)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, raw, keys[0])
}

func TestParseKeysFromEnv_MultipleKeys(t *testing.T) {
	a, b := bytes16('a'), bytes16('b')
	raw := base64.StdEncoding.EncodeToString(a) + "," + base64.StdEncoding.EncodeToString(b)
	keys, err := ParseKeysFromEnv(raw)
	require.NoError(t, err)
	require.Len(t, keys, 2)
}

func TestParseKeysFromEnv_EmptyEntriesSkipped(t *testing.T) {
	a := bytes16('a')
	raw := "," + base64.StdEncoding.EncodeToString(a) + ", ,"
	keys, err := ParseKeysFromEnv(raw)
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

func TestParseKeysFromEnv_EmptyInput(t *testing.T) {
	keys, err := ParseKeysFromEnv("")
	require.NoError(t, err)
	require.Nil(t, keys)

	keys, err = ParseKeysFromEnv("   ")
	require.NoError(t, err)
	require.Nil(t, keys)
}

func TestParseKeysFromEnv_InvalidBase64(t *testing.T) {
	_, err := ParseKeysFromEnv("not_base_64_$$$")
	require.Error(t, err)
}

func TestParseKeysFromEnv_ShortKeyRejected(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("short"))
	_, err := ParseKeysFromEnv(encoded)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 16 bytes")
}

func TestGenerateKey(t *testing.T) {
	a, err := GenerateKey()
	require.NoError(t, err)
	require.Len(t, a, 32)

	b, err := GenerateKey()
	require.NoError(t, err)
	require.NotEqual(t, a, b, "two generated keys must differ")
}

// TestSigner_DefaultTTL documents the 1-hour fallback when SignerConfig
// omits TTL.
func TestSigner_DefaultTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s, err := NewSigner(SignerConfig{
		Keys: [][]byte{bytes16('a')},
		Now:  func() time.Time { return now },
	})
	require.NoError(t, err)

	token, err := s.Sign("sid")
	require.NoError(t, err)

	// 59 minutes: still valid.
	now = now.Add(59 * time.Minute)
	_, err = s.Verify(token)
	require.NoError(t, err)

	// 61 minutes past IAT: expired.
	now = now.Add(2 * time.Minute)
	_, err = s.Verify(token)
	require.ErrorIs(t, err, ErrExpired)
}

func bytes16(b byte) []byte {
	out := make([]byte, 16)
	for i := range out {
		out[i] = b
	}
	return out
}
