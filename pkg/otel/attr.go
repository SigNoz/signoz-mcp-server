package otel

import "go.opentelemetry.io/otel/attribute"

// GenAI semantic convention attribute keys — used on tool-execution spans
// to describe "which tool, what operation." We deliberately do NOT set
// gen_ai.provider.name (formerly gen_ai.system) because MCP is a tool
// protocol, not a GenAI model provider.
// See: https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/
const (
	GenAIOperationNameKey = attribute.Key("gen_ai.operation.name")
	GenAIToolNameKey      = attribute.Key("gen_ai.tool.name")
	GenAIToolCallIDKey    = attribute.Key("gen_ai.tool.call.id")
)

// MCP semantic convention attribute keys.
// Spec: https://opentelemetry.io/docs/specs/semconv/registry/attributes/mcp/
//
// MCPMethodKey, MCPSessionIDKey match the registry exactly. The other keys
// (search_context, tenant_url, tool.is_error, query.payload) are custom
// extensions this server uses for multi-tenant attribution and are not
// defined by the spec.
const (
	MCPMethodKey        = attribute.Key("mcp.method.name")
	MCPSessionIDKey     = attribute.Key("mcp.session.id")
	MCPSearchContextKey = attribute.Key("mcp.search_context")
	MCPTenantURLKey     = attribute.Key("mcp.tenant_url")
	MCPToolIsErrorKey   = attribute.Key("mcp.tool.is_error")
	MCPQueryPayloadKey  = attribute.Key("mcp.query.payload")
)
