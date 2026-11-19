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

func TestLoadConfigDocsRefreshCompatibility(t *testing.T) {
	tests := []struct {
		name, refresh, legacy string
		interval, fallback    time.Duration
		scheduleOff           bool
		fallbackOff           bool
		fallbackEveryTick     bool
	}{
		{name: "defaults", interval: 6 * time.Hour, fallback: 24 * time.Hour},
		{name: "shorter legacy interval keeps the primary cadence", refresh: "10m", legacy: "5m", interval: 10 * time.Minute, fallback: 5 * time.Minute},
		{name: "legacy-only cadence keeps refreshing", refresh: "0", legacy: "12h", interval: 12 * time.Hour, fallback: 12 * time.Hour, fallbackEveryTick: true},
		{name: "legacy off disables only the unchanged-sitemap check", legacy: "off", interval: 6 * time.Hour, fallback: 24 * time.Hour, fallbackOff: true},
		{name: "both off disables the scheduler", refresh: "off", legacy: "off", scheduleOff: true, fallbackOff: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(DocsRefreshIntervalEnv, tt.refresh)
			t.Setenv(DocsFullRefreshIntervalEnv, tt.legacy)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DocsRefreshDisabled != tt.scheduleOff || cfg.DocsFallbackDisabled != tt.fallbackOff {
				t.Fatalf("schedule and fallback disabled = (%t, %t), want (%t, %t)",
					cfg.DocsRefreshDisabled, cfg.DocsFallbackDisabled, tt.scheduleOff, tt.fallbackOff)
			}
			if cfg.DocsFallbackEveryTick != tt.fallbackEveryTick {
				t.Fatalf("revalidate on every fallback tick = %t, want %t", cfg.DocsFallbackEveryTick, tt.fallbackEveryTick)
			}
			if !tt.scheduleOff && cfg.DocsRefreshInterval != tt.interval {
				t.Fatalf("refresh interval = %s, want %s", cfg.DocsRefreshInterval, tt.interval)
			}
			if !tt.fallbackOff && cfg.DocsFallbackCheckPeriod != tt.fallback {
				t.Fatalf("fallback revalidation interval = %s, want %s", cfg.DocsFallbackCheckPeriod, tt.fallback)
			}
		})
	}
}
