package logger

import (
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

// WithOTelBridge wraps an existing zap logger with an additional otelzap core
// so that all log records are also forwarded to the global OTel LoggerProvider.
// The original console/file output is preserved.
func WithOTelBridge(base *zap.Logger) *zap.Logger {
	otelCore := otelzap.NewCore("signoz-mcp-server")
	combined := zapcore.NewTee(base.Core(), otelCore)
	return zap.New(combined, zap.WithCaller(true))
}
