package mcp_server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
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
		zap.String("transport_mode", m.config.TransportMode))

	// Register all handlers
	m.handler.RegisterMetricsHandlers(s)
	m.handler.RegisterAlertsHandlers(s)
	m.handler.RegisterDashboardHandlers(s)
	m.handler.RegisterServiceHandlers(s)
	m.handler.RegisterQueryBuilderV5Handlers(s)
	m.handler.RegisterLogsHandlers(s)
	m.handler.RegisterTracesHandlers(s)

	m.logger.Info("All handlers registered successfully")

	if m.config.TransportMode == "http" {
		return m.startHTTP(s)
	}
	return m.startStdio(s)
}

func (m *MCPServer) startStdio(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in stdio mode")
	return server.ServeStdio(s)
}

func (m *MCPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Extract X-SigNoz-URL custom header (takes precedence over JWT audience)
		customURL := r.Header.Get("X-SigNoz-URL")

		// Extract Authorization header
		authHeader := r.Header.Get("Authorization")

		var apiKey string
		var signozURL string

		if authHeader != "" {
			// Support both "Bearer <token>" and raw token formats
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				apiKey = authHeader
			}

			// Store API key in request context
			ctx = util.SetAPIKey(ctx, apiKey)
			m.logger.Debug("API key extracted from Authorization header")

		} else if m.config.APIKey != "" {
			// Fallback to config API key if no Authorization header
			ctx = util.SetAPIKey(ctx, m.config.APIKey)
			m.logger.Debug("Using API key from environment config")
		} else {
			m.logger.Warn("No API key found in Authorization header or environment")
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		// Determine final URL with precedence: X-SigNoz-URL header > config URL
		if customURL != "" {
			signozURL = strings.TrimSuffix(customURL, "/")
			m.logger.Debug("Using URL from X-SigNoz-URL header", zap.String("url", signozURL))
		} else if signozURL == "" && m.config.URL != "" {
			signozURL = m.config.URL
			m.logger.Debug("Using URL from environment config", zap.String("url", signozURL))
		} else {
			m.logger.Warn("No SigNoz URL found in X-SigNoz-URL header or environment")
			http.Error(w, "SigNoz instance URL is required", http.StatusBadRequest)
			return
		}

		// Store URL in context if available
		if signozURL != "" {
			ctx = util.SetSigNozURL(ctx, signozURL)
		}

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func (m *MCPServer) startHTTP(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in HTTP mode")

	addr := fmt.Sprintf(":%s", m.config.Port)

	mux := http.NewServeMux()

	httpServer := server.NewStreamableHTTPServer(s)
	mux.Handle("/mcp", m.authMiddleware(httpServer))

	m.logger.Info("Listening for MCP clients",
		zap.String("addr", addr),
		zap.String("mcp_endpoint", "/mcp"))

	return http.ListenAndServe(addr, mux)
}
