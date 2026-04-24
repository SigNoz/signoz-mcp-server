package otel

import (
	"go.opentelemetry.io/otel/metric"
)

type Meters struct {
	ToolCalls           metric.Int64Counter
	ToolCallDuration    metric.Float64Histogram
	MethodCalls         metric.Int64Counter
	MethodDuration      metric.Float64Histogram
	SessionRegistered   metric.Int64Counter
	OAuthEvents         metric.Int64Counter
	OAuthFailures       metric.Int64Counter
	IdentityCacheHits   metric.Int64Counter
	IdentityCacheMisses metric.Int64Counter
	DocsSearches        metric.Int64Counter
	DocsSearchDuration  metric.Float64Histogram
	DocsFetches         metric.Int64Counter
	DocsRefreshes       metric.Int64Counter
	DocsRefreshDuration metric.Float64Histogram
	DocsIndexAge        metric.Float64Gauge
	DocsIndexSizeBytes  metric.Int64Gauge
	DocsIndexDocCount   metric.Int64Gauge
	DocsIndexGeneration metric.Int64Gauge
	DocsFetcherRetries  metric.Int64Counter
	DocsSitemapFailures metric.Int64Counter
	DocsRateLimited     metric.Int64Counter
}

func NewMeters(mp metric.MeterProvider) (*Meters, error) {
	meter := mp.Meter("github.com/SigNoz/signoz-mcp-server")

	toolCalls, err := meter.Int64Counter(
		"mcp.tool.calls",
		metric.WithDescription("Count of MCP tool calls"),
	)
	if err != nil {
		return nil, err
	}

	toolCallDuration, err := meter.Float64Histogram(
		"mcp.tool.call.duration",
		metric.WithDescription("Duration of MCP tool calls"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	methodCalls, err := meter.Int64Counter(
		"mcp.method.calls",
		metric.WithDescription("Count of non-tool MCP method calls"),
	)
	if err != nil {
		return nil, err
	}

	methodDuration, err := meter.Float64Histogram(
		"mcp.method.duration",
		metric.WithDescription("Duration of non-tool MCP method calls"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}

	sessionRegistered, err := meter.Int64Counter(
		"mcp.session.registered",
		metric.WithDescription("Count of MCP sessions successfully registered"),
	)
	if err != nil {
		return nil, err
	}

	oauthEvents, err := meter.Int64Counter(
		"mcp.oauth.events",
		metric.WithDescription("Count of OAuth events emitted by the MCP server"),
	)
	if err != nil {
		return nil, err
	}

	oauthFailures, err := meter.Int64Counter(
		"mcp.oauth.failures",
		metric.WithDescription("Count of OAuth failures emitted by the MCP server"),
	)
	if err != nil {
		return nil, err
	}

	identityCacheHits, err := meter.Int64Counter(
		"mcp.identity_cache.hit",
		metric.WithDescription("Count of analytics identity cache hits"),
	)
	if err != nil {
		return nil, err
	}

	identityCacheMisses, err := meter.Int64Counter(
		"mcp.identity_cache.miss",
		metric.WithDescription("Count of analytics identity cache misses"),
	)
	if err != nil {
		return nil, err
	}

	docsSearches, err := meter.Int64Counter("signoz_docs_searches_total", metric.WithDescription("Count of SigNoz docs searches"))
	if err != nil {
		return nil, err
	}
	docsSearchDuration, err := meter.Float64Histogram("signoz_docs_search_duration_seconds", metric.WithDescription("Duration of SigNoz docs searches"), metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	docsFetches, err := meter.Int64Counter("signoz_docs_fetches_total", metric.WithDescription("Count of SigNoz docs fetches"))
	if err != nil {
		return nil, err
	}
	docsRefreshes, err := meter.Int64Counter("signoz_docs_refresh_total", metric.WithDescription("Count of SigNoz docs refresh outcomes"))
	if err != nil {
		return nil, err
	}
	docsRefreshDuration, err := meter.Float64Histogram("signoz_docs_refresh_duration_seconds", metric.WithDescription("Duration of SigNoz docs refreshes"), metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	docsIndexAge, err := meter.Float64Gauge("signoz_docs_index_age_seconds", metric.WithDescription("Age of the active SigNoz docs index"), metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	docsIndexSizeBytes, err := meter.Int64Gauge("signoz_docs_index_size_bytes", metric.WithDescription("Approximate byte size of indexed SigNoz docs corpus"), metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	docsIndexDocCount, err := meter.Int64Gauge("signoz_docs_index_doc_count", metric.WithDescription("Number of pages in the active SigNoz docs index"))
	if err != nil {
		return nil, err
	}
	docsIndexGeneration, err := meter.Int64Gauge("signoz_docs_index_generation", metric.WithDescription("Active SigNoz docs index generation"))
	if err != nil {
		return nil, err
	}
	docsFetcherRetries, err := meter.Int64Counter("signoz_docs_fetcher_retries_total", metric.WithDescription("Count of SigNoz docs fetcher retries"))
	if err != nil {
		return nil, err
	}
	docsSitemapFailures, err := meter.Int64Counter("signoz_docs_sitemap_parse_failures_total", metric.WithDescription("Count of SigNoz docs sitemap parse failures"))
	if err != nil {
		return nil, err
	}
	docsRateLimited, err := meter.Int64Counter("signoz_docs_rate_limited_total", metric.WithDescription("Count of public docs requests rejected by rate limiting"))
	if err != nil {
		return nil, err
	}

	return &Meters{
		ToolCalls:           toolCalls,
		ToolCallDuration:    toolCallDuration,
		MethodCalls:         methodCalls,
		MethodDuration:      methodDuration,
		SessionRegistered:   sessionRegistered,
		OAuthEvents:         oauthEvents,
		OAuthFailures:       oauthFailures,
		IdentityCacheHits:   identityCacheHits,
		IdentityCacheMisses: identityCacheMisses,
		DocsSearches:        docsSearches,
		DocsSearchDuration:  docsSearchDuration,
		DocsFetches:         docsFetches,
		DocsRefreshes:       docsRefreshes,
		DocsRefreshDuration: docsRefreshDuration,
		DocsIndexAge:        docsIndexAge,
		DocsIndexSizeBytes:  docsIndexSizeBytes,
		DocsIndexDocCount:   docsIndexDocCount,
		DocsIndexGeneration: docsIndexGeneration,
		DocsFetcherRetries:  docsFetcherRetries,
		DocsSitemapFailures: docsSitemapFailures,
		DocsRateLimited:     docsRateLimited,
	}, nil
}
