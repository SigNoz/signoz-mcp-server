package analytics

import "testing"

func TestConfigFromEnv_ChoosesWhereAnalyticsGoes(t *testing.T) {
	tests := []struct {
		name       string
		buildKey   string
		enabled    string
		segmentKey string
		doNotTrack string
		want       Config
	}{
		{
			name:       "operator pipeline keeps its key and identity",
			buildKey:   "release-key",
			enabled:    "true",
			segmentKey: "operator-key",
			want:       Config{Enabled: true, Segment: SegmentConfig{Key: "operator-key"}},
		},
		{
			name:       "operator pipeline is not turned off by DO_NOT_TRACK",
			buildKey:   "release-key",
			enabled:    "true",
			segmentKey: "operator-key",
			doNotTrack: "1",
			want:       Config{Enabled: true, Segment: SegmentConfig{Key: "operator-key"}},
		},
		{
			name:     "release build with no settings sends anonymous feedback",
			buildKey: "release-key",
			want:     Config{Enabled: true, Anonymous: true, Segment: SegmentConfig{Key: "release-key"}},
		},
		{
			name:     "enabled without an operator key stays anonymous",
			buildKey: "release-key",
			enabled:  "true",
			want:     Config{Enabled: true, Anonymous: true, Segment: SegmentConfig{Key: "release-key"}},
		},
		{
			name:       "an operator key without ANALYTICS_ENABLED is not used",
			buildKey:   "release-key",
			segmentKey: "operator-key",
			want:       Config{Enabled: true, Anonymous: true, Segment: SegmentConfig{Key: "release-key"}},
		},
		{
			name:     "ANALYTICS_ENABLED=false opts a release build out",
			buildKey: "release-key",
			enabled:  "false",
			want:     Config{},
		},
		{
			name:       "DO_NOT_TRACK=1 opts a release build out",
			buildKey:   "release-key",
			doNotTrack: "1",
			want:       Config{},
		},
		{
			name:     "build from source sends nothing",
			buildKey: "<unset>",
			want:     Config{},
		},
		{
			name:     "release build without a key sends nothing",
			buildKey: "",
			enabled:  "true",
			want:     Config{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := key
			key = tt.buildKey
			t.Cleanup(func() { key = original })
			t.Setenv(EnabledEnv, tt.enabled)
			t.Setenv(SegmentKeyEnv, tt.segmentKey)
			t.Setenv(DoNotTrackEnv, tt.doNotTrack)

			if got := ConfigFromEnv(); got != tt.want {
				t.Fatalf("ConfigFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
