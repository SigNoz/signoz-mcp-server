package config

import (
	"testing"
	"time"
)

func TestGetEnvRefreshInterval(t *testing.T) {
	const key = "TEST_DOCS_REFRESH_INTERVAL"
	def := 6 * time.Hour
	cases := []struct {
		name         string
		value        string
		wantInterval time.Duration
		wantDisabled bool
	}{
		{"unset keeps default", "", def, false},
		{"zero disables", "0", def, true},
		{"zero duration disables", "0s", def, true},
		{"off disables", "off", def, true},
		{"disabled disables", "Disabled", def, true},
		{"false disables", "false", def, true},
		{"positive duration", "90m", 90 * time.Minute, false},
		{"negative falls back", "-1h", def, false},
		{"garbage falls back", "soon", def, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, tc.value)
			got, disabled := getEnvRefreshInterval(key, def)
			if got != tc.wantInterval || disabled != tc.wantDisabled {
				t.Fatalf("getEnvRefreshInterval(%q) = (%s, %v), want (%s, %v)", tc.value, got, disabled, tc.wantInterval, tc.wantDisabled)
			}
		})
	}
}

func TestLoadConfigDocsRefreshDisabled(t *testing.T) {
	t.Setenv("SIGNOZ_URL", "https://example.invalid")
	t.Setenv("SIGNOZ_API_KEY", "test")
	t.Setenv(DocsRefreshIntervalEnv, "0")
	t.Setenv(DocsFullRefreshIntervalEnv, "12h")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DocsRefreshDisabled || cfg.DocsFullRefreshDisabled {
		t.Fatalf("disabled flags = (%v, %v), want (true, false)", cfg.DocsRefreshDisabled, cfg.DocsFullRefreshDisabled)
	}
	if cfg.DocsFullRefreshInterval != 12*time.Hour {
		t.Fatalf("full refresh interval = %s, want 12h; a disabled incremental schedule must not trip the ordering fallback", cfg.DocsFullRefreshInterval)
	}
}
