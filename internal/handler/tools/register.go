package tools

import (
	"github.com/mark3labs/mcp-go/server"
)

// RegisterAllToolHandlers registers every tool handler group on s. It is the
// single source of truth for tool registration: production
// (internal/mcp-server) and the schema/annotation inventory tests all
// register through it, so a new handler group cannot reach the server
// without also passing the pinned-inventory tests.
func (h *Handler) RegisterAllToolHandlers(s *server.MCPServer) {
	h.RegisterMetricsHandlers(s)
	h.RegisterTopMetricsHandlers(s)
	h.RegisterMetricUsageHandlers(s)
	h.RegisterFieldsHandlers(s)
	h.RegisterAlertsHandlers(s)
	h.RegisterDashboardHandlers(s)
	h.RegisterServiceHandlers(s)
	h.RegisterQueryBuilderV5Handlers(s)
	h.RegisterLogsHandlers(s)
	h.RegisterViewHandlers(s)
	h.RegisterDocsHandlers(s)
	h.RegisterTracesHandlers(s)
	h.RegisterNotificationChannelHandlers(s)
	h.RegisterMetricCardinalityHandlers(s)
}
