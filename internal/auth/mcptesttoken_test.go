package auth

import (
	"encoding/base64"
	"testing"
)

func makeToken(t *testing.T, payload string) string {
	t.Helper()
	return "mcp_" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestParseMCPTestToken_ValidUnpadded(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}`
	token := makeToken(t, payload)

	url, key, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" {
		t.Errorf("url = %q, want %q", url, "https://tenant.signoz.cloud")
	}
	if key != "sk_xxx" {
		t.Errorf("key = %q, want %q", key, "sk_xxx")
	}
}
