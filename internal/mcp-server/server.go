package mcp_server

import (
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
)

type MCPServer struct {
	logger  *zap.Logger
	handler *tools.Handler
	config  *config.Config
}

func NewMCPServer(log *zap.Logger, handler *tools.Handler, cfg *config.Config) *MCPServer {
	return &MCPServer{logger: log, handler: handler, config: cfg}
}

func (m *MCPServer) Start() error {
	s := server.NewMCPServer("SigNozMCP", "0.0.1", server.WithLogging(), server.WithToolCapabilities(false))

	m.logger.Info("Starting SigNoz MCP Server",
		zap.String("server_name", "SigNozMCPServer"),
		zap.String("deployment_mode", m.config.DeploymentMode))

	// Register all handlers
	m.handler.RegisterMetricsHandlers(s)
	m.handler.RegisterAlertsHandlers(s)
	m.handler.RegisterDashboardHandlers(s)
	m.handler.RegisterServiceHandlers(s)
	m.handler.RegisterQueryBuilderV5Handlers(s)
	m.handler.RegisterLogsHandlers(s)

	m.logger.Info("All handlers registered successfully")

	if m.config.DeploymentMode == "cloud" {
		return m.startCloud(s)
	}
	return m.startLocal(s)
}

func (m *MCPServer) startLocal(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in LOCAL mode (stdio)")
	return server.ServeStdio(s)
}

func (m *MCPServer) startCloud(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in cloud hosted mode")

	addr := fmt.Sprintf(":%s", m.config.Port)

	mux := http.NewServeMux()

	httpServer := server.NewStreamableHTTPServer(s)
	mux.Handle("/mcp", httpServer)

	m.logger.Info("Listening for MCP clients",
		zap.String("addr", addr),
		zap.String("mcp_endpoint", "/mcp"))

	return http.ListenAndServe(addr, mux)
}
