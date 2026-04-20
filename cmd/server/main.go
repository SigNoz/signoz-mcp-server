package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpserver "github.com/SigNoz/signoz-mcp-server/internal/mcp-server"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/noopanalytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics/segmentanalytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/dashboard"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
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

	logger := logpkg.New(cfg.LogLevel)
	logger.InfoContext(ctx, "Starting SigNoz MCP Server",
		slog.String("log_level", cfg.LogLevel),
		slog.String("transport_mode", cfg.TransportMode))

	res, err := otelpkg.NewResource(ctx, version.Version)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to initialize OpenTelemetry resource", logpkg.ErrAttr(err))
		os.Exit(1)
	}

	shutdownTracer, err := otelpkg.InitTracerProvider(ctx, res)
	if err != nil {
		logger.WarnContext(ctx, "Failed to initialize OpenTelemetry tracer, continuing without tracing", logpkg.ErrAttr(err))
	} else {
		logger.InfoContext(ctx, "OpenTelemetry tracer initialized successfully")
	}

	shutdownMeter, err := otelpkg.InitMeterProvider(ctx, res)
	if err != nil {
		logger.WarnContext(ctx, "Failed to initialize OpenTelemetry meter provider, continuing without metrics export", logpkg.ErrAttr(err))
	} else {
		logger.InfoContext(ctx, "OpenTelemetry meter provider initialized successfully")
	}

	if err := otelruntime.Start(); err != nil {
		logger.WarnContext(ctx, "Failed to initialize OpenTelemetry runtime metrics", logpkg.ErrAttr(err))
	}

	meters, err := otelpkg.NewMeters(otel.GetMeterProvider())
	if err != nil {
		logger.ErrorContext(ctx, "Failed to initialize custom OpenTelemetry meters", logpkg.ErrAttr(err))
		os.Exit(1)
	}

	var analyticsInstance analytics.Analytics
	if cfg.AnalyticsEnabled && cfg.SegmentKey != "" {
		analyticsInstance, err = segmentanalytics.New(logger, analytics.Config{
			Enabled: true,
			Segment: analytics.SegmentConfig{Key: cfg.SegmentKey},
		})
		if err != nil {
			logger.WarnContext(ctx, "Failed to initialize Segment analytics, continuing without analytics", logpkg.ErrAttr(err))
			analyticsInstance = noopanalytics.New()
		}
	} else {
		analyticsInstance = noopanalytics.New()
	}

	handler := tools.NewHandler(logger, cfg)

	dashboard.InitClickhouseSchema()

	srv := mcpserver.NewMCPServer(logger, handler, cfg, analyticsInstance, meters)

	runGroup, runCtx := errgroup.WithContext(ctx)
	runGroup.Go(func() error {
		return srv.Run(runCtx)
	})

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- runGroup.Wait()
	}()

	var (
		runErr     error
		serverDone bool
	)
	select {
	case runErr = <-serverErrCh:
		// Server exited (cleanly or with an error) before a signal arrived.
		// The channel is now drained; do not receive from it again.
		serverDone = true
	case <-ctx.Done():
		logger.InfoContext(ctx, "Received shutdown signal")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var shutdownErr error
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.ErrorContext(ctx, "Failed to shutdown server", logpkg.ErrAttr(err))
		shutdownErr = errors.Join(shutdownErr, err)
	}

	// Join the server goroutine BEFORE draining analytics. Otherwise, a tool
	// call that is still executing (stdio is especially prone to this, since
	// srv.Shutdown is a no-op there) can call dispatchAnalytics and
	// analyticsWG.Add while WaitForAnalytics is running — the sync.WaitGroup
	// misuse contract forbids Add concurrent with a Wait at counter zero,
	// and any late dispatches would otherwise race analyticsInstance.Stop.
	// Bounded by shutCtx so a stuck stdio reader cannot hold the process
	// past the shutdown budget.
	if !serverDone {
		select {
		case err := <-serverErrCh:
			runErr = err
		case <-shutCtx.Done():
			logger.WarnContext(ctx, "Timed out waiting for server goroutine to exit", logpkg.ErrAttr(shutCtx.Err()))
		}
	}

	if err := srv.WaitForAnalytics(shutCtx); err != nil {
		logger.ErrorContext(ctx, "Failed while waiting for analytics dispatches", logpkg.ErrAttr(err))
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if err := analyticsInstance.Stop(shutCtx); err != nil {
		logger.ErrorContext(ctx, "Failed to stop analytics", logpkg.ErrAttr(err))
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if shutdownMeter != nil {
		if err := shutdownMeter(shutCtx); err != nil {
			logger.ErrorContext(ctx, "Failed to shutdown meter provider", logpkg.ErrAttr(err))
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	if shutdownTracer != nil {
		if err := shutdownTracer(shutCtx); err != nil {
			logger.ErrorContext(ctx, "Failed to shutdown tracer provider", logpkg.ErrAttr(err))
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}

	if runErr != nil || shutdownErr != nil {
		logger.ErrorContext(ctx, "Server exited with errors", logpkg.ErrAttr(errors.Join(runErr, shutdownErr)))
		os.Exit(1)
	}
}
