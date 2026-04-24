package config

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/pkg/session"
)

type Config struct {
	URL           string
	APIKey        string
	LogLevel      string
	TransportMode string
	Port          string
	MCPMode       string
	DocsOnlyMode  bool

	OAuthEnabled     bool
	OAuthTokenSecret string
	OAuthIssuerURL   string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	AuthCodeTTL      time.Duration

	// Client cache settings for multi-tenant mode
	ClientCacheSize int
	ClientCacheTTL  time.Duration

	CustomHeaders map[string]string
	// Analytics settings
	AnalyticsEnabled bool
	SegmentKey       string

	DocsRefreshInterval      time.Duration
	DocsFullRefreshInterval  time.Duration
	TrustedProxyCIDRs        []*net.IPNet
	PublicRateLimitBypassIPs map[string]struct{}

	// PublicSessionKeys is the HMAC key ring used to sign the stateless
	// tokens issued to public (docs-only) MCP clients on `initialize`.
	// Index 0 is the active signer; every entry is accepted on verify so
	// rolling key rotation is safe.
	//
	// Keys are provided via SIGNOZ_MCP_PUBLIC_SESSION_KEYS as a
	// comma-separated list of base64 strings. If unset, the server mints
	// an ephemeral 32-byte key per pod — fine for single-replica /
	// local-dev, but public sessions will NOT survive pod restarts or
	// round-robin across replicas. Multi-replica deployments must set
	// the env var to a common value on every pod.
	PublicSessionKeys [][]byte

	// PublicSessionTTL bounds how long a signed public-session token is
	// valid. Shorter TTLs reduce replay-window exposure if a pod's
	// traffic gets mirrored somewhere it shouldn't; longer TTLs reduce
	// the frequency of forced client re-initialization.
	PublicSessionTTL time.Duration
}

const (
	SignozURL     = "SIGNOZ_URL"
	SignozApiKey  = "SIGNOZ_API_KEY"
	LogLevel      = "LOG_LEVEL"
	TransportMode = "TRANSPORT_MODE"
	MCPPort       = "MCP_SERVER_PORT"
	MCPMode       = "SIGNOZ_MCP_MODE"

	SignozCustomHeaders = "SIGNOZ_CUSTOM_HEADERS"
	ClientCacheSize     = "CLIENT_CACHE_SIZE"
	ClientCacheTTL      = "CLIENT_CACHE_TTL_MINUTES"

	AnalyticsEnabledEnv = "ANALYTICS_ENABLED"
	SegmentKeyEnv       = "SEGMENT_KEY"

	OAuthEnabledEnv         = "OAUTH_ENABLED"
	OAuthTokenSecretEnv     = "OAUTH_TOKEN_SECRET"
	OAuthIssuerURLEnv       = "OAUTH_ISSUER_URL"
	OAuthAccessTTLMinutes   = "OAUTH_ACCESS_TOKEN_TTL_MINUTES"
	OAuthRefreshTTLMinutes  = "OAUTH_REFRESH_TOKEN_TTL_MINUTES"
	OAuthAuthCodeTTLSeconds = "OAUTH_AUTH_CODE_TTL_SECONDS"

	DocsRefreshIntervalEnv      = "SIGNOZ_DOCS_REFRESH_INTERVAL"
	DocsFullRefreshIntervalEnv  = "SIGNOZ_DOCS_FULL_REFRESH_INTERVAL"
	TrustedProxyCIDRsEnv        = "SIGNOZ_MCP_TRUSTED_PROXY_CIDRS"
	PublicRateLimitBypassIPsEnv = "SIGNOZ_MCP_PUBLIC_RATE_LIMIT_BYPASS_IPS"
	PublicSessionKeysEnv        = "SIGNOZ_MCP_PUBLIC_SESSION_KEYS"
	PublicSessionTTLEnv         = "SIGNOZ_MCP_PUBLIC_SESSION_TTL"

	defaultClientCacheSize       = 256
	defaultClientCacheTTLMinutes = 30
	defaultAccessTTLMinutes      = 60    // 1 hour
	defaultRefreshTTLMinutes     = 43200 // 30 days
	defaultAuthCodeTTLSeconds    = 600
	defaultDocsRefreshInterval   = 6 * time.Hour
	defaultDocsFullRefreshPeriod = 24 * time.Hour
	defaultPublicSessionTTL      = time.Hour
)

func LoadConfig() (*Config, error) {
	// Trim trailing slash from URL to prevent double-slash issues in API paths
	url := strings.TrimSuffix(getEnv(SignozURL, ""), "/")

	cacheSize := getEnvInt(ClientCacheSize, defaultClientCacheSize)
	cacheTTLMinutes := getEnvInt(ClientCacheTTL, defaultClientCacheTTLMinutes)
	accessTTLMinutes := getEnvInt(OAuthAccessTTLMinutes, defaultAccessTTLMinutes)
	refreshTTLMinutes := getEnvInt(OAuthRefreshTTLMinutes, defaultRefreshTTLMinutes)
	authCodeTTLSeconds := getEnvInt(OAuthAuthCodeTTLSeconds, defaultAuthCodeTTLSeconds)
	docsRefreshInterval := getEnvDuration(DocsRefreshIntervalEnv, defaultDocsRefreshInterval)
	docsFullRefreshInterval := getEnvDuration(DocsFullRefreshIntervalEnv, defaultDocsFullRefreshPeriod)
	publicSessionTTL := getEnvDuration(PublicSessionTTLEnv, defaultPublicSessionTTL)
	// Parse the public-session HMAC key ring. We fail LoadConfig on a
	// malformed env var rather than silently falling back to ephemeral
	// keys — operators asked for a specific ring, so quietly ignoring
	// typos would be a multi-pod-correctness footgun.
	publicSessionKeys, err := session.ParseKeysFromEnv(getEnv(PublicSessionKeysEnv, ""))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", PublicSessionKeysEnv, err)
	}
	if docsFullRefreshInterval < docsRefreshInterval {
		log.Printf("WARN: %s (%s) is shorter than %s (%s); falling back to defaults",
			DocsFullRefreshIntervalEnv, docsFullRefreshInterval, DocsRefreshIntervalEnv, docsRefreshInterval)
		docsRefreshInterval = defaultDocsRefreshInterval
		docsFullRefreshInterval = defaultDocsFullRefreshPeriod
	}
	mode := getEnv(MCPMode, "")

	// Parse custom headers from SIGNOZ_CUSTOM_HEADERS env var (format: "Key1:Value1,Key2:Value2")
	customHeaders := make(map[string]string)
	if headersStr := getEnv(SignozCustomHeaders, ""); headersStr != "" {
		for _, pair := range strings.Split(headersStr, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) != 2 {
				log.Printf("WARN: skipping malformed custom header entry (missing ':'): %q", strings.TrimSpace(pair))
			} else if strings.TrimSpace(parts[0]) == "" {
				log.Printf("WARN: skipping custom header entry with empty name: %q", strings.TrimSpace(pair))
			} else {
				customHeaders[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	return &Config{
		URL:                      url,
		APIKey:                   getEnv(SignozApiKey, ""),
		LogLevel:                 getEnv(LogLevel, "info"),
		TransportMode:            getEnv(TransportMode, "stdio"),
		Port:                     getEnv(MCPPort, "8000"),
		MCPMode:                  mode,
		DocsOnlyMode:             mode == "docs-only",
		OAuthEnabled:             getEnvBool(OAuthEnabledEnv, false),
		OAuthTokenSecret:         getEnv(OAuthTokenSecretEnv, ""),
		OAuthIssuerURL:           strings.TrimSuffix(getEnv(OAuthIssuerURLEnv, ""), "/"),
		AccessTokenTTL:           time.Duration(accessTTLMinutes) * time.Minute,
		RefreshTokenTTL:          time.Duration(refreshTTLMinutes) * time.Minute,
		AuthCodeTTL:              time.Duration(authCodeTTLSeconds) * time.Second,
		ClientCacheSize:          cacheSize,
		ClientCacheTTL:           time.Duration(cacheTTLMinutes) * time.Minute,
		CustomHeaders:            customHeaders,
		AnalyticsEnabled:         getEnvBool(AnalyticsEnabledEnv, false),
		SegmentKey:               getEnv(SegmentKeyEnv, ""),
		DocsRefreshInterval:      docsRefreshInterval,
		DocsFullRefreshInterval:  docsFullRefreshInterval,
		TrustedProxyCIDRs:        parseCIDRs(getEnv(TrustedProxyCIDRsEnv, "")),
		PublicRateLimitBypassIPs: parseIPs(getEnv(PublicRateLimitBypassIPsEnv, "")),
		PublicSessionKeys:        publicSessionKeys,
		PublicSessionTTL:         publicSessionTTL,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
		log.Printf("WARN: invalid duration for %s=%q; using %s", key, value, defaultValue)
	}
	return defaultValue
}

func parseCIDRs(raw string) []*net.IPNet {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []*net.IPNet
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(item); err == nil {
			out = append(out, cidr)
		} else {
			log.Printf("WARN: skipping invalid CIDR in %s: %q", TrustedProxyCIDRsEnv, item)
		}
	}
	return out
}

func parseIPs(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		ip := net.ParseIP(item)
		if ip == nil {
			log.Printf("WARN: skipping invalid IP in %s: %q", PublicRateLimitBypassIPsEnv, item)
			continue
		}
		out[ip.String()] = struct{}{}
	}
	return out
}

func (c *Config) ValidateConfig() error {
	// In HTTP mode, API key can come from Authorization header, so it's optional.
	// In stdio mode, API key must be provided via environment variable.
	if c.TransportMode == "stdio" && !c.DocsOnlyMode && c.APIKey == "" {
		return fmt.Errorf("SIGNOZ_API_KEY is required for stdio mode")
	}

	if c.TransportMode == "stdio" && !c.DocsOnlyMode && c.URL == "" {
		return fmt.Errorf("SIGNOZ_URL is required for stdio mode")
	}

	if c.TransportMode == "stdio" && c.DocsOnlyMode {
		log.Printf("WARN: SIGNOZ_MCP_MODE=docs-only enabled; SIGNOZ_URL and SIGNOZ_API_KEY are optional")
	}

	if c.TransportMode == "http" {
		if c.Port == "" {
			return fmt.Errorf("MCP_SERVER_PORT is required for HTTP transport mode")
		}
	}

	if c.OAuthEnabled {
		if len(c.OAuthTokenSecret) < 32 {
			return fmt.Errorf("OAUTH_TOKEN_SECRET is required and must be at least 32 bytes when OAUTH_ENABLED=true")
		}
		if c.OAuthIssuerURL == "" {
			return fmt.Errorf("OAUTH_ISSUER_URL is required when OAUTH_ENABLED=true")
		}
	}
	return nil
}
