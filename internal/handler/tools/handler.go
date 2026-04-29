package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type Handler struct {
	logger        *slog.Logger
	clientCache   *expirable.LRU[string, *signozclient.SigNoz]
	configURL     string
	customHeaders map[string]string
	meters        *otelpkg.Meters
	docsIndex     *docsindex.IndexRegistry

	// clientOverride, when non-nil, is returned by GetClient instead of
	// looking up the cache. This exists solely to support unit testing
	// with mock clients.
	clientOverride signozclient.Client
}

func (h *Handler) SetMeters(meters *otelpkg.Meters) {
	h.meters = meters
}

func (h *Handler) SetDocsIndex(registry *docsindex.IndexRegistry) {
	h.docsIndex = registry
}

func (h *Handler) DocsIndexReady() bool {
	return h != nil && h.docsIndex != nil && h.docsIndex.Ready()
}

func NewHandler(log *slog.Logger, cfg *config.Config) *Handler {
	// Normalize the configured URL so that the URL comparison in GetClient
	// works reliably (e.g. https://example.com:443 == https://example.com).
	normalizedURL := cfg.URL
	if n, err := util.NormalizeSigNozURL(cfg.URL); err == nil {
		normalizedURL = n
	}

	return &Handler{
		logger:        log,
		clientCache:   expirable.NewLRU[string, *signozclient.SigNoz](cfg.ClientCacheSize, nil, cfg.ClientCacheTTL),
		configURL:     normalizedURL,
		customHeaders: cfg.CustomHeaders,
	}
}

// GetClient returns a cached SigNoz client for the tenant identified by
// the apiKey and signozURL stored in the request context.
// Both stdio and HTTP transports guarantee these values are present
// in the context before any tool handler is called.
func (h *Handler) GetClient(ctx context.Context) (signozclient.Client, error) {
	if h.clientOverride != nil {
		return h.clientOverride, nil
	}

	apiKey, _ := util.GetAPIKey(ctx)
	signozURL, _ := util.GetSigNozURL(ctx)
	authHeader, _ := util.GetAuthHeader(ctx)

	if apiKey == "" || signozURL == "" {
		return nil, fmt.Errorf("missing tenant credentials in context (apiKey or signozURL)")
	}

	if authHeader == "" {
		authHeader = "SIGNOZ-API-KEY"
	}

	cacheKey := util.HashTenantKey(apiKey, signozURL)

	if cachedClient, ok := h.clientCache.Get(cacheKey); ok {
		return cachedClient, nil
	}

	// Only attach custom headers when the tenant URL matches the configured
	// SIGNOZ_URL to prevent leaking proxy-auth credentials (e.g. Cloudflare
	// Access tokens) to arbitrary third-party hosts.
	var headers map[string]string
	if strings.EqualFold(signozURL, h.configURL) {
		headers = h.customHeaders
	}

	h.logger.DebugContext(ctx, "Creating new SigNoz client for tenant")
	newClient := signozclient.NewClient(h.logger, signozURL, apiKey, authHeader, headers)
	newClient.SetMeters(h.meters)
	h.clientCache.Add(cacheKey, newClient)
	return newClient, nil
}
