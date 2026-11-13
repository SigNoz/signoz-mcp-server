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
	DeploymentEnv      = "MCP_DEPLOYMENT"

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
	// Deployment labels every report. DeploymentCloud also attaches the org
	// and user; any other deployment is attributed to a hash of the org ID.
	Deployment string
	Segment    SegmentConfig
}

// FeedbackConfigFromEnv turns feedback on for a build that carries a key,
// unless FEEDBACK_ENABLED is set to anything but a true value. An unreadable
// value turns feedback off, so a mistyped opt-out still opts out.
func FeedbackConfigFromEnv() FeedbackConfig {
	cfg := FeedbackConfig{Deployment: DeploymentSelfHosted}
	if strings.EqualFold(strings.TrimSpace(os.Getenv(DeploymentEnv)), DeploymentCloud) {
		cfg.Deployment = DeploymentCloud
	}
	if feedbackKey == "" || feedbackKey == "<unset>" {
		return cfg
	}
	if value := strings.TrimSpace(os.Getenv(FeedbackEnabledEnv)); value != "" {
		if enabled, err := strconv.ParseBool(value); err != nil || !enabled {
			return cfg
		}
	}
	cfg.Enabled = true
	cfg.Segment = SegmentConfig{Key: feedbackKey}
	return cfg
}

// anonymousIDSalt scopes the hash to this server, so it cannot be matched
// against a hash of the same org ID made by another application.
const anonymousIDSalt = "signoz-mcp-server"

// AnonymousID is the identity self-hosted feedback carries: an HMAC-SHA256 of
// the SigNoz org ID. It is stable for one org and cannot be turned back into
// the ID.
func AnonymousID(orgID string) string {
	mac := hmac.New(sha256.New, []byte(anonymousIDSalt))
	mac.Write([]byte(orgID))
	return hex.EncodeToString(mac.Sum(nil))
}
