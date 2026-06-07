package util

import "testing"

func TestTenantURLAllowlistUnconfiguredAllowsAll(t *testing.T) {
	al := ParseTenantURLAllowlist("")
	if al.Configured() {
		t.Fatalf("empty allowlist should not be configured")
	}
	for _, host := range []string{"demo.us.signoz.cloud", "1.1.1.1", "signoz.example.com", ""} {
		if !al.AllowsHost(host) {
			t.Errorf("unconfigured allowlist should allow %q", host)
		}
	}
	if !al.AllowsURL("https://anything.example.com") {
		t.Errorf("unconfigured allowlist should allow any URL")
	}
}

func TestTenantURLAllowlistWildcard(t *testing.T) {
	al := ParseTenantURLAllowlist("*.us.signoz.cloud")
	if !al.Configured() {
		t.Fatalf("allowlist should be configured")
	}

	allowed := []string{
		"demo.us.signoz.cloud",
		"DEMO.US.SIGNOZ.CLOUD", // case-insensitive
		"tough-gecko.us.signoz.cloud",
		"a.b.us.signoz.cloud", // wildcard spans multiple labels
	}
	for _, host := range allowed {
		if !al.AllowsHost(host) {
			t.Errorf("expected %q to be allowed", host)
		}
	}

	denied := []string{
		"us.signoz.cloud",               // apex is not matched by its own wildcard
		"evil.eu.signoz.cloud",          // different region
		"notus.signoz.cloud",            // no dot boundary before suffix
		"demo.us.signoz.cloud.evil.com", // suffix not at the end
		"1.1.1.1",
		"",
	}
	for _, host := range denied {
		if al.AllowsHost(host) {
			t.Errorf("expected %q to be denied", host)
		}
	}
}

func TestTenantURLAllowlistExactAndMultiple(t *testing.T) {
	al := ParseTenantURLAllowlist("signoz.example.com, *.eu.signoz.cloud ,https://*.us.signoz.cloud/")

	allowed := []string{
		"signoz.example.com",
		"demo.eu.signoz.cloud",
		"demo.us.signoz.cloud", // scheme/path in the pattern is stripped
	}
	for _, host := range allowed {
		if !al.AllowsHost(host) {
			t.Errorf("expected %q to be allowed", host)
		}
	}

	denied := []string{
		"sub.signoz.example.com", // exact entry does not cover subdomains
		"signoz.example.org",
		"169.254.169.254", // SSRF target blocked once an allowlist is set
	}
	for _, host := range denied {
		if al.AllowsHost(host) {
			t.Errorf("expected %q to be denied", host)
		}
	}
}

func TestTenantURLAllowlistAllowsURL(t *testing.T) {
	al := ParseTenantURLAllowlist("*.us.signoz.cloud")

	if !al.AllowsURL("https://demo.us.signoz.cloud") {
		t.Errorf("expected normalized cloud URL to be allowed")
	}
	if !al.AllowsURL("https://demo.us.signoz.cloud:443") {
		t.Errorf("port should not affect host matching")
	}
	if al.AllowsURL("https://1.1.1.1") {
		t.Errorf("expected non-cloud URL to be denied")
	}
	if al.AllowsURL("://bad-url") {
		t.Errorf("malformed URL should be denied when allowlist is configured")
	}
}
