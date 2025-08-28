package logger

import (
	"strings"

	"go.uber.org/zap"
)

type LogLevel string

// NewLogger creates a new logger with specified level
func NewLogger(level LogLevel) (*zap.Logger, error) {
	config := zap.NewProductionConfig()

	switch strings.ToLower(string(level)) {
	case "debug":
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "error":
		config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	return config.Build()
}
