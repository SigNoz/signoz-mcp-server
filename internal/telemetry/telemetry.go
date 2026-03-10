package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

// InitLogProvider sets up an OTLP gRPC log exporter and registers a global
// LoggerProvider. Once registered, the otelzap bridge core will forward zap
// log records to the OTel backend.
//
// It returns a shutdown function that should be deferred in main.
func InitLogProvider(ctx context.Context) (func(context.Context) error, error) {
	res, err := newResource(ctx)
	if err != nil {
		return nil, err
	}

	logExporter, err := otlploggrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	lp := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
		log.WithResource(res),
	)

	otellog.SetLoggerProvider(lp)

	return lp.Shutdown, nil
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

func newResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
	)
}
