package auth

import (
	"encoding/base64"
	"strings"
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

func TestParseMCPTestToken_ValidPadded(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}`
	padded := base64.URLEncoding.EncodeToString([]byte(payload))
	token := "mcp_" + padded

	url, key, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" || key != "sk_xxx" {
		t.Errorf("got (%q, %q), want (https://tenant.signoz.cloud, sk_xxx)", url, key)
	}
}

func TestParseMCPTestToken_ExtraHeadersIgnored(t *testing.T) {
	payload := `{"headers":{"Authorization":"Bearer","X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx","X-Extra":"ignored"}}`
	token := makeToken(t, payload)

	url, key, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" || key != "sk_xxx" {
		t.Errorf("got (%q, %q), want (https://tenant.signoz.cloud, sk_xxx)", url, key)
	}
}

func TestParseMCPTestToken_TrailingSlashTrimmed(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud/","KEY":"sk_xxx"}}`
	token := makeToken(t, payload)

	url, _, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" {
		t.Errorf("url = %q, want trailing slash trimmed", url)
	}
}

func TestParseMCPTestToken_NotAnMCPToken(t *testing.T) {
	_, _, err := ParseMCPTestToken("Bearer eyJhbGciOi.aaa.bbb")
	if err == nil || !strings.Contains(err.Error(), "not an mcp_ token") {
		t.Errorf("err = %v, want 'not an mcp_ token'", err)
	}
}

func TestParseMCPTestToken_BadBase64(t *testing.T) {
	_, _, err := ParseMCPTestToken("Bearer mcp_!!!not-base64!!!")
	if err == nil || !strings.Contains(err.Error(), "bad base64") {
		t.Errorf("err = %v, want 'bad base64'", err)
	}
}

func TestParseMCPTestToken_BadJSON(t *testing.T) {
	token := "mcp_" + base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "bad json") {
		t.Errorf("err = %v, want 'bad json'", err)
	}
}

func TestParseMCPTestToken_MissingHeadersObject(t *testing.T) {
	token := makeToken(t, `{"something":"else"}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing headers object") {
		t.Errorf("err = %v, want 'missing headers object'", err)
	}
}

func TestParseMCPTestToken_MissingURL(t *testing.T) {
	token := makeToken(t, `{"headers":{"KEY":"sk_xxx"}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "X-SigNoz-URL") {
		t.Errorf("err = %v, want 'X-SigNoz-URL' error", err)
	}
}

func TestParseMCPTestToken_NonHTTPSchemeRejected(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"ftp://x.example.com","KEY":"sk_xxx"}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "X-SigNoz-URL") {
		t.Errorf("err = %v, want 'X-SigNoz-URL' error", err)
	}
}

func TestParseMCPTestToken_MissingKey(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud"}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing KEY") {
		t.Errorf("err = %v, want 'missing KEY'", err)
	}
}

func TestParseMCPTestToken_EmptyKey(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"   "}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing KEY") {
		t.Errorf("err = %v, want 'missing KEY'", err)
	}
}
