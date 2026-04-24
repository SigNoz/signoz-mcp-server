package mcp_server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/instructions"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/prompts"
	"github.com/SigNoz/signoz-mcp-server/pkg/session"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Streamable HTTP only fires OnUnregisterSession for the long-lived GET
// listener — POST-only clients never trigger explicit cleanup — so the
// session → ClientInfo map needs bounded eviction.
const (
	sessionClientCacheSize       = 1024
	sessionClientCacheTTL        = 30 * time.Minute
	defaultMethodSpanBodyMaxSize = 1 << 20
	// methodObsTombstoneTTL is how long an expired method observation lingers
	// in methodObs so a late OnError hook can detect the race and skip its
	// fallback (preventing double-count). Finish deletes the entry immediately;
	// this timer is the safety net for the pathological case where finish
	// never runs at all.
	methodObsTombstoneTTL = time.Second
	// streamableHTTPHeartbeatInterval is how often the server pings clients on
	// the GET listen stream. Tuned to fire well inside the default idle timeout
	// of common ingress/LB layers (AWS ALB 60s, nginx 60s, Cloudflare ~100s) so
	// intermediate proxies don't close the stream and force clients to reopen
	// with a fresh `initialize` handshake. Ping is the MCP-spec utility; see
	// https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/ping.
	// Requires mcp-go >= v0.44.1, which routes empty ping replies to HTTP 202
	// instead of the sampling-response path (mark3labs/mcp-go#740).
	streamableHTTPHeartbeatInterval = 20 * time.Second
)

type MCPServer struct {
	logger                 *slog.Logger
	handler                *tools.Handler
	config                 *config.Config
	analytics              analytics.Analytics
	meters                 *otelpkg.Meters
	methodObs              sync.Map
	sessionClients         *expirable.LRU[string, mcp.Implementation]
	maxMethodSpanBodyBytes int64
	methodObsTombstoneTTL  time.Duration
	// httpServer is published via atomic.Pointer so Shutdown (on the main
	// goroutine) can safely race Run's publication (on the errgroup
	// goroutine) when SIGTERM lands mid-startup.
	httpServer  atomic.Pointer[http.Server]
	analyticsWG sync.WaitGroup

	// sessionSigner stateless-signs the mcp-go session ID emitted on a
	// public `initialize` and verifies it on subsequent GET/DELETE. This
	// replaces a process-local sync.Map that broke under multi-replica
	// deployments — see pkg/session for the threat model and token layout.
	sessionSigner *session.Signer
	publicLimiter *publicDocsRateLimiter
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
			m.logger.WarnContext(detachedCtx, "analytics identity resolution failed; skipping identify", logpkg.ErrAttr(err))
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
			m.logger.WarnContext(detachedCtx, "analytics identity resolution failed; skipping track",
				slog.String("event", event),
				logpkg.ErrAttr(err))
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
			m.logger.WarnContext(detachedCtx, "analytics identity resolution failed; skipping identify+track",
				slog.String("event", event),
				logpkg.ErrAttr(err))
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
	if m.meters != nil {
		attrs := []attribute.KeyValue{attribute.String("event", event)}
		attrs = otelpkg.AppendTenantURL(ctx, attrs)
		m.meters.OAuthEvents.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	m.trackEventAsync(ctx, event, props)
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

func NewMCPServer(log *slog.Logger, handler *tools.Handler, cfg *config.Config, a analytics.Analytics, meters *otelpkg.Meters) *MCPServer {
	if handler != nil {
		handler.SetMeters(meters)
	}
	return &MCPServer{
		logger:                 log,
		handler:                handler,
		config:                 cfg,
		analytics:              a,
		meters:                 meters,
		sessionClients:         expirable.NewLRU[string, mcp.Implementation](sessionClientCacheSize, nil, sessionClientCacheTTL),
		maxMethodSpanBodyBytes: defaultMethodSpanBodyMaxSize,
		methodObsTombstoneTTL:  methodObsTombstoneTTL,
		sessionSigner:          buildPublicSessionSigner(log, cfg),
	}
}

// buildPublicSessionSigner returns a ready-to-use Signer. If the
// operator supplied SIGNOZ_MCP_PUBLIC_SESSION_KEYS we use that; if not
// we mint an ephemeral 32-byte key so local-dev and single-replica
// deploys keep working.
//
// Failure modes here are narrow: a configured-but-invalid key ring
// would already have been rejected by LoadConfig before we got here.
// The only paths that still error are crypto/rand exhaustion and
// session.NewSigner's defensive checks, both of which we log and then
// fall through to an unset signer — the middleware treats a nil signer
// as "no public path available" rather than crashing the process.
func buildPublicSessionSigner(log *slog.Logger, cfg *config.Config) *session.Signer {
	if cfg == nil {
		return nil
	}
	keys := cfg.PublicSessionKeys
	ephemeral := len(keys) == 0
	if ephemeral {
		k, err := session.GenerateKey()
		if err != nil {
			if log != nil {
				log.Warn("failed to generate ephemeral public-session key; public docs path disabled", logpkg.ErrAttr(err))
			}
			return nil
		}
		keys = [][]byte{k}
		if log != nil && cfg.TransportMode == "http" {
			log.Warn(
				"SIGNOZ_MCP_PUBLIC_SESSION_KEYS not set; minted ephemeral signing key. "+
					"Public sessions will not survive pod restarts and multi-replica deployments will see 401s.",
				slog.Bool("ephemeral", true),
			)
		}
	}
	signer, err := session.NewSigner(session.SignerConfig{
		Keys: keys,
		TTL:  cfg.PublicSessionTTL,
	})
	if err != nil {
		if log != nil {
			log.Warn("failed to build public-session signer; public docs path disabled", logpkg.ErrAttr(err))
		}
		return nil
	}
	if log != nil && !ephemeral {
		log.Info("public-session signer initialized", slog.String("active_kid", signer.ActiveKID()))
	}
	return signer
}

func (m *MCPServer) Run(ctx context.Context) error {
	// Middleware order matters: mcp-go applies tool-handler middlewares in
	// reverse-slice order, so the first-appended wraps outermost. Register
	// loggingMiddleware FIRST so it wraps recovery — when a tool panics,
	// recovery converts it to an error that bubbles back to loggingMiddleware
	// via the normal return path, so mcp.tool.calls{is_error=true} and the
	// codes.Error span status actually get recorded.
	s := server.NewMCPServer("SigNozMCP", version.Version,
		server.WithLogging(),
		server.WithToolCapabilities(false),
		server.WithInstructions(instructions.ServerInstructions),
		server.WithHooks(m.buildHooks()),
		server.WithToolHandlerMiddleware(m.loggingMiddleware()),
		server.WithRecovery(),
	)

	m.logger.InfoContext(ctx, "Starting SigNoz MCP Server",
		slog.String("server_name", "SigNozMCPServer"),
		slog.String("transport_mode", m.config.TransportMode))

	// Short-circuit if shutdown already signaled. The async docs-index build
	// below would otherwise continue to run after Run() returns; tests rely
	// on a 2 s exit bound.
	if err := ctx.Err(); err != nil {
		m.logger.InfoContext(ctx, "Shutdown signaled before startup; exiting early")
		return nil
	}

	// Register a placeholder IndexRegistry up-front so the docs tool handlers
	// have a non-nil *IndexRegistry to reference. Ready() reports false until
	// the async corpus build below calls Swap() with a real snapshot, so docs
	// handlers correctly return INDEX_NOT_READY in the window before the
	// index is populated. This lets HTTP server publication (below) happen
	// within the 1 s test bound instead of waiting on the 1-3 s bleve build.
	placeholderRegistry, err := docsindex.NewPlaceholderRegistry(ctx)
	if err != nil {
		return fmt.Errorf("initialize placeholder docs registry: %w", err)
	}
	m.handler.SetDocsIndex(placeholderRegistry)

	if m.publicLimiter == nil {
		m.publicLimiter = newPublicDocsRateLimiter(m.config)
	}
	m.publicLimiter.start(ctx)

	// Build the real corpus-backed index asynchronously; swap it in when ready.
	// Always start the refresher even when the embedded corpus is unavailable
	// (schema mismatch, decode failure, missing asset) so the server can
	// recover via live fetch instead of remaining empty until the next
	// process restart.
	go func() {
		var loaded bool
		snapshot, loadErr := docsindex.LoadEmbeddedCorpus()
		if loadErr != nil {
			m.logger.WarnContext(ctx, "embedded docs corpus unavailable; refresher will attempt a live build", logpkg.ErrAttr(loadErr))
		} else if swapErr := placeholderRegistry.Swap(ctx, snapshot); swapErr != nil {
			m.logger.ErrorContext(ctx, "docs index initial build failed; refresher will retry", logpkg.ErrAttr(swapErr))
		} else {
			placeholderRegistry.RecordMetrics(ctx, m.meters)
			m.logger.InfoContext(ctx, "Docs index ready", slog.Int("pages", len(snapshot.Pages)))
			loaded = true
		}
		refresher := docsindex.NewRefresher(m.logger, placeholderRegistry, docsindex.NewFetcher(docsindex.FetcherConfig{}), docsindex.RefreshConfig{
			RefreshInterval:     m.config.DocsRefreshInterval,
			FullRefreshInterval: m.config.DocsFullRefreshInterval,
		})
		refresher.SetMeters(m.meters)
		refresher.Start(ctx)
		// Only kick an immediate refresh when the embedded blob could not
		// be loaded (schema mismatch, decode failure, missing asset) — in
		// that case the index is empty and waiting on the 6 h scheduled
		// tick would serve empty docs tools in the meantime. In the normal
		// "blob loaded fine" case, we deliberately skip the on-boot refresh
		// so startup doesn't pay a ~15 s CPU/memory spike against the live
		// corpus; freshness is handled by the scheduled refresher
		// (6 h incremental, 24 h forced) and by the manually-dispatched
		// docs-index-refresh workflow that maintainers run ahead of a
		// release (.github/workflows/docs-index-refresh.yml) to keep the
		// committed blob reasonably fresh at cold-boot time.
		if !loaded {
			go func() {
				if err := refresher.Trigger(ctx, true); err != nil {
					m.logger.WarnContext(ctx, "initial docs live refresh failed", logpkg.ErrAttr(err))
				}
			}()
		}
	}()

	// Register all handlers
	m.handler.RegisterMetricsHandlers(s)
	m.handler.RegisterFieldsHandlers(s)
	m.handler.RegisterAlertsHandlers(s)
	m.handler.RegisterDashboardHandlers(s)
	m.handler.RegisterServiceHandlers(s)
	m.handler.RegisterQueryBuilderV5Handlers(s)
	m.handler.RegisterLogsHandlers(s)
	m.handler.RegisterViewHandlers(s)
	m.handler.RegisterDocsHandlers(s)
	m.handler.RegisterTracesHandlers(s)
	m.handler.RegisterNotificationChannelHandlers(s)
	m.handler.RegisterResourceTemplates(s)

	// Register prompts
	prompts.RegisterPrompts(s.AddPrompt)

	m.logger.InfoContext(ctx, "All handlers registered successfully")

	if m.config.TransportMode == "http" {
		// Build the *http.Server and publish it via the atomic pointer
		// BEFORE checking ctx or calling ListenAndServe. That way, if main
		// calls Shutdown after we publish but before we call
		// ListenAndServe, Shutdown will observe the non-nil pointer and
		// close the server — so ListenAndServe returns promptly with
		// http.ErrServerClosed instead of hanging until the 15s join
		// timeout. If Shutdown ran earlier (before we published), we
		// detect that via ctx.Err() below and explicitly close the server
		// we just built so it does not leak.
		srv := m.buildHTTP(s)
		m.httpServer.Store(srv)
		if err := ctx.Err(); err != nil {
			m.logger.InfoContext(ctx, "Shutdown signaled before HTTP listener started; closing the unused server")
			_ = srv.Shutdown(context.Background())
			return nil
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	return m.runStdio(ctx, s)
}

// Shutdown closes the HTTP listener if one is active. It is the caller's
// responsibility to also cancel the context passed to Run — Shutdown alone
// does not stop Run from starting a listener if it has not yet reached the
// publication point. In normal use (main.go), signal.NotifyContext cancels
// the run ctx and Shutdown is called right after, so both signals converge.
func (m *MCPServer) Shutdown(ctx context.Context) error {
	if m.config.TransportMode != "http" {
		return nil
	}
	srv := m.httpServer.Load()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
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

type methodObservation struct {
	ctx         context.Context
	method      mcp.MCPMethod
	started     time.Time
	cleanupStop func() bool
	// completed is the exactly-once guard. CAS'd by finishMethodObservation
	// (the hook path) or expireMethodObservation (the ctx-cancel path); whichever
	// wins emits the metric and ends the span. The loser skips emission but the
	// entry stays in methodObs so the loser can still detect "we were beaten by
	// a race" vs "observation was never stored at all" (unmarshal-failure path).
	completed atomic.Bool
}

func methodObservationKey(ctx context.Context, id any, method mcp.MCPMethod, message any) string {
	sessionID := ""
	if session := server.ClientSessionFromContext(ctx); session != nil {
		sessionID = session.SessionID()
	}

	messageID := fmt.Sprintf("%T", message)
	if message != nil {
		messageID = fmt.Sprintf("%s:%p", messageID, message)
	}

	return fmt.Sprintf("%s|%s|%v|%s", sessionID, method, id, messageID)
}

func shouldObserveMethod(method mcp.MCPMethod) bool {
	return method != mcp.MethodToolsCall && !strings.HasPrefix(string(method), "notifications/")
}

func isKnownRequestMethod(method mcp.MCPMethod) bool {
	switch method {
	case mcp.MethodInitialize,
		mcp.MethodPing,
		mcp.MethodSetLogLevel,
		mcp.MethodResourcesList,
		mcp.MethodResourcesTemplatesList,
		mcp.MethodResourcesRead,
		mcp.MethodPromptsList,
		mcp.MethodPromptsGet,
		mcp.MethodToolsList,
		mcp.MethodToolsCall:
		return true
	default:
		return false
	}
}

func methodErrorType(err error) string {
	if err == nil {
		return ""
	}

	var unparsable *server.UnparsableMessageError
	switch {
	case errors.As(err, &unparsable):
		return "parse"
	case errors.Is(err, server.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, server.ErrResourceNotFound), errors.Is(err, server.ErrPromptNotFound), errors.Is(err, server.ErrToolNotFound):
		return "not_found"
	default:
		return "internal"
	}
}

func (m *MCPServer) beginMethodObservation(ctx context.Context, id any, method mcp.MCPMethod, message any) {
	if !shouldObserveMethod(method) {
		return
	}

	key := methodObservationKey(ctx, id, method, message)
	observation := &methodObservation{
		ctx:     ctx,
		method:  method,
		started: time.Now(),
	}
	stored := make(chan struct{})
	observation.cleanupStop = context.AfterFunc(ctx, func() {
		<-stored
		m.expireMethodObservation(key)
	})

	m.methodObs.Store(key, observation)
	close(stored)
}

// finishMethodObservation is called from OnSuccess/OnError hooks. Returns true
// if a matching observation entry was found (regardless of whether the caller's
// emission won the race with expireMethodObservation), so callers can skip the
// unmarshal-path fallback. Returns false only when no observation was ever
// stored for this key — that's the "OnError without BeforeAny" path (e.g.,
// mcp-go unmarshal failures in request_handler.go), where the caller SHOULD
// fallback to synthesize a one-shot emission so the method span gets ended.
func (m *MCPServer) finishMethodObservation(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) bool {
	if !shouldObserveMethod(method) {
		return false
	}

	key := methodObservationKey(ctx, id, method, message)
	value, ok := m.methodObs.Load(key)
	if !ok {
		return false
	}

	observation, ok := value.(*methodObservation)
	if !ok {
		m.methodObs.Delete(key)
		return false
	}

	if observation.completed.CompareAndSwap(false, true) {
		m.completeMethodObservation(observation, err)
	}
	// finish owns map cleanup — delete regardless of which path won the CAS so
	// expire's tombstone doesn't leak indefinitely.
	m.methodObs.Delete(key)
	return true
}

func (m *MCPServer) expireMethodObservation(key string) {
	// Load (not LoadAndDelete) so a late finishMethodObservation can still see
	// the tombstone and skip its own emission. finish removes the entry.
	value, ok := m.methodObs.Load(key)
	if !ok {
		return
	}

	observation, ok := value.(*methodObservation)
	if !ok {
		m.methodObs.Delete(key)
		return
	}

	if !observation.completed.CompareAndSwap(false, true) {
		// finish already emitted; nothing to do.
		return
	}

	ctxErr := observation.ctx.Err()
	expireErr := errors.New("request context ended before success/error hook")
	if ctxErr != nil {
		expireErr = fmt.Errorf("%w: %v", expireErr, ctxErr)
	}
	m.completeMethodObservation(observation, expireErr)

	logCtx := context.WithoutCancel(observation.ctx)
	attrs := []any{slog.String("mcp.method", string(observation.method))}
	if ctxErr != nil {
		attrs = append(attrs, slog.String("context_error", ctxErr.Error()))
	}
	m.logger.WarnContext(logCtx, "mcp method observation ended without success/error hook", attrs...)

	// Drop the tombstone after a short window. Finish usually deletes the
	// entry first; this timer is only load-bearing when no hook ever fires.
	ttl := m.methodObsTombstoneTTL
	if ttl <= 0 {
		ttl = methodObsTombstoneTTL
	}
	time.AfterFunc(ttl, func() {
		m.methodObs.Delete(key)
	})
}

// completeMethodObservationFallback synthesizes a one-shot observation for
// paths where BeforeAny never fired (mcp-go unmarshal-failure OnError invocations
// and "notification channel blocked" operational errors — see mcp-go
// request_handler.go and session.go). Without this, the method span started in
// methodSpanMiddleware would leak and the error would be invisible in
// mcp.method.calls.
func (m *MCPServer) completeMethodObservationFallback(ctx context.Context, method mcp.MCPMethod, err error) {
	observation := &methodObservation{
		ctx:     ctx,
		method:  method,
		started: time.Now(),
	}
	m.completeMethodObservation(observation, err)
}

func (m *MCPServer) completeMethodObservation(observation *methodObservation, err error) {
	if observation == nil {
		return
	}
	if observation.cleanupStop != nil {
		observation.cleanupStop()
	}

	ctx := context.WithoutCancel(observation.ctx)
	span := trace.SpanFromContext(ctx)
	spanAttrs := []attribute.KeyValue{}
	if session := server.ClientSessionFromContext(ctx); session != nil && session.SessionID() != "" {
		spanAttrs = append(spanAttrs, otelpkg.MCPSessionIDKey.String(session.SessionID()))
	}
	spanAttrs = otelpkg.AppendTenantURL(ctx, spanAttrs)

	errorType := methodErrorType(err)
	if errorType != "" {
		spanAttrs = append(spanAttrs, attribute.String("error.type", errorType))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(spanAttrs...)

	if m.meters != nil {
		metricAttrs := []attribute.KeyValue{
			attribute.String("mcp.method.name", string(observation.method)),
		}
		metricAttrs = otelpkg.AppendTenantURL(ctx, metricAttrs)
		if errorType != "" {
			metricAttrs = append(metricAttrs, attribute.String("error.type", errorType))
		}

		opts := metric.WithAttributes(metricAttrs...)
		m.meters.MethodCalls.Add(ctx, 1, opts)
		m.meters.MethodDuration.Record(ctx, float64(time.Since(observation.started))/float64(time.Millisecond), opts)
	}

	span.End()
}

func (m *MCPServer) startMethodSpan(ctx context.Context, method mcp.MCPMethod) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		otelpkg.MCPMethodKey.String(string(method)),
	}
	if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
		attrs = append(attrs, otelpkg.MCPTenantURLKey.String(signozURL))
	}

	return otel.Tracer("signoz-mcp-server").Start(ctx, "MCP "+string(method),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
}

func methodFromJSONRPCMessage(message []byte) (mcp.MCPMethod, bool) {
	var envelope struct {
		JSONRPC string        `json:"jsonrpc"`
		Method  mcp.MCPMethod `json:"method"`
		ID      any           `json:"id,omitempty"`
		Result  any           `json:"result,omitempty"`
	}

	if err := json.Unmarshal(message, &envelope); err != nil {
		return "", false
	}
	if envelope.JSONRPC != mcp.JSONRPC_VERSION || envelope.ID == nil || envelope.Result != nil {
		return "", false
	}
	if !isKnownRequestMethod(envelope.Method) {
		return "", false
	}
	if !shouldObserveMethod(envelope.Method) {
		return "", false
	}

	return envelope.Method, true
}

type delegatedReadCloser struct {
	io.Reader
	io.Closer
}

func (m *MCPServer) peekMethodSpanBody(body io.ReadCloser) ([]byte, io.ReadCloser, bool, error) {
	if body == nil {
		return nil, nil, false, nil
	}

	limited := &io.LimitedReader{R: body, N: m.maxMethodSpanBodyBytes + 1}
	prefix, err := io.ReadAll(limited)
	reconstructed := delegatedReadCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), body),
		Closer: body,
	}
	if err != nil {
		return nil, reconstructed, false, err
	}
	if int64(len(prefix)) > m.maxMethodSpanBodyBytes {
		return nil, reconstructed, true, nil
	}
	return prefix, reconstructed, false, nil
}

func (m *MCPServer) methodSpanMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}

		body, reconstructed, oversized, err := m.peekMethodSpanBody(r.Body)
		if reconstructed != nil {
			r.Body = reconstructed
		}
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if oversized {
			next.ServeHTTP(w, r)
			return
		}

		method, ok := methodFromJSONRPCMessage(body)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		ctx, _ := m.startMethodSpan(r.Context(), method)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// buildHooks returns lifecycle hooks for observability.
func (m *MCPServer) buildHooks() *server.Hooks {
	hooks := &server.Hooks{}
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		m.beginMethodObservation(ctx, id, method, message)
		span := trace.SpanFromContext(ctx)
		spanAttrs := []attribute.KeyValue{}
		if session := server.ClientSessionFromContext(ctx); session != nil && session.SessionID() != "" {
			spanAttrs = append(spanAttrs, otelpkg.MCPSessionIDKey.String(session.SessionID()))
		}
		spanAttrs = otelpkg.AppendTenantURL(ctx, spanAttrs)
		if len(spanAttrs) > 0 {
			span.SetAttributes(spanAttrs...)
		}
		m.logger.DebugContext(ctx, "mcp request", slog.String("mcp.method", string(method)))
	})
	hooks.AddOnSuccess(func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
		if !m.finishMethodObservation(ctx, id, method, message, nil) && shouldObserveMethod(method) {
			trace.SpanFromContext(ctx).End()
		}
	})
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		if shouldObserveMethod(method) {
			// finish returns true iff a matching observation existed (even if
			// expireMethodObservation already emitted on the race path — the
			// tombstone prevents double-count). It returns false only when
			// BeforeAny never stored one (mcp-go unmarshal-failure paths), in
			// which case we synthesize a one-shot emission so the method span
			// gets ended and mcp.method.calls records the failure.
			if !m.finishMethodObservation(ctx, id, method, message, err) {
				m.completeMethodObservationFallback(ctx, method, err)
			}
		} else {
			span := trace.SpanFromContext(ctx)
			if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
				span.SetAttributes(attr)
			}
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		m.logger.ErrorContext(ctx, "mcp error",
			slog.String("mcp.method", string(method)),
			logpkg.ErrAttr(err))
	})
	// Analytics: track session registration after successful initialize.
	// Uses AfterInitialize (not BeforeAny) so failed initializations are not counted.
	hooks.AddAfterInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {
		if m.meters != nil {
			attrs := otelpkg.AppendTenantURL(ctx, nil)
			m.meters.SessionRegistered.Add(ctx, 1, metric.WithAttributes(attrs...))
		}

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
			if message != nil && message.Params.ProtocolVersion != "" {
				props[analytics.AttrProtocolVersion] = message.Params.ProtocolVersion
				traits[analytics.AttrProtocolVersion] = message.Params.ProtocolVersion
			}
			m.attachClientInfo(traits, sessionID)
			m.attachClientInfo(props, sessionID)
			m.identifyAndTrackAsync(ctx, analytics.EventSessionRegistered, traits, props)
		}
	})
	hooks.AddOnRegisterSession(func(ctx context.Context, session server.ClientSession) {
		m.logger.InfoContext(ctx, "mcp session registered")

		if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
			traits := map[string]any{
				analytics.AttrTenantURL: signozURL,
			}
			m.attachClientInfo(traits, session.SessionID())
			m.identifyAsync(ctx, traits)
		}
	})
	hooks.AddOnUnregisterSession(func(ctx context.Context, sess server.ClientSession) {
		m.logger.InfoContext(ctx, "mcp session unregistered")
		m.forgetClientInfo(sess.SessionID())
		// NOTE: we deliberately do NOT clear rate-limit buckets here.
		// mcp-go's session lifecycle (this hook fires on DELETE or GET
		// stream close) is INDEPENDENT of the stateless public-session
		// token lifetime — the token remains valid until its embedded
		// exp. Scrubbing buckets on mcp-go's close would let a client
		// reset quota by cycling streams with the same still-valid
		// token. The idle sweeper reclaims buckets after
		// publicLimiterIdleTTL, which is the correct bound.
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
			if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
				span.SetAttributes(attr)
			}

			toolNameAttr := slog.String("gen_ai.tool.name", req.Params.Name)
			m.logger.DebugContext(ctx, "tool call started", toolNameAttr)
			result, err := next(ctx, req)

			// Determine error status: either a Go error or an MCP tool result error.
			isErr := err != nil || (result != nil && result.IsError)
			span.SetAttributes(otelpkg.MCPToolIsErrorKey.Bool(isErr))
			// Always emit the result size — even zero — so it matches the log
			// field and downstream aggregations (avg, histogram) don't drop
			// empty-result tool calls as nulls.
			resultBytes := approxResultBytes(result)
			span.SetAttributes(otelpkg.MCPToolResultBytesKey.Int64(resultBytes))
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else if result != nil && result.IsError {
				errMsg := extractToolErrorMessage(result)
				span.RecordError(fmt.Errorf("%s", errMsg))
				span.SetStatus(codes.Error, errMsg)
			}

			duration := time.Since(start)
			sizeAttr := slog.Int64("mcp.tool.result.size_bytes", resultBytes)
			switch {
			case err != nil:
				m.logger.ErrorContext(ctx, "tool call failed",
					toolNameAttr,
					slog.Duration("duration", duration),
					slog.Bool("mcp.tool.is_error", isErr),
					sizeAttr,
					logpkg.ErrAttr(err))
			case result != nil && result.IsError:
				m.logger.WarnContext(ctx, "tool call returned error result",
					toolNameAttr,
					slog.Duration("duration", duration),
					slog.Bool("mcp.tool.is_error", isErr),
					sizeAttr,
					slog.String("error_message", extractToolErrorMessage(result)))
			default:
				m.logger.DebugContext(ctx, "tool call finished",
					toolNameAttr,
					slog.Duration("duration", duration),
					slog.Bool("mcp.tool.is_error", isErr),
					sizeAttr)
			}

			if m.meters != nil {
				attrKVs := []attribute.KeyValue{
					attribute.String("gen_ai.tool.name", req.Params.Name),
					attribute.Bool("mcp.tool.is_error", isErr),
				}
				attrKVs = otelpkg.AppendTenantURL(ctx, attrKVs)
				attrs := metric.WithAttributes(attrKVs...)
				m.meters.ToolCalls.Add(ctx, 1, attrs)
				m.meters.ToolCallDuration.Record(ctx, float64(duration)/float64(time.Millisecond), attrs)
			}

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
				if errorType := toolErrorType(err, result); errorType != "" {
					props[analytics.AttrErrorType] = errorType
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

// toolErrorType classifies a tool-call failure into a small, bounded set of
// categories so dashboards can split errors without exploding cardinality.
// Returns "" when there is no error.
func toolErrorType(err error, result *mcp.CallToolResult) string {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "timeout"
		}
		if errors.Is(err, context.Canceled) {
			return "cancelled"
		}
		return "internal"
	}
	if result == nil || !result.IsError {
		return ""
	}

	msg := strings.ToLower(extractToolErrorMessage(result))
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "timeout"
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403"):
		return "unauthorized"
	case strings.Contains(msg, "status 4"):
		return "upstream_4xx"
	case strings.Contains(msg, "status 5"):
		return "upstream_5xx"
	default:
		return "tool_error"
	}
}

// approxResultBytes sums the length of text content entries in a tool result.
// Binary blobs are ignored (we don't want to materialize them just to measure).
func approxResultBytes(result *mcp.CallToolResult) int64 {
	if result == nil {
		return 0
	}
	var total int64
	for _, c := range result.Content {
		tc, ok := c.(mcp.TextContent)
		if !ok {
			continue
		}
		total += int64(len(tc.Text))
	}
	return total
}

func (m *MCPServer) runStdio(ctx context.Context, s *server.MCPServer) error {
	m.logger.InfoContext(ctx, "MCP Server running in stdio mode")

	// Inject env-configured credentials into every request context
	// so that GetClient works uniformly across both transports.
	stdio := server.NewStdioServer(s)
	stdio.SetContextFunc(func(ctx context.Context) context.Context {
		ctx = util.SetAPIKey(ctx, m.config.APIKey)
		ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
		ctx = util.SetSigNozURL(ctx, m.config.URL)
		return ctx
	})

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
		if isPublicDocsRequest(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
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
			m.logger.DebugContext(ctx, "Using SIGNOZ-API-KEY header for auth")
		} else if authHeader != "" {
			token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			if customURL != "" {
				if isJWTToken(token) {
					// JWT token — forward via Authorization: Bearer <token>
					apiKey = "Bearer " + token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "Authorization")
					m.logger.DebugContext(ctx, "Using JWT token authentication via Authorization header", slog.String("mcp.tenant_url", customURL))
				} else {
					// PAT token — forward via SIGNOZ-API-KEY
					apiKey = token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
					m.logger.DebugContext(ctx, "Using API KEY token authentication via SIGNOZ-API-KEY header", slog.String("mcp.tenant_url", customURL))
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
					m.logger.DebugContext(ctx, "OAuth access token extracted from Authorization header", slog.String("mcp.tenant_url", signozURL))
				case errors.Is(err, oauth.ErrExpiredToken):
					// The token is expired but was once server-issued, so the
					// embedded URL is a trusted tenant value. Decorate the
					// otelhttp root span so the 401 trace carries mcp.tenant_url.
					if decryptedURL != "" {
						ctx = util.SetSigNozURL(ctx, decryptedURL)
						if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
							trace.SpanFromContext(ctx).SetAttributes(attr)
						}
					}
					m.setOAuthChallenge(w, `error="invalid_token", error_description="access token expired"`)
					http.Error(w, "OAuth access token expired", http.StatusUnauthorized)
					return
				default:
					// Only fall back to legacy raw API key mode when the request also
					// carries an explicit SigNoz URL (header or config). Otherwise a
					// stale bearer token can mask the OAuth challenge flow.
					if customURL == "" && m.config.URL == "" {
						m.logger.WarnContext(ctx, "Bearer token did not match OAuth token format and no SigNoz URL is available for legacy fallback")
						m.setOAuthChallenge(w, `error="invalid_token", error_description="access token is invalid"`)
						http.Error(w, "OAuth access token is invalid", http.StatusUnauthorized)
						return
					}
					apiKey = token
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
					m.logger.DebugContext(ctx, "Bearer token did not match OAuth token format, falling back to raw API key")
				}
			} else {
				apiKey = token
				ctx = util.SetAPIKey(ctx, apiKey)
				ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
				m.logger.DebugContext(ctx, "Using API KEY token authentication via SIGNOZ-API-KEY header")
			}

		} else if m.config.APIKey != "" {
			// Fallback to config API key
			apiKey = m.config.APIKey
			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			m.logger.DebugContext(ctx, "Using API key from environment config")
		} else {
			m.logger.WarnContext(ctx, "No API key found in headers or environment")
			if m.config.OAuthEnabled {
				m.setOAuthChallenge(w, "")
			}
			http.Error(w, "Authorization or SIGNOZ-API-KEY header required", http.StatusUnauthorized)
			return
		}

		if usedOAuthToken {
			ctx = util.SetSigNozURL(ctx, signozURL)
			if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
				trace.SpanFromContext(ctx).SetAttributes(attr)
			}
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}

		// Determine final URL with precedence: X-SigNoz-URL header > config URL
		if customURL != "" {
			trimmed := strings.TrimSuffix(customURL, "/")
			normalized, err := util.NormalizeSigNozURL(trimmed)
			if err != nil {
				m.logger.WarnContext(ctx, "Invalid X-SigNoz-URL header",
					slog.String("url", customURL), logpkg.ErrAttr(err))
				http.Error(w, fmt.Sprintf("Invalid X-SigNoz-URL: %v", err), http.StatusBadRequest)
				return
			}
			signozURL = normalized
			m.logger.DebugContext(ctx, "Using URL from X-SigNoz-URL header", slog.String("mcp.tenant_url", signozURL))
		} else if m.config.URL != "" {
			signozURL = m.config.URL
			m.logger.DebugContext(ctx, "Using URL from environment config", slog.String("mcp.tenant_url", signozURL))
		} else {
			m.logger.WarnContext(ctx, "No SigNoz URL found in X-SigNoz-URL header or environment")
			http.Error(w, "SigNoz instance URL is required", http.StatusBadRequest)
			return
		}

		ctx = util.SetSigNozURL(ctx, signozURL)

		// Decorate the otelhttp root span with tenant_url so every /mcp
		// request trace is queryable per customer in SigNoz.
		if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
			trace.SpanFromContext(ctx).SetAttributes(attr)
		}

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

	// Readiness is stricter than liveness: Kubernetes should only route
	// traffic to pods that can serve docs tools without INDEX_NOT_READY.
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if m.handler == nil || !m.handler.DocsIndexReady() {
			http.Error(w, "docs index not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	if m.config.OAuthEnabled {
		oauthHandler := oauth.NewHandler(m.logger, m.config, m.trackOAuthEvent, m.meters)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", oauthHandler.HandleProtectedResourceMetadata)
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauthHandler.HandleAuthorizationServerMetadata)
		mux.HandleFunc("POST /oauth/register", oauthHandler.HandleRegisterClient)
		mux.HandleFunc("GET /oauth/authorize", oauthHandler.HandleAuthorizePage)
		mux.HandleFunc("POST /oauth/authorize", oauthHandler.HandleAuthorizeSubmit)
		mux.HandleFunc("POST /oauth/token", oauthHandler.HandleToken)
	}

	mcpHandler := server.NewStreamableHTTPServer(s,
		server.WithHeartbeatInterval(streamableHTTPHeartbeatInterval),
	)
	if m.publicLimiter == nil {
		m.publicLimiter = newPublicDocsRateLimiter(m.config)
	}
	mux.Handle("/mcp", m.authOrPublicMiddleware(m.publicDocsRateLimiter(m.authMiddleware(m.methodSpanMiddleware(mcpHandler)))))

	m.logger.Info("Listening for MCP clients",
		slog.String("addr", addr),
		slog.String("mcp_endpoint", "/mcp"))

	// Wrap the entire mux with OpenTelemetry HTTP instrumentation to
	// automatically create spans for every inbound request. Use a span-name
	// formatter matching the OTel HTTP semconv recommendation
	// ({http.request.method} {http.route}) so each endpoint produces a
	// distinct, readable span name like "HTTP POST /mcp" instead of every
	// request collapsing into the default operation name.
	handler := otelhttp.NewHandler(mux, "signoz-mcp-server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return "HTTP " + r.Method + " " + r.URL.Path
		}),
	)

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
