package main

import (
	"fmt"
	"os"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/logger"
	mcpserver "github.com/SigNoz/signoz-mcp-server/internal/mcp-server"
)

func main() {
	logger, err := logger.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Sprintf("Failed to initialize logger: %v", err))
		os.Exit(1)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to load config: %v", err))
	}

	sigNozClient := client.NewClient(logger, cfg.URL, cfg.APIKey)
	handler := tools.NewHandler(logger, sigNozClient)

	if err := mcpserver.NewMCPServer(logger, handler).Start(); err != nil {
		logger.Fatal(fmt.Sprintf("Failed to start server: %v", err))
	}
}
