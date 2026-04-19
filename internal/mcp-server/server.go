package mcp_server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/instructions"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/prompts"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Streamable HTTP only fires OnUnregisterSession for the long-lived GET
// listener — POST-only clients never trigger explicit cleanup — so the
// session → ClientInfo map needs bounded eviction.
const (
	sessionClientCacheSize = 1024
	sessionClientCacheTTL  = 30 * time.Minute
)

type MCPServer struct {
	logger         *zap.Logger
	handler        *tools.Handler
	config         *config.Config
	analytics      analytics.Analytics
	sessionClients *expirable.LRU[string, mcp.Implementation]
	httpSrv        *http.Server
	analyticsWG    sync.WaitGroup
}

func (m *MCPServer) rememberClientInfo(sessionID string, info mcp.Implementation) {
	if sessionID == "" || info.Name == "" || m.sessionClients == nil {
		return
	}
	m.sessionClients.Add(sessionID, info)
}

// lookupClientInfo re-Adds on hit so the TTL behaves as a sliding window
// keyed on last use, keeping long-running sessions from losing attribution.
func (m *MCPServer) lookupClientInfo(sessionID string) mcp.Implementation {
	if sessionID == "" || m.sessionClients == nil {
		return mcp.Implementation{}
	}
	v, ok := m.sessionClients.Get(sessionID)
	if !ok {
		return mcp.Implementation{}
	}
	m.sessionClients.Add(sessionID, v)
	return v
}

func (m *MCPServer) forgetClientInfo(sessionID string) {
	if sessionID == "" || m.sessionClients == nil {
		return
	}
	m.sessionClients.Remove(sessionID)
}

func (m *MCPServer) attachClientInfo(props map[string]any, sessionID string) {
	info := m.lookupClientInfo(sessionID)
	if info.Name == "" {
		return
	}
	props[analytics.AttrClientName] = info.Name
	if info.Version != "" {
		props[analytics.AttrClientVersion] = info.Version
	}
}

const analyticsAsyncTimeout = 5 * time.Second

func (m *MCPServer) analyticsEnabled() bool {
	return m.analytics != nil && m.analytics.Enabled()
}

func cloneAttrs(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (m *MCPServer) mergeIdentityAttrs(identity *signozclient.AnalyticsIdentity, attrs map[string]any) map[string]any {
	merged := cloneAttrs(attrs)
	if identity == nil {
		return merged
	}
	merged[analytics.AttrOrgID] = identity.OrgID
	merged[analytics.AttrPrincipal] = identity.Principal
	// name and email are Segment reserved traits; user vs service_account is
	// disambiguated via the principal attr rather than key prefixes.
	if identity.Name != "" {
		merged[analytics.AttrName] = identity.Name
	}
	if identity.Email != "" {
		merged[analytics.AttrEmail] = identity.Email
	}
	return merged
}

func (m *MCPServer) resolveIdentity(ctx context.Context) (*signozclient.AnalyticsIdentity, error) {
	if m.handler == nil {
		return nil, errors.New("analytics identity resolution requires a handler")
	}

	client, err := m.handler.GetClient(ctx)
	if err != nil {
		return nil, err
	}

	return client.GetAnalyticsIdentity(ctx)
}

func (m *MCPServer) identifyAsync(ctx context.Context, traits map[string]any) {
	if !m.analyticsEnabled() {
		return
	}

	traits = cloneAttrs(traits)
	m.dispatchAnalytics(ctx, func(detachedCtx context.Context) {
		identity, err := m.resolveIdentity(detachedCtx)
		if err != nil {
			m.logger.Warn("analytics identity resolution failed; skipping identify",
				append(m.analyticsLogFields(detachedCtx), zap.Error(err))...)
			return
		}

		m.analytics.IdentifyUser(detachedCtx, identity.OrgID, identity.UserID, m.mergeIdentityAttrs(identity, traits))
	})
}

func (m *MCPServer) trackEventAsync(ctx context.Context, event string, properties map[string]any) {
	if !m.analyticsEnabled() {
		return
	}

	properties = cloneAttrs(properties)
	m.dispatchAnalytics(ctx, func(detachedCtx context.Context) {
		identity, err := m.resolveIdentity(detachedCtx)
		if err != nil {
			m.logger.Warn("analytics identity resolution failed; skipping track",
				append(m.analyticsLogFields(detachedCtx), zap.String("event", event), zap.Error(err))...)
			return
		}

		m.analytics.TrackUser(detachedCtx, identity.OrgID, identity.UserID, event, m.mergeIdentityAttrs(identity, properties))
	})
}

// identifyAndTrackAsync resolves identity once and emits both calls under
// the same goroutine to avoid a second /me roundtrip.
func (m *MCPServer) identifyAndTrackAsync(ctx context.Context, event string, traits map[string]any, properties map[string]any) {
	if !m.analyticsEnabled() {
		return
	}

	traits = cloneAttrs(traits)
	properties = cloneAttrs(properties)
	m.dispatchAnalytics(ctx, func(detachedCtx context.Context) {
		identity, err := m.resolveIdentity(detachedCtx)
		if err != nil {
			m.logger.Warn("analytics identity resolution failed; skipping identify+track",
				append(m.analyticsLogFields(detachedCtx), zap.String("event", event), zap.Error(err))...)
			return
		}

		m.analytics.IdentifyUser(detachedCtx, identity.OrgID, identity.UserID, m.mergeIdentityAttrs(identity, traits))
		m.analytics.TrackUser(detachedCtx, identity.OrgID, identity.UserID, event, m.mergeIdentityAttrs(identity, properties))
	})
}

// trackOAuthEvent seeds tenant credentials on ctx so the async identity
// lookup can run even though the OAuth HTTP request carried them in form
// fields or an encrypted grant, not in util-context.
func (m *MCPServer) trackOAuthEvent(ctx context.Context, event, apiKey, signozURL string, props map[string]any) {
	if apiKey != "" {
		ctx = util.SetAPIKey(ctx, apiKey)
		ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	}
	if signozURL != "" {
		ctx = util.SetSigNozURL(ctx, signozURL)
	}
	m.trackEventAsync(ctx, event, props)
}

func (m *MCPServer) analyticsLogFields(ctx context.Context) []zap.Field {
	fields := []zap.Field{}
	if sid, ok := util.GetSessionID(ctx); ok && sid != "" {
		fields = append(fields, zap.String("mcp.session.id", sid))
	}
	if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
		fields = append(fields, zap.String("mcp.tenant_url", signozURL))
	}
	return fields
}

// detachedAnalyticsContext roots the async goroutine off context.Background
// (so it survives the parent request) while copying forward the credentials,
// tenant/session fields, and span context the identity lookup needs.
func (m *MCPServer) detachedAnalyticsContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), analyticsAsyncTimeout)

	if apiKey, ok := util.GetAPIKey(parent); ok && apiKey != "" {
		ctx = util.SetAPIKey(ctx, apiKey)
	}
	if authHeader, ok := util.GetAuthHeader(parent); ok && authHeader != "" {
		ctx = util.SetAuthHeader(ctx, authHeader)
	}
	if signozURL, ok := util.GetSigNozURL(parent); ok && signozURL != "" {
		ctx = util.SetSigNozURL(ctx, signozURL)
	}
	if searchContext, ok := util.GetSearchContext(parent); ok && searchContext != "" {
		ctx = util.SetSearchContext(ctx, searchContext)
	}
	if sessionID, ok := util.GetSessionID(parent); ok && sessionID != "" {
		ctx = util.SetSessionID(ctx, sessionID)
	}
	if spanCtx := trace.SpanContextFromContext(parent); spanCtx.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, spanCtx)
	}

	return ctx, cancel
}

func (m *MCPServer) dispatchAnalytics(parent context.Context, fn func(context.Context)) {
	ctx, cancel := m.detachedAnalyticsContext(parent)
	m.analyticsWG.Add(1)
	go func() {
		defer m.analyticsWG.Done()
		defer cancel()
		fn(ctx)
	}()
}

func NewMCPServer(log *zap.Logger, handler *tools.Handler, cfg *config.Config, a analytics.Analytics) *MCPServer {
	return &MCPServer{
		logger:         log,
		handler:        handler,
		config:         cfg,
		analytics:      a,
		sessionClients: expirable.NewLRU[string, mcp.Implementation](sessionClientCacheSize, nil, sessionClientCacheTTL),
	}
}

func (m *MCPServer) Run(ctx context.Context) error {
	s := server.NewMCPServer("SigNozMCP", version.Version,
		server.WithLogging(),
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions(instructions.ServerInstructions),
		server.WithHooks(m.buildHooks()),
		server.WithToolHandlerMiddleware(m.loggingMiddleware()),
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
	m.handler.RegisterNotificationChannelHandlers(s)
	m.handler.RegisterResourceTemplates(s)

	// Register prompts
	prompts.RegisterPrompts(s.AddPrompt)

	m.logger.Info("All handlers registered successfully")

	if m.config.TransportMode == "http" {
		m.httpSrv = m.buildHTTP(s)
		if err := m.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	return m.runStdio(ctx, s)
}

func (m *MCPServer) Shutdown(ctx context.Context) error {
	if m.config.TransportMode != "http" || m.httpSrv == nil {
		return nil
	}
	return m.httpSrv.Shutdown(ctx)
}

func (m *MCPServer) WaitForAnalytics(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		m.analyticsWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// buildHooks returns lifecycle hooks for observability.
func (m *MCPServer) buildHooks() *server.Hooks {
	hooks := &server.Hooks{}
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(
			otelpkg.GenAISystemKey.String(otelpkg.GenAISystemMCP),
			otelpkg.MCPMethodKey.String(string(method)),
		)
		fields := []zap.Field{zap.String("mcp.method", string(method))}
		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			span.SetAttributes(otelpkg.MCPTenantURLKey.String(signozURL))
			fields = append(fields, zap.String("mcp.tenant_url", signozURL))
		}
		m.logger.Debug("mcp request", fields...)
	})
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		fields := []zap.Field{zap.String("mcp.method", string(method)), zap.Error(err)}
		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			fields = append(fields, zap.String("mcp.tenant_url", signozURL))
		}
		m.logger.Error("mcp error", fields...)
	})
	// Analytics: track session registration after successful initialize.
	// Uses AfterInitialize (not BeforeAny) so failed initializations are not counted.
	hooks.AddAfterInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {
		var sessionID string
		if session := server.ClientSessionFromContext(ctx); session != nil {
			sessionID = session.SessionID()
		}
		if message != nil {
			m.rememberClientInfo(sessionID, message.Params.ClientInfo)
		}

		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			traits := map[string]any{
				analytics.AttrTenantURL: signozURL,
			}
			props := map[string]any{
				analytics.AttrTenantURL: signozURL,
			}
			if sessionID != "" {
				props[analytics.AttrSessionID] = sessionID
			}
			m.attachClientInfo(traits, sessionID)
			m.attachClientInfo(props, sessionID)
			m.identifyAndTrackAsync(ctx, analytics.EventSessionRegistered, traits, props)
		}
	})
	hooks.AddOnRegisterSession(func(ctx context.Context, session server.ClientSession) {
		fields := []zap.Field{}
		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			fields = append(fields, zap.String("mcp.tenant_url", signozURL))
		}
		m.logger.Info("mcp session registered", fields...)

		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			traits := map[string]any{
				analytics.AttrTenantURL: signozURL,
			}
			m.attachClientInfo(traits, session.SessionID())
			m.identifyAsync(ctx, traits)
		}
	})
	hooks.AddOnUnregisterSession(func(ctx context.Context, session server.ClientSession) {
		sessionID := session.SessionID()
		fields := []zap.Field{}
		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			fields = append(fields, zap.String("mcp.tenant_url", signozURL))
		}
		m.logger.Info("mcp session unregistered", fields...)

		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			props := map[string]any{
				analytics.AttrTenantURL: signozURL,
				analytics.AttrSessionID: sessionID,
			}
			m.attachClientInfo(props, sessionID)
			m.trackEventAsync(ctx, analytics.EventSessionUnregistered, props)
		}

		// Delete after dispatch — trackEventAsync has already snapshotted props.
		m.forgetClientInfo(sessionID)
	})
	hooks.AddAfterGetPrompt(func(ctx context.Context, id any, message *mcp.GetPromptRequest, result *mcp.GetPromptResult) {
		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			props := map[string]any{
				analytics.AttrTenantURL:  signozURL,
				analytics.AttrPromptName: message.Params.Name,
			}
			if session := server.ClientSessionFromContext(ctx); session != nil {
				props[analytics.AttrSessionID] = session.SessionID()
				m.attachClientInfo(props, session.SessionID())
			}
			m.trackEventAsync(ctx, analytics.EventPromptFetched, props)
		}
	})
	hooks.AddAfterReadResource(func(ctx context.Context, id any, message *mcp.ReadResourceRequest, result *mcp.ReadResourceResult) {
		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			props := map[string]any{
				analytics.AttrTenantURL:   signozURL,
				analytics.AttrResourceURI: message.Params.URI,
			}
			if session := server.ClientSessionFromContext(ctx); session != nil {
				props[analytics.AttrSessionID] = session.SessionID()
				m.attachClientInfo(props, session.SessionID())
			}
			m.trackEventAsync(ctx, analytics.EventResourceFetched, props)
		}
	})
	return hooks
}

// loggingMiddleware returns a tool handler middleware that logs tool call
// start/finish with duration, tool name, session ID, and search context.
// It also creates an OTel span with GenAI semantic convention attributes.
func (m *MCPServer) loggingMiddleware() server.ToolHandlerMiddleware {
	tracer := otel.Tracer("signoz-mcp-server")
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

			// Create a span for this tool call with GenAI semantic attributes.
			ctx, span := tracer.Start(ctx, "execute_tool",
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					otelpkg.GenAISystemKey.String(otelpkg.GenAISystemMCP),
					otelpkg.GenAIOperationNameKey.String("execute_tool"),
					otelpkg.GenAIToolNameKey.String(req.Params.Name),
				))
			defer span.End()

			// Use the span's own span ID as the tool call ID.
			span.SetAttributes(otelpkg.GenAIToolCallIDKey.String(span.SpanContext().SpanID().String()))

			if sid, ok := util.GetSessionID(ctx); ok && sid != "" {
				span.SetAttributes(otelpkg.MCPSessionIDKey.String(sid))
			}
			if sc, ok := util.GetSearchContext(ctx); ok && sc != "" {
				span.SetAttributes(otelpkg.MCPSearchContextKey.String(sc))
			}
			if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
				span.SetAttributes(otelpkg.MCPTenantURLKey.String(signozURL))
			}

			// Add trace_id and span_id to log fields for correlation.
			traceID := span.SpanContext().TraceID().String()
			spanID := span.SpanContext().SpanID().String()
			fields := []zap.Field{
				zap.String("gen_ai.tool.name", req.Params.Name),
				zap.String("trace_id", traceID),
				zap.String("span_id", spanID),
			}
			if sid, ok := util.GetSessionID(ctx); ok && sid != "" {
				fields = append(fields, zap.String("mcp.session.id", sid))
			}
			if sc, ok := util.GetSearchContext(ctx); ok && sc != "" {
				fields = append(fields, zap.String("mcp.search_context", sc))
			}
			if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
				fields = append(fields, zap.String("mcp.tenant_url", signozURL))
			}

			m.logger.Debug("tool call started", fields...)
			result, err := next(ctx, req)

			// Determine error status: either a Go error or an MCP tool result error.
			isErr := err != nil || (result != nil && result.IsError)
			span.SetAttributes(otelpkg.MCPToolIsErrorKey.Bool(isErr))
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else if result != nil && result.IsError {
				errMsg := extractToolErrorMessage(result)
				span.RecordError(fmt.Errorf("%s", errMsg))
				span.SetStatus(codes.Error, errMsg)
			}

			m.logger.Debug("tool call finished",
				append(fields,
					zap.Duration("duration", time.Since(start)),
					zap.Bool("mcp.tool.is_error", isErr))...)

			// Analytics: track tool call
			if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
				props := map[string]any{
					analytics.AttrTenantURL:   signozURL,
					analytics.AttrToolName:    req.Params.Name,
					analytics.AttrToolIsError: isErr,
					analytics.AttrDurationMs:  time.Since(start).Milliseconds(),
				}
				if sid, ok := util.GetSessionID(ctx); ok && sid != "" {
					props[analytics.AttrSessionID] = sid
					m.attachClientInfo(props, sid)
				}
				if sc, ok := util.GetSearchContext(ctx); ok && sc != "" {
					props[analytics.AttrSearchContext] = sc
				}
				m.trackEventAsync(ctx, analytics.EventToolCalled, props)
			}

			return result, err
		}
	}
}

// extractToolErrorMessage returns the text from the first Content entry of an
// MCP tool error result. Falls back to a generic message if the content is
// empty or not text.
func extractToolErrorMessage(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return "tool returned error result"
	}
	if tc, ok := result.Content[0].(mcp.TextContent); ok && tc.Text != "" {
		return tc.Text
	}
	return "tool returned error result"
}

func (m *MCPServer) runStdio(ctx context.Context, s *server.MCPServer) error {
	m.logger.Info("MCP Server running in stdio mode")

	// Inject env-configured credentials into every request context
	// so that GetClient works uniformly across both transports.
	stdio := server.NewStdioServer(s)
	server.WithStdioContextFunc(func(ctx context.Context) context.Context {
		ctx = util.SetAPIKey(ctx, m.config.APIKey)
		ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
		ctx = util.SetSigNozURL(ctx, m.config.URL)
		return ctx
	})(stdio)

	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
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
					m.logger.Debug("Using JWT token authentication via Authorization header", zap.String("mcp.tenant_url", customURL))
				} else {
					// PAT token — forward via SIGNOZ-API-KEY
					apiKey = token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
					m.logger.Debug("Using API KEY token authentication via SIGNOZ-API-KEY header", zap.String("mcp.tenant_url", customURL))
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
					m.logger.Debug("OAuth access token extracted from Authorization header", zap.String("mcp.tenant_url", signozURL))
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
			m.logger.Debug("Using URL from X-SigNoz-URL header", zap.String("mcp.tenant_url", signozURL))
		} else if m.config.URL != "" {
			signozURL = m.config.URL
			m.logger.Debug("Using URL from environment config", zap.String("mcp.tenant_url", signozURL))
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

func (m *MCPServer) buildHTTP(s *server.MCPServer) *http.Server {
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
		oauthHandler := oauth.NewHandler(m.logger, m.config, m.trackOAuthEvent)
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

	return srv
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
