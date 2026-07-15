package otel

import (
	"context"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"go.opentelemetry.io/otel/attribute"
)

// GenAI semantic convention attribute keys — used on tool-execution spans
// to describe "which tool, what operation." We deliberately do NOT set
// gen_ai.provider.name (formerly gen_ai.system) because MCP is a tool
// protocol, not a GenAI model provider.
// See: https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/
const (
	GenAIOperationNameKey = attribute.Key("gen_ai.operation.name")
	GenAIToolNameKey      = attribute.Key("gen_ai.tool.name")
)

// MCP semantic convention attribute keys.
// Spec: https://opentelemetry.io/docs/specs/semconv/registry/attributes/mcp/
//
// MCPMethodKey and MCPProtocolVersionKey match the registry exactly. The other
// keys (search_context, tenant_url, tool.*, query.payload) are custom
// extensions this server uses for multi-tenant attribution and are not
// defined by the spec.
const (
	MCPMethodKey          = attribute.Key("mcp.method.name")
	MCPProtocolVersionKey = attribute.Key("mcp.protocol.version")
	MCPSearchContextKey   = attribute.Key("mcp.search_context")
	MCPTenantURLKey       = attribute.Key("mcp.tenant_url")
	MCPToolIsErrorKey     = attribute.Key("mcp.tool.is_error")
	MCPToolErrorCodeKey   = attribute.Key("mcp.tool.error.code")
	MCPQueryPayloadKey    = attribute.Key("mcp.query.payload")
	// MCPToolResultBytes approximates the size, in bytes, of the text content
	// returned by a tool call — sum of `len(Text)` across TextContent entries.
	// Non-standard (the registry has no equivalent today); scoped under the
	// mcp.tool.* namespace used by this server's other tool-call attrs.
	MCPToolResultBytesKey = attribute.Key("mcp.tool.result.size_bytes")
	// ClientSource is low-cardinality (categorical) and safe on metrics; the
	// two assistant IDs are per-execution UUIDs and MUST NOT be applied as
	// metric attributes.
	MCPClientSourceKey         = attribute.Key("mcp.client_source")
	MCPAssistantThreadIDKey    = attribute.Key("mcp.assistant.thread_id")
	MCPAssistantExecutionIDKey = attribute.Key("mcp.assistant.execution_id")
)

// TenantURLAttr returns mcp.tenant_url as an OTel attribute when the context
// carries a SigNoz URL. Returns the zero KeyValue and false when absent, so
// callers can append conditionally without emitting an empty string value.
func TenantURLAttr(ctx context.Context) (attribute.KeyValue, bool) {
	signozURL, ok := util.GetSigNozURL(ctx)
	if !ok || signozURL == "" {
		return attribute.KeyValue{}, false
	}
	return MCPTenantURLKey.String(signozURL), true
}

// AppendTenantURL appends the mcp.tenant_url attribute to attrs when the
// context carries a SigNoz URL. Returns attrs unchanged when absent.
func AppendTenantURL(ctx context.Context, attrs []attribute.KeyValue) []attribute.KeyValue {
	if attr, ok := TenantURLAttr(ctx); ok {
		return append(attrs, attr)
	}
	return attrs
}

// ClientSourceAttr returns mcp.client_source as an OTel attribute when the
// context carries one.
func ClientSourceAttr(ctx context.Context) (attribute.KeyValue, bool) {
	source, ok := util.GetClientSource(ctx)
	if !ok || source == "" {
		return attribute.KeyValue{}, false
	}
	return MCPClientSourceKey.String(source), true
}

// AppendClientSource appends mcp.client_source. Safe on both span and metric
// attribute lists — client_source is bounded categorical.
func AppendClientSource(ctx context.Context, attrs []attribute.KeyValue) []attribute.KeyValue {
	if attr, ok := ClientSourceAttr(ctx); ok {
		return append(attrs, attr)
	}
	return attrs
}

// AppendCallerCorrelation appends client_source plus the assistant thread/execution
// IDs when present. Use ONLY for span attributes — assistant IDs are
// per-execution UUIDs and would blow up cardinality on metric counters.
func AppendCallerCorrelation(ctx context.Context, attrs []attribute.KeyValue) []attribute.KeyValue {
	attrs = AppendClientSource(ctx, attrs)
	if threadID, ok := util.GetAssistantThreadID(ctx); ok && threadID != "" {
		attrs = append(attrs, MCPAssistantThreadIDKey.String(threadID))
	}
	if executionID, ok := util.GetAssistantExecutionID(ctx); ok && executionID != "" {
		attrs = append(attrs, MCPAssistantExecutionIDKey.String(executionID))
	}
	return attrs
}
