package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

// RegisterResourceTemplates registers dynamic MCP resource templates.
func (h *Handler) RegisterResourceTemplates(s *server.MCPServer) {
	h.logger.Debug("Registering resource templates")

	h.addResourceTemplate(s,
		mcp.NewResourceTemplate(
			"signoz://alert/{id}/summary",
			"Alert Definition and Recent History",
			mcp.WithTemplateDescription("Use this resource with a rule ID from signoz_list_alert_rules to read one live alert definition and up to 10 history records from the preceding six hours. Use signoz_get_alert or signoz_get_alert_history when a tool call is preferred."),
			mcp.WithTemplateMIMEType("application/json"),
		),
		h.handleAlertSummaryResource,
	)

	h.addResourceTemplate(s,
		mcp.NewResourceTemplate(
			"signoz://dashboard/{id}/summary",
			"Dashboard Definition",
			mcp.WithTemplateDescription("Use this resource with an ID from signoz_list_dashboards to read the full live dashboard definition, including widgets and variables. Use signoz_get_dashboard when a tool call is preferred."),
			mcp.WithTemplateMIMEType("application/json"),
		),
		h.handleDashboardSummaryResource,
	)
}

func (h *Handler) handleAlertSummaryResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	ruleID := extractURIParam(req.Params.URI, "signoz://alert/", "/summary")
	if ruleID == "" {
		return nil, fmt.Errorf("missing id in URI")
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

	asOf := timeutil.NowMillis()
	historyStart := asOf - int64((6*time.Hour)/time.Millisecond)
	historyReq := types.AlertHistoryRequest{
		Start: historyStart,
		End:   asOf,
		Order: "desc",
		Limit: 10,
	}
	historyData, err := client.GetAlertHistory(ctx, ruleID, historyReq)
	if err != nil {
		var statusErr *signozclient.HTTPStatusError
		if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("failed to get alert history: %w", err)
		}
		h.logger.WarnContext(ctx, "Failed to get alert history", slog.String("ruleId", ruleID), logpkg.ErrAttr(err))
	}

	summary := map[string]any{
		"alert":            alertData,
		"asOf":             asOf,
		"historyAvailable": err == nil,
		"historyWindow": map[string]int64{
			"start": historyStart,
			"end":   asOf,
		},
	}
	if err == nil {
		summary["recentHistory"] = historyData
	} else {
		summary["warnings"] = []string{"Recent alert history is unavailable; retry this resource or use signoz_get_alert_history."}
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
		return nil, fmt.Errorf("missing id in URI")
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
