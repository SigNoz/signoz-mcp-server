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

// feedbackKey is the Segment write key for agent feedback when the operator
// set no SEGMENT_KEY. Release builds set it through ldflags; a build from
// source keeps "<unset>".
var feedbackKey = "<unset>"

// FeedbackConfig is separate from Config on purpose: agent feedback has its own
// switch and never reads ANALYTICS_ENABLED.
type FeedbackConfig struct {
	Enabled bool
	Segment SegmentConfig
}

// FeedbackConfigFromEnv sends feedback to segmentKey (SEGMENT_KEY), else to the
// key built into the release; with neither it is off. FEEDBACK_ENABLED turns it
// off unless it is empty or a true value, so a mistyped opt-out still opts out.
func FeedbackConfigFromEnv(segmentKey string) FeedbackConfig {
	key := segmentKey
	if key == "" && feedbackKey != "<unset>" {
		key = feedbackKey
	}
	if key == "" {
		return FeedbackConfig{}
	}
	if value := strings.TrimSpace(os.Getenv(FeedbackEnabledEnv)); value != "" {
		if enabled, err := strconv.ParseBool(value); err != nil || !enabled {
			return FeedbackConfig{}
		}
	}
	return FeedbackConfig{Enabled: true, Segment: SegmentConfig{Key: key}}
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
