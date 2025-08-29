package mcp_server

import (
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
)

type MCPServer struct {
	logger  *zap.Logger
	handler *tools.Handler
}

func NewMCPServer(log *zap.Logger, handler *tools.Handler) *MCPServer {
	return &MCPServer{logger: log, handler: handler}
}

func (m *MCPServer) Start() error {
	s := server.NewMCPServer("SigNozMCP", "0.0.1", server.WithLogging(), server.WithToolCapabilities(false))

	m.logger.Info("Starting SigNoz MCP Server", zap.String("server_name", "SigNozMCPServer"))

	// Register all handlers
	m.handler.RegisterMetricsHandlers(s)
	m.handler.RegisterAlertsHandlers(s)
	m.handler.RegisterDashboardHandlers(s)
	m.handler.RegisterServiceHandlers(s)

	m.logger.Info("All handlers registered successfully")
	m.logger.Info("MCP Server ready, serving on stdio")

	return server.ServeStdio(s)
}
