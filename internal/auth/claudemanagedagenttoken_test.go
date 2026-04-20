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

func TestParseClaudeManagedAgentToken_ValidUnpadded(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}`
	token := makeToken(t, payload)

	url, key, err := ParseClaudeManagedAgentToken("Bearer " + token)
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

func TestParseClaudeManagedAgentToken_ValidPadded(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}`
	padded := base64.URLEncoding.EncodeToString([]byte(payload))
	token := "mcp_" + padded

	url, key, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" || key != "sk_xxx" {
		t.Errorf("got (%q, %q), want (https://tenant.signoz.cloud, sk_xxx)", url, key)
	}
}

func TestParseClaudeManagedAgentToken_ExtraHeadersIgnored(t *testing.T) {
	payload := `{"headers":{"Authorization":"Bearer","X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx","X-Extra":"ignored"}}`
	token := makeToken(t, payload)

	url, key, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" || key != "sk_xxx" {
		t.Errorf("got (%q, %q), want (https://tenant.signoz.cloud, sk_xxx)", url, key)
	}
}

func TestParseClaudeManagedAgentToken_TrailingSlashTrimmed(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud/","KEY":"sk_xxx"}}`
	token := makeToken(t, payload)

	url, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" {
		t.Errorf("url = %q, want trailing slash trimmed", url)
	}
}

func TestParseClaudeManagedAgentToken_NotAClaudeManagedAgentToken(t *testing.T) {
	_, _, err := ParseClaudeManagedAgentToken("Bearer eyJhbGciOi.aaa.bbb")
	if err == nil || !strings.Contains(err.Error(), "not a claude managed-agent token") {
		t.Errorf("err = %v, want 'not a claude managed-agent token'", err)
	}
}

func TestParseClaudeManagedAgentToken_BadBase64(t *testing.T) {
	_, _, err := ParseClaudeManagedAgentToken("Bearer mcp_!!!not-base64!!!")
	if err == nil || !strings.Contains(err.Error(), "bad base64") {
		t.Errorf("err = %v, want 'bad base64'", err)
	}
}

func TestParseClaudeManagedAgentToken_BadJSON(t *testing.T) {
	token := "mcp_" + base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	_, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "bad json") {
		t.Errorf("err = %v, want 'bad json'", err)
	}
}

func TestParseClaudeManagedAgentToken_MissingHeadersObject(t *testing.T) {
	token := makeToken(t, `{"something":"else"}`)
	_, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing headers object") {
		t.Errorf("err = %v, want 'missing headers object'", err)
	}
}

func TestParseClaudeManagedAgentToken_MissingURL(t *testing.T) {
	token := makeToken(t, `{"headers":{"KEY":"sk_xxx"}}`)
	_, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "X-SigNoz-URL") {
		t.Errorf("err = %v, want 'X-SigNoz-URL' error", err)
	}
}

func TestParseClaudeManagedAgentToken_UnsupportedSchemeRejected(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"ftp://x.example.com","KEY":"sk_xxx"}}`)
	_, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "X-SigNoz-URL") {
		t.Errorf("err = %v, want 'X-SigNoz-URL' error", err)
	}
}

func TestParseClaudeManagedAgentToken_HTTPSchemeAccepted(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"http://tenant.example.com","KEY":"sk_xxx"}}`)
	url, key, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://tenant.example.com" {
		t.Errorf("url = %q, want http://tenant.example.com", url)
	}
	if key != "sk_xxx" {
		t.Errorf("key = %q, want sk_xxx", key)
	}
}

func TestParseClaudeManagedAgentToken_MissingKey(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud"}}`)
	_, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing KEY") {
		t.Errorf("err = %v, want 'missing KEY'", err)
	}
}

func TestParseClaudeManagedAgentToken_EmptyKey(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"   "}}`)
	_, _, err := ParseClaudeManagedAgentToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing KEY") {
		t.Errorf("err = %v, want 'missing KEY'", err)
	}
}
