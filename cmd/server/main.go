package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpserver "github.com/SigNoz/signoz-mcp-server/internal/mcp-server"
	"github.com/SigNoz/signoz-mcp-server/internal/telemetry"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.ValidateConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration validation failed: %v\n", err)
		os.Exit(1)
	}

	log, err := telemetry.NewLogger(telemetry.LogLevel(cfg.LogLevel))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	log.Info("Starting SigNoz MCP Server",
		zap.String("log_level", cfg.LogLevel),
		zap.String("transport_mode", cfg.TransportMode))

	// Initialize OpenTelemetry tracer. Configuration is driven by OTEL_*
	// environment variables (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SERVICE_NAME, etc.).
	shutdownTracer, err := telemetry.InitTracer(context.Background())
	if err != nil {
		log.Warn("Failed to initialize OpenTelemetry tracer, continuing without tracing", zap.Error(err))
	} else {
		defer func() {
			if err := shutdownTracer(context.Background()); err != nil {
				log.Error("Failed to shutdown tracer provider", zap.Error(err))
			}
		}()
		log.Info("OpenTelemetry tracer initialized successfully")
	}

	// Initialize OpenTelemetry meter provider for exporting HTTP metrics
	// (request duration, size, etc.) recorded by otelhttp middleware.
	shutdownMeter, err := telemetry.InitMeterProvider(context.Background())
	if err != nil {
		log.Warn("Failed to initialize OpenTelemetry meter provider, continuing without metrics export", zap.Error(err))
	} else {
		defer func() {
			if err := shutdownMeter(context.Background()); err != nil {
				log.Error("Failed to shutdown meter provider", zap.Error(err))
			}
		}()
		log.Info("OpenTelemetry meter provider initialized successfully")
	}

	handler := tools.NewHandler(log, cfg)

	dashboard.InitClickhouseSchema()

	if err := mcpserver.NewMCPServer(log, handler, cfg).Start(); err != nil {
		log.Fatal(fmt.Sprintf("Failed to start server: %v", err))
	}
}
