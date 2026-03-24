package mcp_server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/instructions"
	"github.com/SigNoz/signoz-mcp-server/pkg/prompts"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
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
	s := server.NewMCPServer("SigNozMCP", "0.0.1",
		server.WithLogging(),
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions(instructions.ServerInstructions),
		server.WithToolHandlerMiddleware(m.loggingMiddleware()),
		server.WithHooks(m.buildHooks()),
	)

	m.logger.Info("Starting SigNoz MCP Server",
		zap.String("server_name", "SigNozMCPServer"),
		zap.String("transport_mode", m.config.TransportMode))

	// Register all handlers
	m.handler.RegisterMetricsHandlers(s)
	m.handler.RegisterFieldsHandlers(s)
	m.handler.RegisterAlertsHandlers(s)
	m.handler.RegisterDashboardHandlers(s)
	m.handler.RegisterServiceHandlers(s)
	m.handler.RegisterQueryBuilderV5Handlers(s)
	m.handler.RegisterLogsHandlers(s)
	m.handler.RegisterTracesHandlers(s)
	m.handler.RegisterResourceTemplates(s)

	// Register prompts
	prompts.RegisterPrompts(s.AddPrompt)

	m.logger.Info("All handlers registered successfully")

	if m.config.TransportMode == "http" {
		return m.startHTTP(s)
	}
	return m.startStdio(s)
}

// buildHooks returns lifecycle hooks for observability.
func (m *MCPServer) buildHooks() *server.Hooks {
	hooks := &server.Hooks{}
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		m.logger.Debug("mcp request", zap.String("method", string(method)))
	})
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		m.logger.Error("mcp error", zap.String("method", string(method)), zap.Error(err))
	})
	hooks.AddOnRegisterSession(func(ctx context.Context, session server.ClientSession) {
		m.logger.Info("mcp session registered")
	})
	hooks.AddOnUnregisterSession(func(ctx context.Context, session server.ClientSession) {
		m.logger.Info("mcp session unregistered")
	})
	return hooks
}

// loggingMiddleware returns a tool handler middleware that logs tool call
// start/finish with duration and tool name.
func (m *MCPServer) loggingMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			m.logger.Debug("tool call started", zap.String("tool", req.Params.Name))
			result, err := next(ctx, req)
			m.logger.Debug("tool call finished",
				zap.String("tool", req.Params.Name),
				zap.Duration("duration", time.Since(start)),
				zap.Bool("error", err != nil))
			return result, err
		}
	}
}

func (m *MCPServer) startStdio(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in stdio mode")

	// Inject env-configured credentials into every request context
	// so that GetClient works uniformly across both transports.
	ctxFunc := server.WithStdioContextFunc(func(ctx context.Context) context.Context {
		ctx = util.SetAPIKey(ctx, m.config.APIKey)
		ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
		ctx = util.SetSigNozURL(ctx, m.config.URL)
		return ctx
	})

	return server.ServeStdio(s, ctxFunc)
}

func (m *MCPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Extract X-SigNoz-URL custom header (takes precedence over JWT audience)
		customURL := r.Header.Get("X-SigNoz-URL")

		// Check for auth credentials from headers.
		// Clients can provide either:
		//   - SIGNOZ-API-KEY: <pat-token>
		//   - Authorization: Bearer <token>  (JWT or PAT)
		//   - Authorization: <token>         (legacy)
		signozAPIKey := r.Header.Get("SIGNOZ-API-KEY")
		authHeader := r.Header.Get("Authorization")

		var apiKey string
		var signozURL string

		if signozAPIKey != "" {
			// Explicit PAT via SIGNOZ-API-KEY header — forward as-is.
			apiKey = strings.TrimPrefix(signozAPIKey, "Bearer ")

			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			m.logger.Debug("Using SIGNOZ-API-KEY header for auth")
		} else if authHeader != "" {
			// Strip "Bearer " prefix if present to get the raw token.
			token := strings.TrimPrefix(authHeader, "Bearer ")

			apiKey = "Bearer " + token
			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "Authorization")
			m.logger.Debug("Using JWT token authentication via Authorization header")

		} else if m.config.APIKey != "" {
			// Fallback to config API key
			apiKey = m.config.APIKey
			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			m.logger.Debug("Using API key from environment config")
		} else {
			m.logger.Warn("No API key found in headers or environment")
			http.Error(w, "Authorization or SIGNOZ-API-KEY header required", http.StatusUnauthorized)
			return
		}

		// Determine final URL with precedence: X-SigNoz-URL header > config URL
		if customURL != "" {
			trimmed := strings.TrimSuffix(customURL, "/")
			normalized, err := normalizeSigNozURL(trimmed)
			if err != nil {
				m.logger.Warn("Invalid X-SigNoz-URL header",
					zap.String("url", customURL), zap.Error(err))
				http.Error(w, fmt.Sprintf("Invalid X-SigNoz-URL: %v", err), http.StatusBadRequest)
				return
			}
			signozURL = normalized
			m.logger.Debug("Using URL from X-SigNoz-URL header", zap.String("url", signozURL))
		} else if m.config.URL != "" {
			signozURL = m.config.URL
			m.logger.Debug("Using URL from environment config", zap.String("url", signozURL))
		} else {
			m.logger.Warn("No SigNoz URL found in X-SigNoz-URL header or environment")
			http.Error(w, "SigNoz instance URL is required", http.StatusBadRequest)
			return
		}

		ctx = util.SetSigNozURL(ctx, signozURL)

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func (m *MCPServer) startHTTP(s *server.MCPServer) error {
	m.logger.Info("MCP Server running in HTTP mode")

	addr := fmt.Sprintf(":%s", m.config.Port)

	mux := http.NewServeMux()

	// Health check endpoint — no auth required so that Kubernetes
	// probes and load balancers can reach it.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	httpServer := server.NewStreamableHTTPServer(s)
	mux.Handle("/mcp", m.authMiddleware(httpServer))

	m.logger.Info("Listening for MCP clients",
		zap.String("addr", addr),
		zap.String("mcp_endpoint", "/mcp"))

	// Wrap the entire mux with OpenTelemetry HTTP instrumentation to
	// automatically create spans for every inbound request.
	handler := otelhttp.NewHandler(mux, "signoz-mcp-server")

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout and IdleTimeout are intentionally left at 0 (no timeout)
		// because MCP uses long-lived SSE connections for streaming responses.
		// Setting these would prematurely kill active MCP sessions.
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	return srv.ListenAndServe()
}

// normalizeSigNozURL validates that rawURL is safe to use as a SigNoz backend
// target and returns the canonical origin form (scheme://host[:port]).
// It blocks non-HTTP schemes, private/reserved IPs, and localhost to prevent
// SSRF. Default ports (80 for http, 443 for https) are stripped so that
// equivalent URLs like https://example.com and https://example.com:443
// produce the same origin string for caching.
func normalizeSigNozURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("malformed URL: %w", err)
	}

	// Only allow http and https schemes
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme %q not allowed, must be http or https", parsed.Scheme)
	}

	// Reject URLs that contain path, query, or fragment components.
	// Users may copy a full UI URL like https://tenant.example.com/dashboard/123?orgId=1
	// but baseURL must be an origin only (scheme + host + optional port).
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without a path, got path %q", parsed.Path)
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without query parameters")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("URL must be an origin (scheme://host[:port]) without a fragment")
	}

	host := parsed.Hostname()

	if host == "" {
		return "", fmt.Errorf("URL must include a host")
	}

	// Lowercase the host for consistent comparison.
	host = strings.ToLower(host)

	// Block localhost variants
	if host == "localhost" || host == "0.0.0.0" || host == "[::]" {
		return "", fmt.Errorf("host %q is not allowed", host)
	}

	// Build the canonical origin. Strip default ports so that
	// https://example.com and https://example.com:443 hash identically.
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	origin := scheme + "://" + host
	if port != "" {
		origin += ":" + port
	}

	return origin, nil
}
