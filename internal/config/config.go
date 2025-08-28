package config

import (
	"os"
)

type Config struct {
	URL      string
	APIKey   string
	LogLevel string
}

const (
	SignozURL    = "SIGNOZ_URL"
	SignozApiKey = "SIGNOZ_API_KEY"
	LogLevel     = "LOG_LEVEL"
)

func LoadConfig() (*Config, error) {
	return &Config{
		URL:      getEnv(SignozURL, ""),
		APIKey:   getEnv(SignozApiKey, ""),
		LogLevel: getEnv(LogLevel, "info"),
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
