package mcp_server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/handler/tools"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type MCPServer struct {
	logger       *zap.Logger
	handler      *tools.Handler
	config       *config.Config
	rateLimiters *expirable.LRU[string, *rate.Limiter]
}

func NewMCPServer(log *zap.Logger, handler *tools.Handler, cfg *config.Config) *MCPServer {
	// Reuse the same cache size/TTL config for rate limiter entries so they
	// are bounded and auto-expire, matching the client cache lifecycle.
	limiters := expirable.NewLRU[string, *rate.Limiter](
		cfg.ClientCacheSize, nil, cfg.ClientCacheTTL,
	)
	return &MCPServer{logger: log, handler: handler, config: cfg, rateLimiters: limiters}
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

	// Inject env-configured credentials into every request context
	// so that GetClient works uniformly across both transports.
	ctxFunc := server.WithStdioContextFunc(func(ctx context.Context) context.Context {
		ctx = util.SetAPIKey(ctx, m.config.APIKey)
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
			apiKey = m.config.APIKey
			ctx = util.SetAPIKey(ctx, apiKey)
			m.logger.Debug("Using API key from environment config")
		} else {
			m.logger.Warn("No API key found in Authorization header or environment")
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		// Determine final URL with precedence: X-SigNoz-URL header > config URL
		if customURL != "" {
			normalized, err := normalizeSigNozURL(strings.TrimSuffix(customURL, "/"))
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

		// Per-tenant rate limiting keyed by hashed apiKey:signozURL
		tenantKey := util.HashTenantKey(apiKey, signozURL)
		limiter, ok := m.rateLimiters.Get(tenantKey)
		if !ok {
			limiter = rate.NewLimiter(
				rate.Limit(m.config.RateLimitPerTenant),
				m.config.RateLimitBurst,
			)
			m.rateLimiters.Add(tenantKey, limiter)
		}
		if !limiter.Allow() {
			m.logger.Warn("Rate limit exceeded for tenant",
				zap.String("url", signozURL))
			http.Error(w, "Rate limit exceeded. Try again later.", http.StatusTooManyRequests)
			return
		}

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

// privateIPNets defines CIDR ranges that are not allowed as SigNoz URL targets.
var privateIPNets = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 unique local
		"fe80::/10",      // IPv6 link-local
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, _ := net.ParseCIDR(cidr)
		nets = append(nets, n)
	}
	return nets
}()

// normalizeSigNozURL validates that rawURL is safe to use as a SigNoz backend
// target and returns the canonical origin form (scheme://host[:port]).
// It blocks non-HTTP schemes, private/reserved IPs, and localhost to prevent
// SSRF. Default ports (80 for http, 443 for https) are stripped so that
// equivalent URLs like https://example.com and https://example.com:443
// produce the same origin string for caching and rate-limiting.
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

	// Resolve the hostname and check every IP against private ranges
	ips, err := net.LookupHost(host)
	if err != nil {
		return "", fmt.Errorf("cannot resolve host %q: %w", host, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		// Normalize IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1) to
		// their 4-byte IPv4 form. Without this, net.IPNet.Contains silently
		// returns false when comparing a 16-byte IP against a 4-byte CIDR,
		// allowing SSRF via addresses like http://[::ffff:10.0.0.1]/.
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		for _, n := range privateIPNets {
			if n.Contains(ip) {
				return "", fmt.Errorf("host %q resolves to private address %s", host, ipStr)
			}
		}
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
