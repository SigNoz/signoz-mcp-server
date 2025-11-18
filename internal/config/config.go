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
	SignozPrefix  bool
}

const (
	SignozURL     = "SIGNOZ_URL"
	SignozApiKey  = "SIGNOZ_API_KEY"
	LogLevel      = "LOG_LEVEL"
	TransportMode = "TRANSPORT_MODE"
	MCPPort       = "MCP_SERVER_PORT"
)

func LoadConfig() (*Config, error) {
	signozPrefix := flag.Bool("signoz-prefix", false, "Add signoz_ prefix to all tool names")
	
	// Suppress default flag error handling to prevent automatic exit
	flag.CommandLine.Usage = func() {}
	
	// Parse flags, but don't exit on error - just ignore unknown flags
	flag.Parse()

	return &Config{
		URL:           getEnv(SignozURL, ""),
		APIKey:        getEnv(SignozApiKey, ""),
		LogLevel:      getEnv(LogLevel, "info"),
		TransportMode: getEnv(TransportMode, "stdio"),
		Port:          getEnv(MCPPort, "8000"),
		SignozPrefix:  *signozPrefix,
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
