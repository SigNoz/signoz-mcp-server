package tools

import (
	"context"
	"encoding/json"
	"strings"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (h *Handler) RegisterDataRetentionHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering data retention handlers")

	tool := mcp.NewTool("signoz_get_data_retention",
		mcp.WithOutputSchema[signozclient.DataRetention](),
		withReadOnlyToolAnnotations(),
		mcp.WithDescription(
			"Use this when the user wants the workspace's configured deletion-retention periods for metrics, traces, and logs. It returns current defaults in hours, active custom log overrides, and change status. Retention changes apply only to newly ingested data; older data can retain an earlier TTL. If currentStateKnown is false, report current retention as unknown and direct the user to webUrl; do not treat a pending or failed attempt as active. This tool returns retention only; for ingestion volume, use signoz_list_metrics with source=\"meter\". The call reads configuration and makes no changes. Example arguments: {\"searchContext\":\"What retention is configured for metrics, traces, and logs?\"}."),
		mcp.WithString("searchContext",
			mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
	)

	h.addTool(s, tool, h.handleGetDataRetention)
}

func (h *Handler) handleGetDataRetention(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_get_data_retention")

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	retention, err := client.GetDataRetention(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to fetch data retention", err)
		return upstreamError(err), nil
	}

	if base, ok := util.GetSigNozURL(ctx); ok && strings.TrimSpace(base) != "" {
		retention.WebURL = strings.TrimRight(base, "/") + "/settings"
	}

	out, err := json.Marshal(retention)
	if err != nil {
		return InternalErrorResult(err.Error()), nil
	}
	return structuredResult(out), nil
}
