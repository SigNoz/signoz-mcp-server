package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func InitTracerProvider(ctx context.Context, res *resource.Resource) (func(context.Context) error, ExporterStatus, error) {
	setTextMapPropagator()

	status := TraceExporterStatus()
	if status != ExporterStatusEnabled {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		return nil, status, nil
	}

	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, status, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	return tp.Shutdown, status, nil
}

func setTextMapPropagator() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}
