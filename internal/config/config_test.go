package config

import (
	"bytes"
	"encoding/base64"
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

func TestValidateConfig_HTTPAllowsEphemeralPublicSessionKeysByDefault(t *testing.T) {
	cfg := &Config{
		TransportMode: "http",
		Port:          "8000",
	}

	require.NoError(t, cfg.ValidateConfig())
}

func TestValidateConfig_HTTPAllowsSharedPublicSessionKeys(t *testing.T) {
	cfg := &Config{
		TransportMode:     "http",
		Port:              "8000",
		PublicSessionKeys: [][]byte{bytes.Repeat([]byte{'k'}, 32)},
	}

	require.NoError(t, cfg.ValidateConfig())
}

func TestLoadConfig_PublicSessionKeys(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'s'}, 32))
	t.Setenv(PublicSessionKeysEnv, key)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Len(t, cfg.PublicSessionKeys, 1)
	require.Equal(t, bytes.Repeat([]byte{'s'}, 32), cfg.PublicSessionKeys[0])
}
