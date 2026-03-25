package mcp_server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
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
// start/finish with duration, tool name, session ID, and search context.
func (m *MCPServer) loggingMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()

			// Extract session ID from mcp-go client session.
			if session := server.ClientSessionFromContext(ctx); session != nil {
				ctx = util.SetSessionID(ctx, session.SessionID())
			}

			// Extract searchContext from tool arguments (LLM-provided).
			if args, ok := req.Params.Arguments.(map[string]any); ok {
				if sc, ok := args["searchContext"].(string); ok && sc != "" {
					ctx = util.SetSearchContext(ctx, sc)
				}
			}

			fields := []zap.Field{zap.String("tool", req.Params.Name)}
			if sid, ok := util.GetSessionID(ctx); ok && sid != "" {
				fields = append(fields, zap.String("session_id", sid))
			}
			if sc, ok := util.GetSearchContext(ctx); ok && sc != "" {
				fields = append(fields, zap.String("search_context", sc))
			}

			m.logger.Debug("tool call started", fields...)
			result, err := next(ctx, req)
			m.logger.Debug("tool call finished",
				append(fields,
					zap.Duration("duration", time.Since(start)),
					zap.Bool("error", err != nil))...)
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

func isJWTToken(token string) bool {
	return strings.Count(token, ".") == 2 && strings.HasPrefix(token, "eyJ")
}

func (m *MCPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Extract X-SigNoz-URL custom header (takes precedence over JWT audience)
		customURL := r.Header.Get("X-SigNoz-URL")

		// Check for auth credentials from headers.
		// Clients can provide either:
		//   - SIGNOZ-API-KEY: <pat-token>
		//   - Authorization: Bearer <token>  (JWT, PAT)
		//   - Authorization: <token>         (legacy)
		signozAPIKey := r.Header.Get("SIGNOZ-API-KEY")
		authHeader := r.Header.Get("Authorization")

		var apiKey string
		var signozURL string
		var usedOAuthToken bool

		if signozAPIKey != "" {
			// Explicit PAT via SIGNOZ-API-KEY header — forward as-is.
			apiKey = strings.TrimPrefix(signozAPIKey, "Bearer ")

			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			m.logger.Debug("Using SIGNOZ-API-KEY header for auth")
		} else if authHeader != "" {
			token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			if customURL != "" {
				if isJWTToken(token) {
					// JWT token — forward via Authorization: Bearer <token>
					apiKey = "Bearer " + token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "Authorization")
					m.logger.Debug("Using JWT token authentication via Authorization header")
				} else {
					// PAT token — forward via SIGNOZ-API-KEY
					apiKey = token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
					m.logger.Debug("Using API KEY token authentication via SIGNOZ-API-KEY header")
				}
			} else if m.config.OAuthEnabled {
				decryptedAPIKey, decryptedURL, _, _, err := oauth.DecryptToken(token, []byte(m.config.OAuthTokenSecret))
				switch {
				case err == nil:
					apiKey = decryptedAPIKey
					signozURL = decryptedURL
					usedOAuthToken = true
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
					m.logger.Debug("OAuth access token extracted from Authorization header")
				case errors.Is(err, oauth.ErrExpiredToken):
					m.setOAuthChallenge(w, `error="invalid_token", error_description="access token expired"`)
					http.Error(w, "OAuth access token expired", http.StatusUnauthorized)
					return
				default:
					// Only fall back to legacy raw API key mode when the request also
					// carries an explicit SigNoz URL (header or config). Otherwise a
					// stale bearer token can mask the OAuth challenge flow.
					if customURL == "" && m.config.URL == "" {
						m.logger.Warn("Bearer token did not match OAuth token format and no SigNoz URL is available for legacy fallback")
						m.setOAuthChallenge(w, `error="invalid_token", error_description="access token is invalid"`)
						http.Error(w, "OAuth access token is invalid", http.StatusUnauthorized)
						return
					}
					apiKey = token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
					m.logger.Debug("Bearer token did not match OAuth token format, falling back to raw API key")
				}
			} else {
				apiKey = token
				ctx = util.SetAPIKey(ctx, apiKey)
				ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
				m.logger.Debug("Using API KEY token authentication via SIGNOZ-API-KEY header")
			}

		} else if m.config.APIKey != "" {
			// Fallback to config API key
			apiKey = m.config.APIKey
			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			m.logger.Debug("Using API key from environment config")
		} else {
			m.logger.Warn("No API key found in headers or environment")
			if m.config.OAuthEnabled {
				m.setOAuthChallenge(w, "")
			}
			http.Error(w, "Authorization or SIGNOZ-API-KEY header required", http.StatusUnauthorized)
			return
		}

		if usedOAuthToken {
			ctx = util.SetSigNozURL(ctx, signozURL)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}

		// Determine final URL with precedence: X-SigNoz-URL header > config URL
		if customURL != "" {
			trimmed := strings.TrimSuffix(customURL, "/")
			normalized, err := util.NormalizeSigNozURL(trimmed)
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

	if m.config.OAuthEnabled {
		oauthHandler := oauth.NewHandler(m.logger, m.config)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", oauthHandler.HandleProtectedResourceMetadata)
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauthHandler.HandleAuthorizationServerMetadata)
		mux.HandleFunc("POST /oauth/register", oauthHandler.HandleRegisterClient)
		mux.HandleFunc("GET /oauth/authorize", oauthHandler.HandleAuthorizePage)
		mux.HandleFunc("POST /oauth/authorize", oauthHandler.HandleAuthorizeSubmit)
		mux.HandleFunc("POST /oauth/token", oauthHandler.HandleToken)
	}

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

func (m *MCPServer) setOAuthChallenge(w http.ResponseWriter, extra string) {
	if !m.config.OAuthEnabled {
		return
	}

	resourceMetadata := m.oauthResourceMetadataURL()
	if extra == "" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, resourceMetadata))
		return
	}

	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer %s, resource_metadata="%s"`, extra, resourceMetadata))
}

func (m *MCPServer) oauthResourceMetadataURL() string {
	return strings.TrimSuffix(m.config.OAuthIssuerURL, "/") + "/.well-known/oauth-protected-resource"
}
