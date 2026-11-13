package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
)

const (
	FeedbackEnabledEnv = "FEEDBACK_ENABLED"

	// Values of the deployment property on a feedback report.
	DeploymentCloud      = "cloud"
	DeploymentSelfHosted = "self_hosted"
)

// feedbackKey is the Segment write key for agent feedback. Release builds set
// it through ldflags; a build from source keeps "<unset>" and sends nothing.
var feedbackKey = "<unset>"

// FeedbackConfig is separate from Config on purpose: agent feedback has its own
// Segment source and switch, and never reads ANALYTICS_ENABLED or SEGMENT_KEY.
type FeedbackConfig struct {
	Enabled bool
	Segment SegmentConfig
}

// FeedbackConfigFromEnv turns feedback on for a build that carries a key,
// unless FEEDBACK_ENABLED is set to anything but a true value. An unreadable
// value turns feedback off, so a mistyped opt-out still opts out. An empty
// value counts as unset, like every other setting this server reads.
func FeedbackConfigFromEnv() FeedbackConfig {
	if feedbackKey == "" || feedbackKey == "<unset>" {
		return FeedbackConfig{}
	}
	if value := strings.TrimSpace(os.Getenv(FeedbackEnabledEnv)); value != "" {
		if enabled, err := strconv.ParseBool(value); err != nil || !enabled {
			return FeedbackConfig{}
		}
	}
	return FeedbackConfig{Enabled: true, Segment: SegmentConfig{Key: feedbackKey}}
}

// anonymousIDSalt scopes the hash to this server, so it cannot be matched
// against a hash of the same org ID made by another application.
const anonymousIDSalt = "signoz-mcp-server"

// AnonymousID is the identity feedback about a self-hosted SigNoz carries: an
// HMAC-SHA256 of the SigNoz org ID. It is stable for one org and cannot be turned back into
// the ID.
func AnonymousID(orgID string) string {
	mac := hmac.New(sha256.New, []byte(anonymousIDSalt))
	mac.Write([]byte(orgID))
	return hex.EncodeToString(mac.Sum(nil))
}
