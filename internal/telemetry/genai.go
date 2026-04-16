package telemetry

import "go.opentelemetry.io/otel/attribute"

// GenAI semantic convention attribute keys.
// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/
const (
	GenAISystemKey        = attribute.Key("gen_ai.system")
	GenAIOperationNameKey = attribute.Key("gen_ai.operation.name")
	GenAIToolNameKey      = attribute.Key("gen_ai.tool.name")
	GenAIToolCallIDKey    = attribute.Key("gen_ai.tool.call.id")
)

// MCP-specific attribute keys.
const (
	MCPMethodKey        = attribute.Key("mcp.method")
	MCPSessionIDKey     = attribute.Key("mcp.session.id")
	MCPSearchContextKey = attribute.Key("mcp.search_context")
	MCPTenantURLKey     = attribute.Key("mcp.tenant_url")
	MCPToolIsErrorKey   = attribute.Key("mcp.tool.is_error")
	MCPQueryPayloadKey  = attribute.Key("mcp.query.payload")
)

// GenAISystemMCP is the gen_ai.system value for MCP tool servers.
const GenAISystemMCP = "mcp"
