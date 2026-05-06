package util

import "testing"

func TestNormalizeSigNozURLFormInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "adds https for protocol-less cloud host",
			raw:  "tough-gecko.us.signoz.cloud",
			want: "https://tough-gecko.us.signoz.cloud",
		},
		{
			name: "strips path from full URL",
			raw:  "https://tough-gecko.us.signoz.cloud/home",
			want: "https://tough-gecko.us.signoz.cloud",
		},
		{
			name: "adds https and strips path query and fragment",
			raw:  "tough-gecko.us.signoz.cloud/home?orgId=123#logs",
			want: "https://tough-gecko.us.signoz.cloud",
		},
		{
			name: "adds https when stripped query contains absolute URL",
			raw:  "tough-gecko.us.signoz.cloud/home?next=https://example.com",
			want: "https://tough-gecko.us.signoz.cloud",
		},
		{
			name: "preserves explicit http and custom port",
			raw:  "http://tenant.example.com:8080/dashboard",
			want: "http://tenant.example.com:8080",
		},
		{
			name: "strips default https port after path removal",
			raw:  "https://tenant.example.com:443/home",
			want: "https://tenant.example.com",
		},
		{
			name: "accepts protocol-relative URL",
			raw:  "//tenant.example.com/home",
			want: "https://tenant.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSigNozURLFormInput(tt.raw)
			if err != nil {
				t.Fatalf("NormalizeSigNozURLFormInput(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeSigNozURLFormInput(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNormalizeSigNozURLFormInputRejectsInvalidHosts(t *testing.T) {
	tests := []string{
		"",
		"ftp://tenant.example.com/home",
		"http:/tenant.example.com/home",
		"https:/tenant.example.com/home",
		"http:tenant.example.com/home",
		"https:tenant.example.com/home",
		"https://tough-gecko.us.signoz.cloud@evil.example/home",
		"tough-gecko.us.signoz.cloud@evil.example/home",
		"localhost/home",
		"0.0.0.0/home",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := NormalizeSigNozURLFormInput(raw); err == nil {
				t.Fatalf("NormalizeSigNozURLFormInput(%q) = %q, want error", raw, got)
			}
		})
	}
}

func TestNormalizeSigNozURLRejectsUserinfo(t *testing.T) {
	tests := []string{
		"https://tenant.example.com@evil.example",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := NormalizeSigNozURL(raw); err == nil {
				t.Fatalf("NormalizeSigNozURL(%q) = %q, want error", raw, got)
			}
		})
	}
}
