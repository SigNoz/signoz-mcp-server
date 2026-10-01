package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_CustomHeaders(t *testing.T) {
	tests := []struct {
		name            string
		envValue        string
		expectedHeaders map[string]string
	}{
		{
			name:            "empty env var produces empty map",
			envValue:        "",
			expectedHeaders: map[string]string{},
		},
		{
			name:     "single header pair",
			envValue: "X-Custom-Auth:my-token",
			expectedHeaders: map[string]string{
				"X-Custom-Auth": "my-token",
			},
		},
		{
			name:     "multiple header pairs",
			envValue: "CF-Access-Client-Id:abc123.access,CF-Access-Client-Secret:secret456",
			expectedHeaders: map[string]string{
				"CF-Access-Client-Id":     "abc123.access",
				"CF-Access-Client-Secret": "secret456",
			},
		},
		{
			name:     "whitespace is trimmed",
			envValue: " Key1 : Value1 , Key2 : Value2 ",
			expectedHeaders: map[string]string{
				"Key1": "Value1",
				"Key2": "Value2",
			},
		},
		{
			name:     "value containing colon is preserved",
			envValue: "Authorization:Bearer my-jwt-token:with:colons",
			expectedHeaders: map[string]string{
				"Authorization": "Bearer my-jwt-token:with:colons",
			},
		},
		{
			name:     "malformed entry without colon is skipped",
			envValue: "ValidKey:ValidValue,MalformedEntry",
			expectedHeaders: map[string]string{
				"ValidKey": "ValidValue",
			},
		},
		{
			name:     "empty header name is skipped",
			envValue: ":some-value,ValidKey:ValidValue",
			expectedHeaders: map[string]string{
				"ValidKey": "ValidValue",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIGNOZ_URL", "http://localhost:8080")
			t.Setenv("SIGNOZ_API_KEY", "test-key")

			if tt.envValue != "" {
				t.Setenv("SIGNOZ_CUSTOM_HEADERS", tt.envValue)
			}

			cfg, err := LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.expectedHeaders, cfg.CustomHeaders)
		})
	}
}

func TestLoadConfig_WebURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"unset", "", ""},
		{"public origin", "https://signoz.example.com/", "https://signoz.example.com"},
		{"localhost browser", "http://LOCALHOST:3301/", "http://localhost:3301"},
		{"default port", "https://SIGNOZ.EXAMPLE.COM:443/", "https://signoz.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(SignozURL, "http://signoz.internal:8080")
			t.Setenv(SignozWebURL, tc.raw)
			cfg, err := LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.WebURL)
		})
	}
}

func TestLoadConfig_RejectsInvalidWebURL(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		wantErr string
	}{
		{"https://signoz.example.com/signoz", "without a path"},
		{"https://signoz.example.com?orgId=1", "without query parameters"},
		{"https://signoz.example.com#dashboard", "without a fragment"},
		{"https://user:password@signoz.example.com", "must not include user info"},
		{"ftp://signoz.example.com", "must be http or https"},
		{"https://", "must include a host"},
		{"http://0.0.0.0:3301", "not allowed"},
		{"http://[::]:3301", "not allowed"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(SignozWebURL, tc.raw)
			_, err := LoadConfig()
			require.ErrorContains(t, err, "SIGNOZ_WEB_URL")
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestValidateConfig_HTTPAllowsCredentialsFromHeaders(t *testing.T) {
	cfg := &Config{
		TransportMode: "http",
		Port:          "8000",
	}

	require.NoError(t, cfg.ValidateConfig())
}

func TestLoadConfig_HTTPHostDefaultsToAllInterfaces(t *testing.T) {
	t.Setenv(MCPHost, "")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Empty(t, cfg.Host)
}

func TestLoadConfig_HTTPHostCanBeConfigured(t *testing.T) {
	t.Setenv(MCPHost, "127.0.0.1")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", cfg.Host)
}

func TestValidateConfig_StdioRequiresConfiguredCredentials(t *testing.T) {
	cfg := &Config{
		TransportMode: "stdio",
	}

	require.ErrorContains(t, cfg.ValidateConfig(), "SIGNOZ_API_KEY is required")
}
