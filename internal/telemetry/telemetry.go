package telemetry

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

// InitTracer sets up an OTLP gRPC trace exporter and registers a global
// TracerProvider. Configuration (endpoint, headers, service name) is read
// from the standard OTEL_* environment variables by the SDK automatically.
//
// It returns a shutdown function that should be deferred in main.
func InitTracer(ctx context.Context) (func(context.Context) error, error) {
	res, err := newResource(ctx)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// InitMeterProvider sets up an OTLP gRPC metric exporter and registers a
// global MeterProvider. The otelhttp middleware automatically records HTTP
// metrics (request duration, size, etc.) using the global MeterProvider.
//
// It returns a shutdown function that should be deferred in main.
func InitMeterProvider(ctx context.Context) (func(context.Context) error, error) {
	res, err := newResource(ctx)
	if err != nil {
		return nil, err
	}

	metricExporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)

	return mp.Shutdown, nil
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
	return input.With(zap.String("tenant_url", url))
}

func newResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
	)
}
