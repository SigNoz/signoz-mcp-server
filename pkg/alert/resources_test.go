package alert

import (
	"strings"
	"testing"
)

func TestAlertInstructionsDescribePolicyAwareChannelPreflight(t *testing.T) {
	for _, required := range []string{
		"same still-current prepared operation",
		"refresh if state may have changed",
		"fully paginated signoz_list_notification_channels",
		"list is config-free, defaults to 20 rows, and accepts at most 200 per page",
		"immutable routing identity is displayName, not machine name",
		"offer signoz_create_notification_channel",
		"settings the user provides",
		"default test to false",
		"Never guess or create automatically",
		"every threshold tier needs an exact returned displayName",
		"top-level preferredChannels is rejected",
		"Confirmed v2 policy routing sets notificationSettings.usePolicy=true",
		"any supplied displayName values still require verification",
		"V1 anomaly rules use exact returned displayName values in top-level preferredChannels and cannot use policy routing",
	} {
		if !strings.Contains(Instructions, required) {
			t.Errorf("alert instructions missing notification-channel guidance %q", required)
		}
	}
	if strings.Contains(Instructions, "If the user explicitly names a channel, use it directly") {
		t.Error("alert instructions must not prescribe direct use of an unvalidated channel name")
	}
	if strings.Contains(Instructions, "requires at least one existing valid channel even when notificationSettings.usePolicy=true") {
		t.Error("alert instructions must not retain the obsolete policy-routing channel requirement")
	}
	if strings.Contains(Instructions, "Fall back to rule-level preferredChannels") {
		t.Error("alert instructions must not describe preferredChannels as a v2 routing fallback")
	}
}
