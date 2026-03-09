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

	// Client cache settings for multi-tenant mode
	ClientCacheSize int
	ClientCacheTTL  time.Duration

	// Per-tenant rate limiting (HTTP mode only)
	RateLimitPerTenant float64 // requests per second per tenant
	RateLimitBurst     int     // max burst size per tenant
}

const (
	SignozURL     = "SIGNOZ_URL"
	SignozApiKey  = "SIGNOZ_API_KEY"
	LogLevel      = "LOG_LEVEL"
	TransportMode = "TRANSPORT_MODE"
	MCPPort       = "MCP_SERVER_PORT"

	ClientCacheSize = "CLIENT_CACHE_SIZE"
	ClientCacheTTL  = "CLIENT_CACHE_TTL_MINUTES"

	RateLimitPerTenant = "RATE_LIMIT_PER_TENANT" // requests per second
	RateLimitBurst     = "RATE_LIMIT_BURST"      // max burst

	defaultClientCacheSize       = 256
	defaultClientCacheTTLMinutes = 30
	defaultRateLimitPerTenant    = 10 // 10 requests/sec
	defaultRateLimitBurst        = 20 // burst of 20
)

func LoadConfig() (*Config, error) {
	// Trim trailing slash from URL to prevent double-slash issues in API paths
	url := strings.TrimSuffix(getEnv(SignozURL, ""), "/")

	cacheSize := getEnvInt(ClientCacheSize, defaultClientCacheSize)
	cacheTTLMinutes := getEnvInt(ClientCacheTTL, defaultClientCacheTTLMinutes)

	return &Config{
		URL:                url,
		APIKey:             getEnv(SignozApiKey, ""),
		LogLevel:           getEnv(LogLevel, "info"),
		TransportMode:      getEnv(TransportMode, "stdio"),
		Port:               getEnv(MCPPort, "8000"),
		ClientCacheSize:    cacheSize,
		ClientCacheTTL:     time.Duration(cacheTTLMinutes) * time.Minute,
		RateLimitPerTenant: float64(getEnvInt(RateLimitPerTenant, defaultRateLimitPerTenant)),
		RateLimitBurst:     getEnvInt(RateLimitBurst, defaultRateLimitBurst),
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

func (c *Config) ValidateConfig() error {
	// if c.URL == "" {
	// 	return fmt.Errorf("SIGNOZ_URL is required")
	// }

	// In HTTP mode, API key can come from Authorization header, so it's optional
	// In stdio mode, API key must be provided via environment variable
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
	return nil
}
