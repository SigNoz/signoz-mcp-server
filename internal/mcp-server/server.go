package mcp_server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const (
	serverName    = "SigNozMCP"
	serverVersion = "0.0.1"
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
	s := server.NewMCPServer(serverName, serverVersion, server.WithLogging(), server.WithToolCapabilities(false))

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
		// Extract Authorization header
		authHeader := r.Header.Get("Authorization")

		var apiKey string
		if authHeader != "" {
			// Support both "Bearer <token>" and raw token formats
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				apiKey = authHeader
			}

			// Store API key in request context
			ctx := util.SetAPIKey(r.Context(), apiKey)
			r = r.WithContext(ctx)

			m.logger.Debug("API key extracted from Authorization header")
		} else if m.config.APIKey != "" {
			// Fallback to config API key if no Authorization header
			ctx := util.SetAPIKey(r.Context(), m.config.APIKey)
			r = r.WithContext(ctx)

			m.logger.Debug("Using API key from environment config")
		} else {
			m.logger.Warn("No API key found in Authorization header or environment")
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (m *MCPServer) startHTTP(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in HTTP mode")

	addr := fmt.Sprintf(":%s", m.config.Port)

	mux := http.NewServeMux()

	// Health check endpoint for connectivity verification
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		m.logger.Debug("Health check requested")
		response := map[string]string{
			"status":       "ok",
			"server":       serverName,
			"version":      serverVersion,
			"mcp_endpoint": "/mcp",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			m.logger.Error("Failed to encode health response", zap.Error(err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})

	// Root endpoint for discoverability
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		m.logger.Debug("Root endpoint requested")
		response := map[string]string{
			"name":         serverName,
			"version":      serverVersion,
			"mcp_endpoint": "/mcp",
			"health":       "/health",
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			m.logger.Error("Failed to encode root response", zap.Error(err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	})

	httpServer := server.NewStreamableHTTPServer(s)
	mux.Handle("/mcp", m.authMiddleware(httpServer))

	m.logger.Info("Listening for MCP clients",
		zap.String("addr", addr),
		zap.String("mcp_endpoint", "/mcp"))

	return http.ListenAndServe(addr, mux)
}
