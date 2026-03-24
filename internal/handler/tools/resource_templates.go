package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

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

	log := h.tenantLogger(ctx)
	client, err := h.GetClient(ctx)
	if err != nil {
		return nil, err
	}

	log.Debug("Fetching alert summary resource", zap.String("ruleId", ruleID))

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
		log.Warn("Failed to get alert history", zap.String("ruleId", ruleID), zap.Error(err))
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

	log := h.tenantLogger(ctx)
	client, err := h.GetClient(ctx)
	if err != nil {
		return nil, err
	}

	log.Debug("Fetching dashboard summary resource", zap.String("uuid", uuid))

	dashData, err := client.GetDashboard(ctx, uuid)
	if err != nil {
		return nil, fmt.Errorf("failed to get dashboard: %w", err)
	}

	// Extract just metadata + widget names to keep the resource concise
	var raw map[string]any
	if err := json.Unmarshal(dashData, &raw); err != nil {
		// If we can't parse, return the raw data
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(dashData),
			},
		}, nil
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(dashData),
		},
	}, nil
}

// extractURIParam extracts the parameter value from a URI by stripping the prefix and suffix.
// e.g., extractURIParam("signoz://alert/123/summary", "signoz://alert/", "/summary") returns "123"
func extractURIParam(uri, prefix, suffix string) string {
	s := strings.TrimPrefix(uri, prefix)
	s = strings.TrimSuffix(s, suffix)
	return s
}
