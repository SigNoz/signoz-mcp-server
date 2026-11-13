package analytics

import "testing"

func TestFeedbackConfigFromEnv_DecidesWhetherFeedbackIsSent(t *testing.T) {
	on := FeedbackConfig{Enabled: true, Segment: SegmentConfig{Key: "release-key"}}
	off := FeedbackConfig{}
	tests := []struct {
		name      string
		buildKey  string
		enabled   string
		analytics string
		want      FeedbackConfig
	}{
		{name: "release build with no settings sends feedback", buildKey: "release-key", want: on},
		{name: "FEEDBACK_ENABLED=true keeps it on", buildKey: "release-key", enabled: "true", want: on},
		{name: "FEEDBACK_ENABLED=false turns it off", buildKey: "release-key", enabled: "false", want: off},
		{name: "an unreadable FEEDBACK_ENABLED turns it off", buildKey: "release-key", enabled: "off", want: off},
		{name: "analytics settings do not turn feedback off", buildKey: "release-key", analytics: "false", want: on},
		{name: "build from source sends nothing", buildKey: "<unset>", enabled: "true", want: off},
		{name: "release build without a key sends nothing", buildKey: "", enabled: "true", want: off},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := feedbackKey
			feedbackKey = tt.buildKey
			t.Cleanup(func() { feedbackKey = original })
			t.Setenv(FeedbackEnabledEnv, tt.enabled)
			t.Setenv("ANALYTICS_ENABLED", tt.analytics)

			if got := FeedbackConfigFromEnv(); got != tt.want {
				t.Fatalf("FeedbackConfigFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
