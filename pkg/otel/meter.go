package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

func InitMeterProvider(ctx context.Context, res *resource.Resource) (func(context.Context) error, ExporterStatus, error) {
	status := MetricExporterStatus()
	if status != ExporterStatusEnabled {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		return nil, status, nil
	}

	metricExporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, status, err
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)

	return mp.Shutdown, status, nil
}
