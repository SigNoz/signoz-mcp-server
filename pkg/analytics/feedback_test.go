package analytics

import "testing"

func TestFeedbackConfigFromEnv_DecidesWhetherAndWhereFeedbackIsSent(t *testing.T) {
	toRelease := FeedbackConfig{Enabled: true, Segment: SegmentConfig{Key: "release-key"}}
	toOperator := FeedbackConfig{Enabled: true, Segment: SegmentConfig{Key: "operator-key"}}
	off := FeedbackConfig{}
	tests := []struct {
		name       string
		buildKey   string
		segmentKey string
		enabled    string
		analytics  string
		want       FeedbackConfig
	}{
		{name: "release build with no settings sends to the release key", buildKey: "release-key", want: toRelease},
		{name: "SEGMENT_KEY takes the feedback instead of the release key", buildKey: "release-key", segmentKey: "operator-key", want: toOperator},
		{name: "SEGMENT_KEY takes the feedback with analytics off", buildKey: "release-key", segmentKey: "operator-key", analytics: "false", want: toOperator},
		{name: "SEGMENT_KEY takes the feedback in a build from source", buildKey: "<unset>", segmentKey: "operator-key", want: toOperator},
		{name: "FEEDBACK_ENABLED=true keeps it on", buildKey: "release-key", enabled: "true", want: toRelease},
		{name: "FEEDBACK_ENABLED=false turns it off", buildKey: "release-key", enabled: "false", want: off},
		{name: "FEEDBACK_ENABLED=false turns it off with SEGMENT_KEY set", buildKey: "release-key", segmentKey: "operator-key", enabled: "false", want: off},
		{name: "an unreadable FEEDBACK_ENABLED turns it off", buildKey: "release-key", enabled: "off", want: off},
		{name: "analytics settings do not turn feedback off", buildKey: "release-key", analytics: "false", want: toRelease},
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

			if got := FeedbackConfigFromEnv(tt.segmentKey); got != tt.want {
				t.Fatalf("FeedbackConfigFromEnv(%q) = %+v, want %+v", tt.segmentKey, got, tt.want)
			}
		})
	}
}
