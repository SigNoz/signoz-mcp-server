package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpserver "github.com/SigNoz/signoz-mcp-server/internal/mcp-server"
	"github.com/SigNoz/signoz-mcp-server/internal/telemetry"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/segmentanalytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	res, err := otelpkg.NewResource(context.Background(), version.Version)
	if err != nil {
		log.Error("Failed to initialize OpenTelemetry resource", zap.Error(err))
		os.Exit(1)
	}

	shutdownTracer, err := otelpkg.InitTracerProvider(context.Background(), res)
	if err != nil {
		log.Warn("Failed to initialize OpenTelemetry tracer, continuing without tracing", zap.Error(err))
	} else {
		log.Info("OpenTelemetry tracer initialized successfully")
	}

	shutdownMeter, err := otelpkg.InitMeterProvider(context.Background(), res)
	if err != nil {
		log.Warn("Failed to initialize OpenTelemetry meter provider, continuing without metrics export", zap.Error(err))
	} else {
		log.Info("OpenTelemetry meter provider initialized successfully")
	}

	if err := otelruntime.Start(); err != nil {
		log.Warn("Failed to initialize OpenTelemetry runtime metrics", zap.Error(err))
	}

	var analyticsInstance analytics.Analytics
	if cfg.AnalyticsEnabled && cfg.SegmentKey != "" {
		analyticsInstance, err = segmentanalytics.New(log, analytics.Config{
			Enabled: true,
			Segment: analytics.SegmentConfig{Key: cfg.SegmentKey},
		})
		if err != nil {
			log.Warn("Failed to initialize Segment analytics, continuing without analytics", zap.Error(err))
			analyticsInstance = noopanalytics.New()
		}
	} else {
		analyticsInstance = noopanalytics.New()
	}

	handler := tools.NewHandler(log, cfg)

	dashboard.InitClickhouseSchema()

	srv := mcpserver.NewMCPServer(log, handler, cfg, analyticsInstance)

	runGroup, runCtx := errgroup.WithContext(ctx)
	runGroup.Go(func() error {
		return srv.Run(runCtx)
	})

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- runGroup.Wait()
	}()

	var runErr error
	select {
	case runErr = <-serverErrCh:
	case <-ctx.Done():
		log.Info("Received shutdown signal")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var shutdownErr error
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("Failed to shutdown server", zap.Error(err))
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if err := srv.WaitForAnalytics(shutCtx); err != nil {
		log.Error("Failed while waiting for analytics dispatches", zap.Error(err))
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if err := analyticsInstance.Stop(shutCtx); err != nil {
		log.Error("Failed to stop analytics", zap.Error(err))
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if shutdownMeter != nil {
		if err := shutdownMeter(shutCtx); err != nil {
			log.Error("Failed to shutdown meter provider", zap.Error(err))
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	if shutdownTracer != nil {
		if err := shutdownTracer(shutCtx); err != nil {
			log.Error("Failed to shutdown tracer provider", zap.Error(err))
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}

	if runErr == nil {
		runErr = <-serverErrCh
	}

	if runErr != nil || shutdownErr != nil {
		log.Error("Server exited with errors", zap.Error(errors.Join(runErr, shutdownErr)))
		os.Exit(1)
	}
}
