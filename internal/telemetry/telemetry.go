package telemetry

import (
	"context"
	"strings"

	"go.uber.org/zap"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

// InitTracer sets up an OTLP gRPC trace exporter and registers a global
// TracerProvider. Configuration (endpoint, headers, service name) is read
// from the standard OTEL_* environment variables by the SDK automatically.
//
// It returns a shutdown function that should be deferred in main.
func InitTracer(ctx context.Context) (func(context.Context) error, error) {
	res, err := otelpkg.NewResource(ctx, version.Version)
	if err != nil {
		return nil, err
	}
	return otelpkg.InitTracerProvider(ctx, res)
}

// InitMeterProvider sets up an OTLP gRPC metric exporter and registers a
// global MeterProvider. The otelhttp middleware automatically records HTTP
// metrics (request duration, size, etc.) using the global MeterProvider.
//
// It returns a shutdown function that should be deferred in main.
func InitMeterProvider(ctx context.Context) (func(context.Context) error, error) {
	res, err := otelpkg.NewResource(ctx, version.Version)
	if err != nil {
		return nil, err
	}
	return otelpkg.InitMeterProvider(ctx, res)
}

type LogLevel string

// NewLogger creates a new logger with the specified level.
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

// LoggerWithURL returns a child logger enriched with the given URL field.
func LoggerWithURL(input *zap.Logger, url string) *zap.Logger {
	return input.With(zap.String("mcp.tenant_url", url))
}
