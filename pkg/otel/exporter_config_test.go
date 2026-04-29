package otel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
)

func TestTraceExporterStatus(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want ExporterStatus
	}{
		{
			name: "not configured without exporter or endpoint env",
			want: ExporterStatusNotConfigured,
		},
		{
			name: "enabled by shared OTLP endpoint",
			env:  map[string]string{EnvExporterOTLPEndpoint: "http://localhost:4317"},
			want: ExporterStatusEnabled,
		},
		{
			name: "enabled by trace-specific OTLP endpoint",
			env:  map[string]string{EnvExporterOTLPTracesEndpoint: "http://localhost:4317"},
			want: ExporterStatusEnabled,
		},
		{
			name: "enabled by explicit trace exporter",
			env:  map[string]string{EnvTracesExporter: "otlp"},
			want: ExporterStatusEnabled,
		},
		{
			name: "none disables traces even when endpoint is configured",
			env: map[string]string{
				EnvExporterOTLPEndpoint: "http://localhost:4317",
				EnvTracesExporter:       " none ",
			},
			want: ExporterStatusDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearExporterEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			if got := TraceExporterStatus(); got != tt.want {
				t.Fatalf("TraceExporterStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMetricExporterStatus(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want ExporterStatus
	}{
		{
			name: "not configured without exporter or endpoint env",
			want: ExporterStatusNotConfigured,
		},
		{
			name: "enabled by shared OTLP endpoint",
			env:  map[string]string{EnvExporterOTLPEndpoint: "http://localhost:4317"},
			want: ExporterStatusEnabled,
		},
		{
			name: "enabled by metrics-specific OTLP endpoint",
			env:  map[string]string{EnvExporterOTLPMetricsEndpoint: "http://localhost:4317"},
			want: ExporterStatusEnabled,
		},
		{
			name: "enabled by explicit metrics exporter",
			env:  map[string]string{EnvMetricsExporter: "otlp"},
			want: ExporterStatusEnabled,
		},
		{
			name: "none disables metrics even when endpoint is configured",
			env: map[string]string{
				EnvExporterOTLPEndpoint: "http://localhost:4317",
				EnvMetricsExporter:      " none ",
			},
			want: ExporterStatusDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearExporterEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			if got := MetricExporterStatus(); got != tt.want {
				t.Fatalf("MetricExporterStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitProvidersSkipNetworkExportWhenNotConfigured(t *testing.T) {
	clearExporterEnv(t)

	shutdownTracer, traceStatus, err := InitTracerProvider(context.Background(), resource.Empty())
	if err != nil {
		t.Fatalf("InitTracerProvider() error = %v", err)
	}
	if shutdownTracer != nil {
		t.Fatalf("InitTracerProvider() returned a shutdown function, want nil")
	}
	if traceStatus != ExporterStatusNotConfigured {
		t.Fatalf("InitTracerProvider() status = %q, want %q", traceStatus, ExporterStatusNotConfigured)
	}

	shutdownMeter, metricStatus, err := InitMeterProvider(context.Background(), resource.Empty())
	if err != nil {
		t.Fatalf("InitMeterProvider() error = %v", err)
	}
	if shutdownMeter != nil {
		t.Fatalf("InitMeterProvider() returned a shutdown function, want nil")
	}
	if metricStatus != ExporterStatusNotConfigured {
		t.Fatalf("InitMeterProvider() status = %q, want %q", metricStatus, ExporterStatusNotConfigured)
	}
}

func TestInitProvidersHonorNoneExporter(t *testing.T) {
	clearExporterEnv(t)
	t.Setenv(EnvExporterOTLPEndpoint, "http://localhost:4317")
	t.Setenv(EnvTracesExporter, "none")
	t.Setenv(EnvMetricsExporter, "none")

	shutdownTracer, traceStatus, err := InitTracerProvider(context.Background(), resource.Empty())
	if err != nil {
		t.Fatalf("InitTracerProvider() error = %v", err)
	}
	if shutdownTracer != nil {
		t.Fatalf("InitTracerProvider() returned a shutdown function, want nil")
	}
	if traceStatus != ExporterStatusDisabled {
		t.Fatalf("InitTracerProvider() status = %q, want %q", traceStatus, ExporterStatusDisabled)
	}

	shutdownMeter, metricStatus, err := InitMeterProvider(context.Background(), resource.Empty())
	if err != nil {
		t.Fatalf("InitMeterProvider() error = %v", err)
	}
	if shutdownMeter != nil {
		t.Fatalf("InitMeterProvider() returned a shutdown function, want nil")
	}
	if metricStatus != ExporterStatusDisabled {
		t.Fatalf("InitMeterProvider() status = %q, want %q", metricStatus, ExporterStatusDisabled)
	}
}

func clearExporterEnv(t *testing.T) {
	t.Helper()

	for _, env := range []string{
		EnvExporterOTLPEndpoint,
		EnvExporterOTLPTracesEndpoint,
		EnvExporterOTLPMetricsEndpoint,
		EnvTracesExporter,
		EnvMetricsExporter,
	} {
		t.Setenv(env, "")
	}
}
