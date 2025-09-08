package config

import (
	"fmt"
	"os"
)

type Config struct {
	URL            string
	APIKey         string
	LogLevel       string
	DeploymentMode string
	Port           string
}

const (
	SignozURL      = "SIGNOZ_URL"
	SignozApiKey   = "SIGNOZ_API_KEY"
	LogLevel       = "LOG_LEVEL"
	DeploymentMode = "DEPLOYMENT_MODE"
	MCPPort        = "MCP_SERVER_PORT"
)

func LoadConfig() (*Config, error) {
	return &Config{
		URL:            getEnv(SignozURL, ""),
		APIKey:         getEnv(SignozApiKey, ""),
		LogLevel:       getEnv(LogLevel, "info"),
		DeploymentMode: getEnv(DeploymentMode, "local"),
		Port:           getEnv(MCPPort, "8000"),
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
	if c.APIKey == "" {
		return fmt.Errorf("SIGNOZ_API_KEY is required")
	}

	if c.DeploymentMode == "cloud" {
		if c.Port == "" {
			return fmt.Errorf("MCP_SERVER_PORT is required for cloud deployment")
		}
	}
	return nil
}
