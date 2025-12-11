package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	URL           string
	APIKey        string
	LogLevel      string
	TransportMode string
	Port          string
}

const (
	SignozURL     = "SIGNOZ_URL"
	SignozApiKey  = "SIGNOZ_API_KEY"
	LogLevel      = "LOG_LEVEL"
	TransportMode = "TRANSPORT_MODE"
	MCPPort       = "MCP_SERVER_PORT"
)

func LoadConfig() (*Config, error) {
	// Trim trailing slash from URL to prevent double-slash issues in API paths
	url := strings.TrimSuffix(getEnv(SignozURL, ""), "/")
	return &Config{
		URL:           url,
		APIKey:        getEnv(SignozApiKey, ""),
		LogLevel:      getEnv(LogLevel, "info"),
		TransportMode: getEnv(TransportMode, "stdio"),
		Port:          getEnv(MCPPort, "8000"),
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func (c *Config) ValidateConfig() error {
	if c.URL == "" {
		return fmt.Errorf("SIGNOZ_URL is required")
	}

	// In HTTP mode, API key can come from Authorization header, so it's optional
	// In stdio mode, API key must be provided via environment variable
	if c.TransportMode != "http" && c.APIKey == "" {
		return fmt.Errorf("SIGNOZ_API_KEY is required for stdio mode")
	}

	if c.TransportMode == "http" {
		if c.Port == "" {
			return fmt.Errorf("MCP_SERVER_PORT is required for HTTP transport mode")
		}
	}
	return nil
}
