package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/logger"
	mcpserver "github.com/SigNoz/signoz-mcp-server/internal/mcp-server"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}

	if err := cfg.ValidateConfig(); err != nil {
		fmt.Fprintln(os.Stderr, fmt.Sprintf("Configuration validation failed: %v", err))
		os.Exit(1)
	}

	log, err := logger.NewLogger(logger.LogLevel(cfg.LogLevel))
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Sprintf("Failed to initialize logger: %v", err))
		os.Exit(1)
	}

	log.Info("Starting SigNoz MCP Server",
		zap.String("log_level", cfg.LogLevel),
		zap.String("transport_mode", cfg.TransportMode))

	sigNozClient := client.NewClient(log, cfg.URL, cfg.APIKey)
	handler := tools.NewHandler(log, sigNozClient)

	if err := mcpserver.NewMCPServer(log, handler, cfg).Start(); err != nil {
		log.Fatal(fmt.Sprintf("Failed to start server: %v", err))
	}
}
