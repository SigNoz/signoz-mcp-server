package otel

import (
	"go.opentelemetry.io/otel/metric"
)

type Meters struct {
	ToolCalls           metric.Int64Counter
	ToolCallDuration    metric.Float64Histogram
	SessionRegistered   metric.Int64Counter
	OAuthEvents         metric.Int64Counter
	IdentityCacheHits   metric.Int64Counter
	IdentityCacheMisses metric.Int64Counter
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

	return &Meters{
		ToolCalls:           toolCalls,
		ToolCallDuration:    toolCallDuration,
		SessionRegistered:   sessionRegistered,
		OAuthEvents:         oauthEvents,
		IdentityCacheHits:   identityCacheHits,
		IdentityCacheMisses: identityCacheMisses,
	}, nil
}
