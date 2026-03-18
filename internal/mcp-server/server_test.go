package mcp_server

import (
	"strings"
	"testing"
)

func TestNormalizeSigNozURL_RejectsPathQueryFragment(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{
			name:    "URL with path",
			url:     "https://tenant.example.com/dashboard/123",
			wantErr: "without a path",
		},
		{
			name:    "URL with query parameters",
			url:     "https://tenant.example.com?orgId=1",
			wantErr: "without query parameters",
		},
		{
			name:    "URL with path and query",
			url:     "https://tenant.example.com/dashboard/123?orgId=1",
			wantErr: "without a path",
		},
		{
			name:    "URL with fragment",
			url:     "https://tenant.example.com#section",
			wantErr: "without a fragment",
		},
		{
			name: "trailing slash is allowed",
			url:  "https://tenant.example.com/",
		},
		{
			name: "bare origin is allowed",
			url:  "https://tenant.example.com",
		},
		{
			name: "origin with port is allowed",
			url:  "https://tenant.example.com:8080",
		},
		{
			name:    "ftp scheme rejected",
			url:     "ftp://tenant.example.com",
			wantErr: "not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeSigNozURL(tt.url)
			if tt.wantErr == "" {
				// These cases may still fail due to DNS resolution of
				// the fake host, which is fine — we only care that the
				// path/query/fragment check itself does not fire.
				if err != nil {
					for _, keyword := range []string{"without a path", "without query", "without a fragment"} {
						if strings.Contains(err.Error(), keyword) {
							t.Errorf("unexpected rejection: %v", err)
						}
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestNormalizeSigNozURL_CanonicalizesOrigin(t *testing.T) {
	// These tests use 1.1.1.1 (Cloudflare DNS) which resolves to a public IP,
	// so the full validation pipeline succeeds without DNS issues.
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "strips default https port",
			url:  "https://1.1.1.1:443",
			want: "https://1.1.1.1",
		},
		{
			name: "strips default http port",
			url:  "http://1.1.1.1:80",
			want: "http://1.1.1.1",
		},
		{
			name: "keeps non-default port",
			url:  "https://1.1.1.1:8080",
			want: "https://1.1.1.1:8080",
		},
		{
			name: "lowercases scheme",
			url:  "HTTPS://1.1.1.1",
			want: "https://1.1.1.1",
		},
		{
			name: "strips trailing slash",
			url:  "https://1.1.1.1/",
			want: "https://1.1.1.1",
		},
		{
			name: "bare origin unchanged",
			url:  "https://1.1.1.1",
			want: "https://1.1.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSigNozURL(tt.url)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("normalizeSigNozURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
