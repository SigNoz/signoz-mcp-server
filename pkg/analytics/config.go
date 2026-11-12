package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
)

const (
	EnabledEnv    = "ANALYTICS_ENABLED"
	SegmentKeyEnv = "SEGMENT_KEY"
	DoNotTrackEnv = "DO_NOT_TRACK"
)

// key is the Segment write key for anonymous agent feedback. Release builds
// set it through ldflags; a build from source keeps "<unset>" and sends nothing.
var key = "<unset>"

type Config struct {
	Enabled bool
	// Anonymous limits delivery to agent feedback, attributed to a hash of the
	// SigNoz org ID. When false, every event goes to the operator's own Segment
	// source with org and user identity.
	Anonymous bool
	Segment   SegmentConfig
}

type SegmentConfig struct {
	Key string
}

// ConfigFromEnv resolves where analytics goes. ANALYTICS_ENABLED=true with a
// SEGMENT_KEY is the operator's own pipeline and wins over everything else, so
// a deployment that already sets both keeps its events and identity unchanged.
// Otherwise a release build sends anonymous agent feedback unless the operator
// opts out with ANALYTICS_ENABLED=false or DO_NOT_TRACK=1.
func ConfigFromEnv() Config {
	enabled, enabledSet := envBool(EnabledEnv)
	if segmentKey := os.Getenv(SegmentKeyEnv); enabledSet && enabled && segmentKey != "" {
		return Config{Enabled: true, Segment: SegmentConfig{Key: segmentKey}}
	}

	doNotTrack, _ := envBool(DoNotTrackEnv)
	optedOut := doNotTrack || (enabledSet && !enabled)
	if optedOut || key == "" || key == "<unset>" {
		return Config{}
	}
	return Config{Enabled: true, Anonymous: true, Segment: SegmentConfig{Key: key}}
}

func envBool(name string) (value, set bool) {
	parsed, err := strconv.ParseBool(os.Getenv(name))
	return parsed, err == nil
}

// anonymousIDSalt scopes the hash to this server, so it cannot be matched
// against a hash of the same org ID made by another application.
const anonymousIDSalt = "signoz-mcp-server"

// AnonymousID is the identity anonymous events carry: an HMAC-SHA256 of the
// SigNoz org ID. It is stable for one org and cannot be turned back into the ID.
func AnonymousID(orgID string) string {
	mac := hmac.New(sha256.New, []byte(anonymousIDSalt))
	mac.Write([]byte(orgID))
	return hex.EncodeToString(mac.Sum(nil))
}
