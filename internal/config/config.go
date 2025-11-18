package config

import (
	"flag"
	"fmt"
	"os"
)

type Config struct {
	URL           string
	APIKey        string
	LogLevel      string
	TransportMode string
	Port          string
	ToolPrefix    string
}

const (
	SignozURL        = "SIGNOZ_URL"
	SignozApiKey     = "SIGNOZ_API_KEY"
	LogLevel         = "LOG_LEVEL"
	TransportMode    = "TRANSPORT_MODE"
	MCPPort          = "MCP_SERVER_PORT"
	SignozToolPrefix = "SIGNOZ_TOOL_PREFIX"
)

func LoadConfig() (*Config, error) {
	toolPrefix := flag.String("tool-prefix", "", "Prefix to add to all tool names (e.g., 'signoz' makes 'list_services' become 'signoz_list_services')")

	// Suppress default flag error handling to prevent automatic exit
	flag.CommandLine.Usage = func() {}

	// Parse flags, but don't exit on error - just ignore unknown flags
	flag.Parse()

	// Get prefix from flag or environment variable, default to empty string
	prefix := *toolPrefix
	if prefix == "" {
		prefix = getEnv(SignozToolPrefix, "")
	}

	return &Config{
		URL:           getEnv(SignozURL, ""),
		APIKey:        getEnv(SignozApiKey, ""),
		LogLevel:      getEnv(LogLevel, "info"),
		TransportMode: getEnv(TransportMode, "stdio"),
		Port:          getEnv(MCPPort, "8000"),
		ToolPrefix:    prefix,
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
