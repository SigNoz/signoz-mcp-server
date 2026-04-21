package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// RegisterResourceTemplates registers dynamic MCP resource templates.
func (h *Handler) RegisterResourceTemplates(s *server.MCPServer) {
	h.logger.Debug("Registering resource templates")

	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"signoz://alert/{ruleId}/summary",
			"Alert Summary",
			mcp.WithTemplateDescription("Get alert configuration and recent history for a specific alert rule."),
			mcp.WithTemplateMIMEType("application/json"),
		),
		h.handleAlertSummaryResource,
	)

	s.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"signoz://dashboard/{uuid}/summary",
			"Dashboard Summary",
			mcp.WithTemplateDescription("Get dashboard metadata and widget list for a specific dashboard."),
			mcp.WithTemplateMIMEType("application/json"),
		),
		h.handleDashboardSummaryResource,
	)
}

func (h *Handler) handleAlertSummaryResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	ruleID := extractURIParam(req.Params.URI, "signoz://alert/", "/summary")
	if ruleID == "" {
		return nil, fmt.Errorf("missing ruleId in URI")
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return nil, err
	}

	h.logger.DebugContext(ctx, "Fetching alert summary resource", slog.String("ruleId", ruleID))

	alertData, err := client.GetAlertByRuleID(ctx, ruleID)
	if err != nil {
		return nil, fmt.Errorf("failed to get alert: %w", err)
	}

	historyReq := types.AlertHistoryRequest{
		Start:  timeutil.HoursAgoMillis(6),
		End:    timeutil.NowMillis(),
		Order:  "desc",
		Limit:  10,
		Offset: 0,
	}
	historyData, err := client.GetAlertHistory(ctx, ruleID, historyReq)
	if err != nil {
		// History fetch is best-effort; include alert data even if history fails
		h.logger.WarnContext(ctx, "Failed to get alert history", slog.String("ruleId", ruleID), logpkg.ErrAttr(err))
	}

	summary := map[string]json.RawMessage{
		"alert": alertData,
	}
	if historyData != nil {
		summary["recentHistory"] = historyData
	}

	data, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal summary: %w", err)
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

func (h *Handler) handleDashboardSummaryResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	uuid := extractURIParam(req.Params.URI, "signoz://dashboard/", "/summary")
	if uuid == "" {
		return nil, fmt.Errorf("missing uuid in URI")
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return nil, err
	}

	h.logger.DebugContext(ctx, "Fetching dashboard summary resource", slog.String("uuid", uuid))

	dashData, err := client.GetDashboard(ctx, uuid)
	if err != nil {
		return nil, fmt.Errorf("failed to get dashboard: %w", err)
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(dashData),
		},
	}, nil
}

// extractURIParam extracts the parameter value from a URI by stripping the
// prefix and suffix. Returns empty string if the URI doesn't match.
// e.g., extractURIParam("signoz://alert/123/summary", "signoz://alert/", "/summary") returns "123"
func extractURIParam(uri, prefix, suffix string) string {
	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return ""
	}
	return uri[len(prefix) : len(uri)-len(suffix)]
}
