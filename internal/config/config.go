package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	URL           string
	APIKey        string
	LogLevel      string
	TransportMode string
	Port          string

	OAuthEnabled     bool
	OAuthTokenSecret string
	OAuthIssuerURL   string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	AuthCodeTTL      time.Duration

	// Client cache settings for multi-tenant mode
	ClientCacheSize int
	ClientCacheTTL  time.Duration
}

const (
	SignozURL     = "SIGNOZ_URL"
	SignozApiKey  = "SIGNOZ_API_KEY"
	LogLevel      = "LOG_LEVEL"
	TransportMode = "TRANSPORT_MODE"
	MCPPort       = "MCP_SERVER_PORT"

	ClientCacheSize = "CLIENT_CACHE_SIZE"
	ClientCacheTTL  = "CLIENT_CACHE_TTL_MINUTES"

	OAuthEnabledEnv         = "OAUTH_ENABLED"
	OAuthTokenSecretEnv     = "OAUTH_TOKEN_SECRET"
	OAuthIssuerURLEnv       = "OAUTH_ISSUER_URL"
	OAuthAccessTTLMinutes   = "OAUTH_ACCESS_TOKEN_TTL_MINUTES"
	OAuthRefreshTTLMinutes  = "OAUTH_REFRESH_TOKEN_TTL_MINUTES"
	OAuthAuthCodeTTLSeconds = "OAUTH_AUTH_CODE_TTL_SECONDS"

	defaultClientCacheSize       = 256
	defaultClientCacheTTLMinutes = 30
	defaultAccessTTLMinutes      = 60       // 1 hour
	defaultRefreshTTLMinutes     = 43200    // 30 days
	defaultAuthCodeTTLSeconds    = 600
)

func LoadConfig() (*Config, error) {
	// Trim trailing slash from URL to prevent double-slash issues in API paths
	url := strings.TrimSuffix(getEnv(SignozURL, ""), "/")

	cacheSize := getEnvInt(ClientCacheSize, defaultClientCacheSize)
	cacheTTLMinutes := getEnvInt(ClientCacheTTL, defaultClientCacheTTLMinutes)
	accessTTLMinutes := getEnvInt(OAuthAccessTTLMinutes, defaultAccessTTLMinutes)
	refreshTTLMinutes := getEnvInt(OAuthRefreshTTLMinutes, defaultRefreshTTLMinutes)
	authCodeTTLSeconds := getEnvInt(OAuthAuthCodeTTLSeconds, defaultAuthCodeTTLSeconds)

	return &Config{
		URL:              url,
		APIKey:           getEnv(SignozApiKey, ""),
		LogLevel:         getEnv(LogLevel, "info"),
		TransportMode:    getEnv(TransportMode, "stdio"),
		Port:             getEnv(MCPPort, "8000"),
		OAuthEnabled:     getEnvBool(OAuthEnabledEnv, false),
		OAuthTokenSecret: getEnv(OAuthTokenSecretEnv, ""),
		OAuthIssuerURL:   strings.TrimSuffix(getEnv(OAuthIssuerURLEnv, ""), "/"),
		AccessTokenTTL:   time.Duration(accessTTLMinutes) * time.Minute,
		RefreshTokenTTL:  time.Duration(refreshTTLMinutes) * time.Minute,
		AuthCodeTTL:      time.Duration(authCodeTTLSeconds) * time.Second,
		ClientCacheSize:  cacheSize,
		ClientCacheTTL:   time.Duration(cacheTTLMinutes) * time.Minute,
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

func (c *Config) ValidateConfig() error {
	// In HTTP mode, API key can come from Authorization header, so it's optional.
	// In stdio mode, API key must be provided via environment variable.
	if c.TransportMode == "stdio" && c.APIKey == "" {
		return fmt.Errorf("SIGNOZ_API_KEY is required for stdio mode")
	}

	if c.TransportMode == "stdio" && c.URL == "" {
		return fmt.Errorf("SIGNOZ_URL is required for stdio mode")
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
