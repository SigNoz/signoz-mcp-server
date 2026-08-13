package mcp_server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	mcpcontract "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/oauth"
	"github.com/SigNoz/signoz-mcp-server/pkg/analytics"
	"github.com/SigNoz/signoz-mcp-server/pkg/instructions"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/prompts"
	"github.com/SigNoz/signoz-mcp-server/pkg/toolerrors"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	officialSDKPageSize = 128
	unknownToolName     = "unknown"
)

type MCPServer struct {
	logger    *slog.Logger
	handler   *tools.Handler
	config    *config.Config
	analytics analytics.Analytics
	meters    *otelpkg.Meters
	// httpServer is published via atomic.Pointer so Shutdown (on the main
	// goroutine) can safely race Run's publication (on the errgroup
	// goroutine) when SIGTERM lands mid-startup.
	httpServer  atomic.Pointer[http.Server]
	analyticsWG sync.WaitGroup
}

type sdkLogHandler struct {
	next slog.Handler
}

func (h *sdkLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// ERROR records may be narrowly downgraded in Handle, so allow them through
	// when DEBUG is enabled even if the wrapped handler filters ERROR separately.
	return h.next.Enabled(ctx, level) || (level >= slog.LevelError && h.next.Enabled(ctx, slog.LevelDebug))
}

func (h *sdkLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "server run cancelled" || (record.Message == "method removed in the new protocol" && sdkLogMethod(record) == "logging/setLevel") {
		record.Level = slog.LevelDebug
	}
	if !h.next.Enabled(ctx, record.Level) {
		return nil
	}
	sanitized := slog.NewRecord(record.Time, record.Level, logpkg.TruncBody([]byte(record.Message)), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		sanitized.AddAttrs(boundSDKLogAttr(attr))
		return true
	})
	return h.next.Handle(ctx, sanitized)
}

func sdkLogMethod(record slog.Record) string {
	var method string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "method" {
			method = attr.Value.String()
			return false
		}
		return true
	})
	return method
}

func boundSDKLogAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, logpkg.TruncBody([]byte(attr.Value.String())))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok {
			return slog.String(attr.Key, logpkg.TruncBody([]byte(err.Error())))
		}
		return slog.String(attr.Key, logpkg.RedactedTruncAny(attr.Value.Any()))
	case slog.KindGroup:
		group := attr.Value.Group()
		for i := range group {
			group[i] = boundSDKLogAttr(group[i])
		}
		return slog.Group(attr.Key, attrsToAny(group)...)
	default:
		return attr
	}
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for i := range attrs {
		values[i] = attrs[i]
	}
	return values
}

func (h *sdkLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bounded := make([]slog.Attr, len(attrs))
	for i := range attrs {
		bounded[i] = boundSDKLogAttr(attrs[i])
	}
	return &sdkLogHandler{next: h.next.WithAttrs(bounded)}
}

func (h *sdkLogHandler) WithGroup(name string) slog.Handler {
	return &sdkLogHandler{next: h.next.WithGroup(name)}
}

// attachCallerCorrelation copies caller-correlation values from ctx onto an
// analytics property map. clientSource is always set; the assistant IDs may
// be empty.
func attachCallerCorrelation(ctx context.Context, props map[string]any) {
	if source, ok := util.GetClientSource(ctx); ok && source != "" {
		props[analytics.AttrClientSource] = source
	}
	if threadID, ok := util.GetAssistantThreadID(ctx); ok && threadID != "" {
		props[analytics.AttrAssistantThreadID] = threadID
	}
	if executionID, ok := util.GetAssistantExecutionID(ctx); ok && executionID != "" {
		props[analytics.AttrAssistantExecutionID] = executionID
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
	if clientSource, ok := util.GetClientSource(parent); ok && clientSource != "" {
		ctx = util.SetClientSource(ctx, clientSource)
	}
	if threadID, ok := util.GetAssistantThreadID(parent); ok && threadID != "" {
		ctx = util.SetAssistantThreadID(ctx, threadID)
	}
	if executionID, ok := util.GetAssistantExecutionID(parent); ok && executionID != "" {
		ctx = util.SetAssistantExecutionID(ctx, executionID)
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
		logger:    log,
		handler:   handler,
		config:    cfg,
		analytics: a,
		meters:    meters,
	}
}

func (m *MCPServer) Run(ctx context.Context) error {
	s := m.newSDKServer()

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
	// without waiting on the 1-3 s bleve build.
	placeholderRegistry, err := docsindex.NewPlaceholderRegistry(ctx)
	if err != nil {
		return fmt.Errorf("initialize placeholder docs registry: %w", err)
	}
	m.handler.SetDocsIndex(placeholderRegistry)

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

	m.registerHandlers(s)

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

// registerHandlers publishes the full production catalog through one seam used
// by Run and by the SDK-independent wire compatibility oracle.
func (m *MCPServer) registerHandlers(s *mcp.Server) {
	m.handler.RegisterAllToolHandlers(s)
	m.handler.RegisterResourceTemplates(s)
	prompts.RegisterPrompts(func(prompt mcpcontract.Prompt, handler mcpcontract.PromptHandlerFunc) {
		m.handler.RegisterPrompt(s, prompt, handler)
	})
}

func (m *MCPServer) newSDKServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "SigNozMCP", Version: version.Version}, &mcp.ServerOptions{
		Instructions: instructions.ServerInstructions,
		Logger:       slog.New(&sdkLogHandler{next: m.logger.Handler()}),
		PageSize:     officialSDKPageSize,
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{},
			Resources: &mcp.ResourceCapabilities{},
			Prompts:   &mcp.PromptCapabilities{},
		},
	})
	s.AddReceivingMiddleware(m.receivingMiddleware(func(name string) bool {
		return m.handler != nil && m.handler.HasRegisteredTool(s, name)
	}))
	return s
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

func shouldObserveMethod(method string) bool {
	return method != "tools/call" && !strings.HasPrefix(method, "notifications/")
}

func methodErrorType(err error) string {
	if err == nil {
		return ""
	}

	var rpcErr *jsonrpc.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.CodeMethodNotFound:
		return "unsupported"
	case errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.CodeInvalidParams:
		return "invalid_params"
	default:
		return "internal"
	}
}

func methodErrorLogLevel(err error) slog.Level {
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.CodeMethodNotFound {
		return slog.LevelDebug
	}
	return logpkg.LevelForError(err)
}

func (m *MCPServer) completeMethodObservation(ctx context.Context, method string, started time.Time, err error) {
	ctx = context.WithoutCancel(ctx)
	span := trace.SpanFromContext(ctx)
	spanAttrs := []attribute.KeyValue{}
	spanAttrs = otelpkg.AppendTenantURL(ctx, spanAttrs)
	spanAttrs = otelpkg.AppendCallerCorrelation(ctx, spanAttrs)

	errorType := methodErrorType(err)
	if errorType != "" {
		spanAttrs = append(spanAttrs, attribute.String("error.type", errorType))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(spanAttrs...)

	if m.meters != nil {
		metricAttrs := []attribute.KeyValue{
			attribute.String("mcp.method.name", otelpkg.NormalizeMCPMethod(method)),
		}
		metricAttrs = otelpkg.AppendTenantURL(ctx, metricAttrs)
		metricAttrs = otelpkg.AppendClientSource(ctx, metricAttrs)
		if errorType != "" {
			metricAttrs = append(metricAttrs, attribute.String("error.type", errorType))
		}

		opts := metric.WithAttributes(metricAttrs...)
		m.meters.MethodCalls.Add(ctx, 1, opts)
		m.meters.MethodDuration.Record(ctx, float64(time.Since(started))/float64(time.Millisecond), opts)
	}
}

// maxBytesMiddleware bounds an inbound /mcp request body (config.MaxRequestBytes,
// default 4 MiB; env MCP_MAX_REQUEST_BYTES) so one oversized POST can't OOM the
// shared pod: a declared over-cap Content-Length is rejected early with 413,
// otherwise MaxBytesReader bounds the (possibly chunked) stream and an over-cap
// read surfaces downstream as a JSON-RPC parse error. The limit<=0 guard
// is defensive for directly-constructed configs (e.g. tests).
func (m *MCPServer) maxBytesMiddleware(next http.Handler) http.Handler {
	limit := int64(m.config.MaxRequestBytes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if limit > 0 {
			if r.ContentLength > limit {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *MCPServer) receivingMiddleware(isRegisteredTool func(string) bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (result mcp.Result, err error) {
			start := time.Now()
			observedMethod := otelpkg.NormalizeMCPMethod(method)
			ctx, span := otel.Tracer("signoz-mcp-server").Start(ctx, observedMethod, trace.WithSpanKind(trace.SpanKindServer))
			if req, ok := request.(*mcp.CallToolRequest); ok && req.Params != nil {
				var arguments any
				ctx, arguments = mcpcontract.CacheToolArguments(ctx, req.Params.Arguments)
				ctx = toolRequestContext(ctx, arguments)
			}
			recoveredPanic := false
			defer func() {
				if recovered := recover(); recovered != nil {
					recoveredPanic = true
					err = &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "Internal error"}
					result = nil
				}
				toolName := ""
				if method == "tools/call" {
					toolName = observedToolName(request, isRegisteredTool)
					ctx = util.SetToolName(ctx, toolName)
				}
				if err != nil {
					message := "mcp error"
					attrs := []any{slog.String("mcp.method.name", observedMethod), logpkg.BoundedErrAttr(err)}
					if recoveredPanic {
						message = "mcp handler panic recovered"
						attrs = append(attrs, slog.String("stack", logpkg.TruncBody(debug.Stack())))
					}
					m.logger.Log(ctx, methodErrorLogLevel(err), message, attrs...)
				}
				if method == "tools/call" {
					span.SetName("tools/call " + toolName)
					span.SetAttributes(otelpkg.GenAIOperationNameKey.String("execute_tool"), otelpkg.GenAIToolNameKey.String(toolName))
					result = m.completeToolObservation(ctx, request, result, err, start, toolName)
				} else if shouldObserveMethod(method) {
					m.completeMethodObservation(ctx, method, start, err)
				} else if err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
				}
				m.trackMethodAnalytics(ctx, method, request, result, err)
				span.End()
			}()

			spanAttrs := []attribute.KeyValue{otelpkg.MCPMethodKey.String(observedMethod)}
			spanAttrs = append(spanAttrs, requestTelemetryAttrs(request)...)
			spanAttrs = otelpkg.AppendTenantURL(ctx, spanAttrs)
			spanAttrs = otelpkg.AppendCallerCorrelation(ctx, spanAttrs)
			if searchContext, ok := util.GetSearchContext(ctx); ok && searchContext != "" {
				spanAttrs = append(spanAttrs, otelpkg.MCPSearchContextKey.String(searchContext))
			}
			span.SetAttributes(spanAttrs...)
			return next(ctx, method, request)
		}
	}
}

func requestTelemetryAttrs(request mcp.Request) []attribute.KeyValue {
	serverRequest, ok := request.(interface {
		ProtocolVersion() string
		ClientInfo() *mcp.Implementation
		ClientCapabilities() *mcp.ClientCapabilities
	})
	if !ok {
		return nil
	}
	attrs := make([]attribute.KeyValue, 0, 6)
	protocolVersion := serverRequest.ProtocolVersion()
	if protocolVersion == "" {
		if extra := request.GetExtra(); extra != nil {
			protocolVersion = strings.TrimSpace(extra.Header.Get("Mcp-Protocol-Version"))
		}
	}
	if protocolVersion != "" {
		attrs = append(attrs, otelpkg.MCPProtocolVersionKey.String(protocolVersion))
	}
	if clientInfo := serverRequest.ClientInfo(); clientInfo != nil {
		if clientInfo.Name != "" {
			attrs = append(attrs, otelpkg.MCPClientNameKey.String(util.NormalizeCallerCorrelationValue(clientInfo.Name)))
		}
		if clientInfo.Version != "" {
			attrs = append(attrs, otelpkg.MCPClientVersionKey.String(util.NormalizeCallerCorrelationValue(clientInfo.Version)))
		}
	}
	if capabilities := serverRequest.ClientCapabilities(); capabilities != nil {
		attrs = append(attrs,
			otelpkg.MCPClientRootsKey.Bool(capabilities.RootsV2 != nil),     //nolint:staticcheck // Legacy MCP clients may still advertise roots during the deprecation window.
			otelpkg.MCPClientSamplingKey.Bool(capabilities.Sampling != nil), //nolint:staticcheck // Legacy MCP clients may still advertise sampling during the deprecation window.
			otelpkg.MCPClientElicitationKey.Bool(capabilities.Elicitation != nil),
		)
	}
	return attrs
}

func attachRequestAnalytics(request mcp.Request, props map[string]any) {
	serverRequest, ok := request.(interface {
		ProtocolVersion() string
		ClientInfo() *mcp.Implementation
	})
	if !ok {
		return
	}
	if protocolVersion := serverRequest.ProtocolVersion(); protocolVersion != "" {
		props[analytics.AttrProtocolVersion] = protocolVersion
	}
	if clientInfo := serverRequest.ClientInfo(); clientInfo != nil {
		if clientInfo.Name != "" {
			props[analytics.AttrClientName] = util.NormalizeCallerCorrelationValue(clientInfo.Name)
		}
		if clientInfo.Version != "" {
			props[analytics.AttrClientVersion] = util.NormalizeCallerCorrelationValue(clientInfo.Version)
		}
	}
}

func observedToolName(request mcp.Request, isRegisteredTool func(string) bool) string {
	req, ok := request.(*mcp.CallToolRequest)
	if !ok || req.Params == nil || req.Params.Name == "" {
		return unknownToolName
	}
	if isRegisteredTool == nil || !isRegisteredTool(req.Params.Name) {
		return unknownToolName
	}
	return req.Params.Name
}

func toolRequestContext(ctx context.Context, arguments any) context.Context {
	if args, ok := arguments.(map[string]any); ok {
		if searchContext, _ := args["searchContext"].(string); searchContext != "" {
			return util.SetSearchContext(ctx, searchContext)
		}
	}
	return ctx
}

func (m *MCPServer) completeToolObservation(ctx context.Context, request mcp.Request, rawResult mcp.Result, err error, started time.Time, toolName string) mcp.Result {
	result, _ := rawResult.(*mcp.CallToolResult)

	var resultBytes int64
	if err == nil && result != nil {
		var marshalErr error
		resultBytes, marshalErr = serializedResultBytes(result)
		if marshalErr != nil {
			m.logger.ErrorContext(ctx, "tool result is not JSON serializable",
				slog.String("tool", toolName),
				logpkg.ErrAttr(marshalErr))
			*result = *tools.InternalErrorResult("Internal server error: tool result could not be serialized. Retry once; if it persists, report this as a server bug.")
			resultBytes, _ = serializedResultBytes(result)
		}
	}

	// Determine error status: either a Go error or an MCP tool result error.
	isErr := err != nil || (result != nil && result.IsError)
	errorType := toolOTelErrorType(err, result)
	errorCode := toolerrors.Code(result)
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(otelpkg.MCPToolIsErrorKey.Bool(isErr))
	if errorType != "" {
		span.SetAttributes(attribute.String("error.type", errorType))
	}
	if errorCode != "" {
		span.SetAttributes(otelpkg.MCPToolErrorCodeKey.String(errorCode))
	}
	// Always emit the result size — even zero — so it matches the log
	// field and downstream aggregations (avg, histogram) don't drop
	// empty-result tool calls as nulls.
	span.SetAttributes(otelpkg.MCPToolResultBytesKey.Int64(resultBytes))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if result != nil && result.IsError {
		errMsg := extractToolErrorMessage(result)
		span.RecordError(fmt.Errorf("%s", errMsg))
		span.SetStatus(codes.Error, errMsg)
	}

	duration := time.Since(started)
	sizeAttr := slog.Int64("mcp.tool.result.size_bytes", resultBytes)
	switch {
	case err != nil:
		// Client-driven cancellations (context.Canceled) log at DEBUG;
		// deadline-exceeded and real failures stay ERROR.
		level := logpkg.LevelForError(err)
		attrs := []any{
			slog.Duration("duration", duration),
			slog.Bool("mcp.tool.is_error", isErr),
			sizeAttr,
			logpkg.BoundedErrAttr(err),
		}
		if m.logger.Enabled(ctx, level) {
			attrs = append(attrs, slog.String("mcp.request", redactedRequestParams(request)))
		}
		m.logger.Log(ctx, level, "tool call failed", attrs...)
	case result != nil && result.IsError:
		attrs := []any{
			slog.Duration("duration", duration),
			slog.Bool("mcp.tool.is_error", isErr),
			sizeAttr,
			slog.String("error_message", logpkg.TruncBody([]byte(extractToolErrorMessage(result)))),
		}
		if m.logger.Enabled(ctx, slog.LevelWarn) {
			attrs = append(attrs, slog.String("mcp.request", redactedRequestParams(request)))
		}
		m.logger.WarnContext(ctx, "tool call returned error result", attrs...)
	default:
		m.logger.DebugContext(ctx, "tool call finished",
			slog.Duration("duration", duration),
			slog.Bool("mcp.tool.is_error", isErr),
			sizeAttr)
	}

	m.recordToolMetrics(ctx, toolName, isErr, errorType, errorCode, duration)
	m.trackToolCall(ctx, request, toolName, isErr, duration, toolAnalyticsErrorType(errorType, errorCode))
	if result != nil {
		return result
	}
	return rawResult
}

func (m *MCPServer) trackMethodAnalytics(ctx context.Context, method string, request mcp.Request, result mcp.Result, err error) {
	if err != nil {
		return
	}
	signozURL, ok := util.GetSigNozURL(ctx)
	if !ok || signozURL == "" {
		return
	}
	props := map[string]any{analytics.AttrTenantURL: signozURL}
	attachRequestAnalytics(request, props)
	var params mcp.Params
	if request != nil {
		params = request.GetParams()
	}
	switch method {
	case "initialize":
		if initializeParams, ok := params.(*mcp.InitializeParams); ok && initializeParams != nil && initializeParams.ClientInfo != nil {
			props[analytics.AttrClientName] = initializeParams.ClientInfo.Name
			props[analytics.AttrClientVersion] = initializeParams.ClientInfo.Version
			props[analytics.AttrProtocolVersion] = initializeParams.ProtocolVersion
		}
		if initialized, ok := result.(*mcp.InitializeResult); ok && initialized.ProtocolVersion != "" {
			props[analytics.AttrProtocolVersion] = initialized.ProtocolVersion
		}
		attachCallerCorrelation(ctx, props)
		m.trackEventAsync(ctx, analytics.EventClientInitialized, props)
	case "prompts/get":
		if promptParams, ok := params.(*mcp.GetPromptParams); ok && promptParams != nil {
			props[analytics.AttrPromptName] = promptParams.Name
			attachCallerCorrelation(ctx, props)
			m.trackEventAsync(ctx, analytics.EventPromptFetched, props)
		}
	case "resources/read":
		if resourceParams, ok := params.(*mcp.ReadResourceParams); ok && resourceParams != nil {
			props[analytics.AttrResourceURI] = resourceParams.URI
			attachCallerCorrelation(ctx, props)
			m.trackEventAsync(ctx, analytics.EventResourceFetched, props)
		}
	}
}

func redactedRequestParams(request mcp.Request) string {
	if request == nil {
		return logpkg.RedactedTruncAny(nil)
	}
	return logpkg.RedactedTruncAny(request.GetParams())
}

func (m *MCPServer) recordToolMetrics(ctx context.Context, toolName string, isErr bool, errorType, errorCode string, duration time.Duration) {
	if m.meters == nil {
		return
	}
	attrKVs := []attribute.KeyValue{
		otelpkg.GenAIToolNameKey.String(toolName),
		otelpkg.MCPToolIsErrorKey.Bool(isErr),
	}
	if errorType != "" {
		attrKVs = append(attrKVs, attribute.String("error.type", errorType))
	}
	if errorCode != "" {
		attrKVs = append(attrKVs, otelpkg.MCPToolErrorCodeKey.String(errorCode))
	}
	attrKVs = otelpkg.AppendTenantURL(ctx, attrKVs)
	attrKVs = otelpkg.AppendClientSource(ctx, attrKVs)
	opts := metric.WithAttributes(attrKVs...)
	m.meters.ToolCalls.Add(ctx, 1, opts)
	m.meters.ToolCallDuration.Record(ctx, float64(duration)/float64(time.Millisecond), opts)
}

func (m *MCPServer) trackToolCall(ctx context.Context, request mcp.Request, toolName string, isErr bool, duration time.Duration, errorType string) {
	signozURL, ok := util.GetSigNozURL(ctx)
	if !ok || signozURL == "" {
		return
	}
	props := map[string]any{
		analytics.AttrTenantURL:   signozURL,
		analytics.AttrToolName:    toolName,
		analytics.AttrToolIsError: isErr,
		analytics.AttrDurationMs:  duration.Milliseconds(),
	}
	if errorType != "" {
		props[analytics.AttrErrorType] = errorType
	}
	attachRequestAnalytics(request, props)
	attachCallerCorrelation(ctx, props)
	m.trackEventAsync(ctx, analytics.EventToolCalled, props)
}

func toolOTelErrorType(err error, result *mcp.CallToolResult) string {
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "timeout"
		case errors.Is(err, context.Canceled):
			return "cancelled"
		default:
			return "internal"
		}
	}
	if result != nil && result.IsError {
		return "tool_error"
	}
	return ""
}

// extractToolErrorMessage returns the text from the first Content entry of an
// MCP tool error result. Falls back to a generic message if the content is
// empty or not text.
func extractToolErrorMessage(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return "tool returned error result"
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok && tc.Text != "" {
		return tc.Text
	}
	return "tool returned error result"
}

func toolAnalyticsErrorType(errorType, errorCode string) string {
	if errorCode != "" {
		return strings.ToLower(errorCode)
	}
	return errorType
}

func serializedResultBytes(result *mcp.CallToolResult) (int64, error) {
	if result == nil {
		return 0, nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return 0, err
	}
	return int64(len(encoded)), nil
}

func (m *MCPServer) runStdio(ctx context.Context, s *mcp.Server) error {
	m.logger.InfoContext(ctx, "MCP Server running in stdio mode")
	ctx = util.SetAPIKey(ctx, m.config.APIKey)
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	ctx = util.SetSigNozURL(ctx, m.config.URL)
	ctx = util.SetClientSource(ctx, util.ClientSourceUserClient)
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// stripBearerPrefix removes a leading "Bearer " scheme token (case-insensitive,
// per RFC 7235 — SigNoz parses the scheme the same way) and trims surrounding
// whitespace, returning the bare token value.
func stripBearerPrefix(authValue string) string {
	const prefix = "Bearer "
	if len(authValue) >= len(prefix) && strings.EqualFold(authValue[:len(prefix)], prefix) {
		return strings.TrimSpace(authValue[len(prefix):])
	}
	return strings.TrimSpace(authValue)
}

const (
	authModeNone                = "none"
	authModeSignozAPIKeyHeader  = "signoz-api-key-header"
	authModeAuthorizationBearer = "authorization-bearer"
	authModeOAuthAccessToken    = "oauth-access-token"
	authModeConfigAPIKey        = "config-api-key"

	authFailureExpiredOAuthToken   = "expired_oauth_token"
	authFailureInvalidOAuthToken   = "invalid_oauth_token"
	authFailureInvalidSignozURL    = "invalid_signoz_url"
	authFailureMissingCredential   = "missing_credentials"
	authFailureMissingSignozURL    = "missing_signoz_url"
	authFailureDisallowedSignozURL = "disallowed_signoz_url"
)

// enforceInstanceURLAllowlist rejects a client-supplied SigNoz URL not in
// SIGNOZ_INSTANCE_URL_ALLOWLIST, returning false after writing the 403 + auth
// failure. signozURL must already be on ctx so the failure carries mcp.tenant_url.
func (m *MCPServer) enforceInstanceURLAllowlist(ctx context.Context, w http.ResponseWriter, r *http.Request, signozURL, authMode string) bool {
	if m.config.InstanceURLAllowlist.AllowsURL(signozURL) {
		return true
	}
	m.logAuthFailure(ctx, r, http.StatusForbidden, authFailureDisallowedSignozURL, authMode,
		"Tenant SigNoz URL is not permitted by the server allowlist", slog.String("mcp.tenant_url", signozURL))
	http.Error(w, util.InstanceURLNotPermittedMessage(), http.StatusForbidden)
	return false
}

func httpRequestSpanAttrs(r *http.Request) []attribute.KeyValue {
	if r == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", r.Method),
	}
	if r.URL != nil && r.URL.Path != "" {
		attrs = append(attrs, attribute.String("url.path", r.URL.Path))
	}
	if serverAddress := util.HTTPServerAddress(r); serverAddress != "" {
		attrs = append(attrs, attribute.String("server.address", serverAddress))
	}
	if clientAddress := util.HTTPClientAddress(r); clientAddress != "" {
		attrs = append(attrs, attribute.String("client.address", clientAddress))
	}
	if userAgent := util.HTTPUserAgent(r); userAgent != "" {
		attrs = append(attrs, attribute.String("user_agent.original", userAgent))
	}
	return attrs
}

func decorateAuthSpan(ctx context.Context, r *http.Request, authMode string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	spanAttrs := httpRequestSpanAttrs(r)
	if authMode != "" {
		spanAttrs = append(spanAttrs, attribute.String("mcp.auth.mode", authMode))
	}
	spanAttrs = append(spanAttrs, attrs...)
	span.SetAttributes(spanAttrs...)
}

func (m *MCPServer) logAuthFailure(ctx context.Context, r *http.Request, status int, reason, authMode, msg string, attrs ...slog.Attr) {
	decorateAuthSpan(ctx, r, authMode,
		attribute.Int("http.response.status_code", status),
		attribute.String("mcp.auth.failure_reason", reason),
	)
	if m.meters != nil {
		metricAttrs := []attribute.KeyValue{
			attribute.String("mcp.auth.failure_reason", reason),
			attribute.String("mcp.auth.mode", authMode),
		}
		metricAttrs = otelpkg.AppendTenantURL(ctx, metricAttrs)
		metricAttrs = otelpkg.AppendClientSource(ctx, metricAttrs)
		m.meters.AuthFailures.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
	}

	// Allowlist rejections are recorded on the metric and span only; the
	// per-request log would be noisy for a misconfigured/looping client.
	if reason == authFailureDisallowedSignozURL {
		return
	}

	logAttrs := []slog.Attr{
		slog.Int("http.response.status_code", status),
		slog.String("mcp.auth.failure_reason", reason),
		slog.String("mcp.auth.mode", authMode),
	}
	logAttrs = append(logAttrs, logpkg.HTTPRequestAttrs(r)...)
	logAttrs = append(logAttrs, attrs...)
	level := slog.LevelWarn
	if reason == authFailureExpiredOAuthToken || reason == authFailureMissingCredential {
		level = slog.LevelDebug
	}
	m.logger.LogAttrs(ctx, level, msg, logAttrs...)
}

func (m *MCPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Read correlation headers up front so every auth branch (including
		// 401/early-reject paths) propagates them. Values are advisory and
		// flow into every log/span/event, so they are normalized
		// (trim + length-cap) before being stashed.
		clientSource := util.NormalizeClientSource(r.Header.Get("X-SigNoz-Client-Source"))
		ctx = util.SetClientSource(ctx, clientSource)
		if threadID := util.NormalizeCallerCorrelationValue(r.Header.Get("X-SigNoz-Assistant-Thread-Id")); threadID != "" {
			ctx = util.SetAssistantThreadID(ctx, threadID)
		}
		if executionID := util.NormalizeCallerCorrelationValue(r.Header.Get("X-SigNoz-Assistant-Execution-Id")); executionID != "" {
			ctx = util.SetAssistantExecutionID(ctx, executionID)
		}

		// Apply to the otelhttp root span so 401/early-reject traces are
		// still queryable by caller.
		if rootSpan := trace.SpanFromContext(ctx); rootSpan.IsRecording() {
			rootSpan.SetAttributes(otelpkg.AppendCallerCorrelation(ctx, nil)...)
		}

		// Extract the direct-credential tenant header. A server-issued OAuth
		// token's encrypted tenant remains authoritative.
		customURL := r.Header.Get("X-SigNoz-URL")

		// SigNoz classifies credentials by header name, not token shape, so
		// each is forwarded on the header the client used: SIGNOZ-API-KEY
		// as-is, Authorization bearer tokens as Authorization (unless the
		// bearer is a server-issued OAuth access token, handled below).
		signozAPIKey := r.Header.Get("SIGNOZ-API-KEY")
		authHeader := r.Header.Get("Authorization")

		var apiKey string
		var signozURL string
		var usedOAuthToken bool
		authMode := authModeNone

		if signozAPIKey != "" {
			// Explicit PAT via SIGNOZ-API-KEY header — forward as-is.
			apiKey = stripBearerPrefix(signozAPIKey)
			authMode = authModeSignozAPIKeyHeader

			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
		} else if authHeader != "" {
			token := stripBearerPrefix(authHeader)
			if m.config.OAuthEnabled {
				decryptedAPIKey, decryptedURL, _, _, err := oauth.DecryptToken(token, []byte(m.config.OAuthTokenSecret))
				switch {
				case err == nil:
					apiKey = decryptedAPIKey
					signozURL = decryptedURL
					usedOAuthToken = true
					authMode = authModeOAuthAccessToken
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
				case errors.Is(err, oauth.ErrExpiredToken):
					authMode = authModeOAuthAccessToken
					// The token is expired but was once server-issued, so the
					// embedded URL is a trusted tenant value. Decorate the
					// otelhttp root span so the 401 trace carries mcp.tenant_url.
					if decryptedURL != "" {
						ctx = util.SetSigNozURL(ctx, decryptedURL)
						if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
							trace.SpanFromContext(ctx).SetAttributes(attr)
						}
					}
					m.logAuthFailure(ctx, r, http.StatusUnauthorized, authFailureExpiredOAuthToken, authMode, "OAuth access token expired")
					m.setOAuthChallenge(w, `error="invalid_token", error_description="access token expired"`)
					http.Error(w, "OAuth access token expired", http.StatusUnauthorized)
					return
				default:
					// Not an OAuth token. Forward as a direct credential only
					// when a SigNoz URL is available; otherwise a stale bearer
					// token would mask the OAuth challenge flow.
					if customURL == "" && m.config.URL == "" {
						m.logAuthFailure(ctx, r, http.StatusUnauthorized, authFailureInvalidOAuthToken, authModeAuthorizationBearer, "Bearer token did not match OAuth token format and no SigNoz URL is available for legacy fallback")
						m.setOAuthChallenge(w, `error="invalid_token", error_description="access token is invalid"`)
						http.Error(w, "OAuth access token is invalid", http.StatusUnauthorized)
						return
					}
					apiKey = "Bearer " + token
					authMode = authModeAuthorizationBearer
					ctx = util.SetAPIKey(ctx, apiKey)
					ctx = util.SetAuthHeader(ctx, "Authorization")
					m.logger.DebugContext(ctx, "Bearer token did not match OAuth token format, forwarding as Authorization")
				}
			} else {
				// OAuth disabled: honor the ingress header (Authorization).
				apiKey = "Bearer " + token
				authMode = authModeAuthorizationBearer
				ctx = util.SetAPIKey(ctx, apiKey)
				ctx = util.SetAuthHeader(ctx, "Authorization")
			}

		} else if m.config.APIKey != "" {
			// Fallback to config API key
			apiKey = m.config.APIKey
			authMode = authModeConfigAPIKey
			ctx = util.SetAPIKey(ctx, apiKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			m.logger.DebugContext(ctx, "Using API key from environment config")
		} else {
			m.logAuthFailure(ctx, r, http.StatusUnauthorized, authFailureMissingCredential, authMode, "No API key found in headers or environment")
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
			if !m.enforceInstanceURLAllowlist(ctx, w, r, signozURL, authMode) {
				return
			}
			decorateAuthSpan(ctx, r, authMode)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}

		// Determine final URL with precedence: X-SigNoz-URL header > config URL
		if customURL != "" {
			trimmed := strings.TrimSuffix(customURL, "/")
			normalized, err := util.NormalizeSigNozURL(trimmed)
			if err != nil {
				m.logAuthFailure(ctx, r, http.StatusBadRequest, authFailureInvalidSignozURL, authMode, "Invalid X-SigNoz-URL header",
					slog.String("url", customURL), logpkg.ErrAttr(err))
				http.Error(w, fmt.Sprintf("Invalid X-SigNoz-URL: %v", err), http.StatusBadRequest)
				return
			}
			// Set the SigNoz URL on ctx first so an allowlist rejection is attributed.
			ctx = util.SetSigNozURL(ctx, normalized)
			if !m.enforceInstanceURLAllowlist(ctx, w, r, normalized, authMode) {
				return
			}
			signozURL = normalized
		} else if m.config.URL != "" {
			signozURL = m.config.URL
			m.logger.DebugContext(ctx, "Using URL from environment config", slog.String("mcp.tenant_url", signozURL))
		} else {
			m.logAuthFailure(ctx, r, http.StatusBadRequest, authFailureMissingSignozURL, authMode, "No SigNoz URL found in X-SigNoz-URL header or environment")
			http.Error(w, "SigNoz instance URL is required", http.StatusBadRequest)
			return
		}

		ctx = util.SetSigNozURL(ctx, signozURL)

		// Decorate the otelhttp root span with tenant_url so every /mcp
		// request trace is queryable per customer in SigNoz.
		if attr, ok := otelpkg.TenantURLAttr(ctx); ok {
			trace.SpanFromContext(ctx).SetAttributes(attr)
		}
		decorateAuthSpan(ctx, r, authMode)

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func (m *MCPServer) buildHTTP(s *mcp.Server) *http.Server {
	m.logger.Info("MCP Server running in HTTP mode")

	addr := net.JoinHostPort(m.config.Host, m.config.Port)

	mux := http.NewServeMux()

	// /livez is the shallow process-liveness probe. Do not check dependencies
	// here; failing liveness tells Kubernetes to restart the container.
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	// Readiness/health are stricter than liveness: Kubernetes should only route
	// traffic to pods that can serve docs tools without INDEX_NOT_READY. /healthz
	// is kept as a legacy generic health endpoint, matching SigNoz's API shape.
	readyHandler := func(w http.ResponseWriter, r *http.Request) {
		if m.handler == nil || !m.handler.DocsIndexReady() {
			http.Error(w, "docs index not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}
	mux.HandleFunc("/readyz", readyHandler)
	mux.HandleFunc("/healthz", readyHandler)

	if m.config.OAuthEnabled {
		oauthHandler := oauth.NewHandler(m.logger, m.config, m.trackOAuthEvent, m.meters)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", oauthHandler.HandleProtectedResourceMetadata)
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauthHandler.HandleAuthorizationServerMetadata)
		mux.HandleFunc("POST /oauth/register", oauthHandler.HandleRegisterClient)
		mux.HandleFunc("GET /oauth/authorize", oauthHandler.HandleAuthorizePage)
		mux.HandleFunc("POST /oauth/authorize", oauthHandler.HandleAuthorizeSubmit)
		mux.HandleFunc("POST /oauth/token", oauthHandler.HandleToken)
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, m.streamableHTTPOptions())
	protectedMCPHandler := http.NewCrossOriginProtection().Handler(m.maxBytesMiddleware(m.authMiddleware(mcpHandler)))
	mux.Handle("/mcp", protectedMCPHandler)

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
		// WriteTimeout remains 0 because a long-running tool call may legitimately
		// exceed the request read timeout. IdleTimeout inherits the same default.
		MaxHeaderBytes: 1 << 20, // 1 MB
	}

	return srv
}

func (m *MCPServer) streamableHTTPOptions() *mcp.StreamableHTTPOptions {
	limit := int64(m.config.MaxRequestBytes)
	if limit <= 0 {
		limit = -1
	}
	return &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		Logger:                       slog.New(&sdkLogHandler{next: m.logger.Handler()}),
		MaxRequestBodyBytes:          limit,
		PropagateRequestCancellation: true,
	}
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
