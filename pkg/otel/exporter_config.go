package otel

import (
	"os"
	"strings"
)

const (
	EnvExporterOTLPEndpoint        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	EnvExporterOTLPTracesEndpoint  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	EnvExporterOTLPMetricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	EnvTracesExporter              = "OTEL_TRACES_EXPORTER"
	EnvMetricsExporter             = "OTEL_METRICS_EXPORTER"
)

type ExporterStatus string

const (
	ExporterStatusEnabled       ExporterStatus = "enabled"
	ExporterStatusDisabled      ExporterStatus = "disabled"
	ExporterStatusNotConfigured ExporterStatus = "not_configured"
)

func TraceExporterStatus() ExporterStatus {
	return exporterStatus(EnvTracesExporter, EnvExporterOTLPTracesEndpoint)
}

func MetricExporterStatus() ExporterStatus {
	return exporterStatus(EnvMetricsExporter, EnvExporterOTLPMetricsEndpoint)
}

func exporterStatus(exporterEnv, signalEndpointEnv string) ExporterStatus {
	exporter := strings.ToLower(strings.TrimSpace(os.Getenv(exporterEnv)))
	if exporter == "none" {
		return ExporterStatusDisabled
	}
	if exporter != "" {
		return ExporterStatusEnabled
	}
	if envIsSet(signalEndpointEnv) || envIsSet(EnvExporterOTLPEndpoint) {
		return ExporterStatusEnabled
	}
	return ExporterStatusNotConfigured
}

func envIsSet(name string) bool {
	return strings.TrimSpace(os.Getenv(name)) != ""
}
