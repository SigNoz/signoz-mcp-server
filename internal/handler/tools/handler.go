package tools

import (
	"context"
	"fmt"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	"go.uber.org/zap"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/telemetry"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

type Handler struct {
	logger      *zap.Logger
	clientCache *expirable.LRU[string, *signozclient.SigNoz]
}

func NewHandler(log *zap.Logger, cfg *config.Config) *Handler {
	return &Handler{
		logger:      log,
		clientCache: expirable.NewLRU[string, *signozclient.SigNoz](cfg.ClientCacheSize, nil, cfg.ClientCacheTTL),
	}
}

// tenantLogger returns a logger enriched with tenant-identifying fields
// extracted from the request context. Safe to call even when the context
// is missing credentials — fields are simply omitted.
func (h *Handler) tenantLogger(ctx context.Context) *zap.Logger {
	l := h.logger
	if signozURL, ok := util.GetSigNozURL(ctx); ok && signozURL != "" {
		l = telemetry.LoggerWithURL(l, signozURL)
	}
	return l
}

// GetClient returns a cached SigNoz client for the tenant identified by
// the apiKey and signozURL stored in the request context.
// Both stdio and HTTP transports guarantee these values are present
// in the context before any tool handler is called.
func (h *Handler) GetClient(ctx context.Context) (signozclient.Client, error) {
	apiKey, _ := util.GetAPIKey(ctx)
	signozURL, _ := util.GetSigNozURL(ctx)

	if apiKey == "" || signozURL == "" {
		return nil, fmt.Errorf("missing tenant credentials in context (apiKey or signozURL)")
	}

	cacheKey := util.HashTenantKey(apiKey, signozURL)

	if cachedClient, ok := h.clientCache.Get(cacheKey); ok {
		return cachedClient, nil
	}

	h.logger.Debug("Creating new SigNoz client for tenant",
		zap.String("url", signozURL))
	newClient := signozclient.NewClient(h.logger, signozURL, apiKey)
	h.clientCache.Add(cacheKey, newClient)
	return newClient, nil
}
