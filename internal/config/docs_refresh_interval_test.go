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

func TestLoadConfigDocsRefreshOrderingFallback(t *testing.T) {
	tests := []struct {
		name                string
		refresh, full       string
		wantRefreshDisabled bool
		wantRefresh         time.Duration // zero: not asserted
		wantFull            time.Duration
	}{
		{
			name:        "full shorter than incremental falls back to the defaults",
			refresh:     "10m",
			full:        "5m",
			wantRefresh: defaultDocsRefreshInterval,
			wantFull:    defaultDocsFullRefreshPeriod,
		},
		{
			name:                "a disabled incremental schedule does not trip the fallback",
			refresh:             "0",
			full:                "12h",
			wantRefreshDisabled: true,
			wantFull:            12 * time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIGNOZ_URL", "https://example.invalid")
			t.Setenv("SIGNOZ_API_KEY", "test")
			t.Setenv(DocsRefreshIntervalEnv, tt.refresh)
			t.Setenv(DocsFullRefreshIntervalEnv, tt.full)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DocsRefreshDisabled != tt.wantRefreshDisabled || cfg.DocsFullRefreshDisabled {
				t.Fatalf("disabled flags = (%v, %v), want (%v, false)", cfg.DocsRefreshDisabled, cfg.DocsFullRefreshDisabled, tt.wantRefreshDisabled)
			}
			if tt.wantRefresh != 0 && cfg.DocsRefreshInterval != tt.wantRefresh {
				t.Fatalf("refresh interval = %s, want %s", cfg.DocsRefreshInterval, tt.wantRefresh)
			}
			if cfg.DocsFullRefreshInterval != tt.wantFull {
				t.Fatalf("full refresh interval = %s, want %s", cfg.DocsFullRefreshInterval, tt.wantFull)
			}
		})
	}
}
