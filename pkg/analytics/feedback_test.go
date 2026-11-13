package analytics

import "testing"

func TestFeedbackConfigFromEnv_DecidesWhetherAndHowFeedbackIsSent(t *testing.T) {
	on := func(deployment string) FeedbackConfig {
		return FeedbackConfig{Enabled: true, Deployment: deployment, Segment: SegmentConfig{Key: "release-key"}}
	}
	off := FeedbackConfig{Deployment: DeploymentSelfHosted}
	tests := []struct {
		name       string
		buildKey   string
		enabled    string
		deployment string
		analytics  string
		want       FeedbackConfig
	}{
		{name: "release build with no settings sends feedback as self-hosted", buildKey: "release-key", want: on(DeploymentSelfHosted)},
		{name: "FEEDBACK_ENABLED=true keeps it on", buildKey: "release-key", enabled: "true", want: on(DeploymentSelfHosted)},
		{name: "FEEDBACK_ENABLED=false turns it off", buildKey: "release-key", enabled: "false", want: off},
		{name: "an unreadable FEEDBACK_ENABLED turns it off", buildKey: "release-key", enabled: "off", want: off},
		{name: "analytics settings do not turn feedback off", buildKey: "release-key", analytics: "false", want: on(DeploymentSelfHosted)},
		{name: "MCP_DEPLOYMENT=cloud marks the hosted deployment", buildKey: "release-key", deployment: " Cloud ", want: on(DeploymentCloud)},
		{name: "an unknown deployment is self-hosted", buildKey: "release-key", deployment: "staging", want: on(DeploymentSelfHosted)},
		{name: "build from source sends nothing", buildKey: "<unset>", enabled: "true", want: off},
		{name: "release build without a key sends nothing", buildKey: "", enabled: "true", want: off},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := feedbackKey
			feedbackKey = tt.buildKey
			t.Cleanup(func() { feedbackKey = original })
			t.Setenv(FeedbackEnabledEnv, tt.enabled)
			t.Setenv(DeploymentEnv, tt.deployment)
			t.Setenv("ANALYTICS_ENABLED", tt.analytics)

			if got := FeedbackConfigFromEnv(); got != tt.want {
				t.Fatalf("FeedbackConfigFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
