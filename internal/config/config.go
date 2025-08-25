package config

import (
	"fmt"
	"os"
)

const (
	SignozURL    = "SIGNOZ_URL"
	SignozApiKey = "SIGNOZ_API_KEY"
)

type Config struct {
	URL    string
	APIKey string
}

func LoadConfig() (*Config, error) {
	url := os.Getenv(SignozURL)
	if url == "" {
		return nil, fmt.Errorf("environment variable `%s` not set", SignozURL)
	}
	apiKey := os.Getenv(SignozApiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable `%s` not set", SignozApiKey)
	}
	return &Config{URL: url, APIKey: apiKey}, nil
}
