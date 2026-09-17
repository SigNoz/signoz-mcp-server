package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterInfraHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering infrastructure monitoring handlers")

	listHostsTool := mcp.NewTool("signoz_list_hosts",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("List infrastructure hosts with CPU, memory, wait, and load metrics from SigNoz. Returns host names, active status, OS type, and resource utilization. Defaults to last 1 hour if no time specified."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '6h', '24h'. Defaults to last 1 hour if not provided.")),
		mcp.WithString("start", mcp.Description("Start timestamp in milliseconds (optional, defaults to 1 hour ago)")),
		mcp.WithString("end", mcp.Description("End timestamp in milliseconds (optional, defaults to now)")),
		mcp.WithString("orderBy", mcp.Description("Column to sort by: 'cpu', 'memory', 'wait', or 'load15' (default: 'cpu')")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc' (default: 'desc')")),
		mcp.WithString("limit", mcp.Description("Maximum number of hosts to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Number of hosts to skip for pagination (default: 0)")),
	)
	h.addTool(s, listHostsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_hosts")
		args := req.Params.Arguments.(map[string]any)

		startStr, endStr := timeutil.GetTimestampsWithDefaults(args, "ms")

		var start, end int64
		if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
			return validationErrorf("start", "invalid start timestamp: %s", startStr), nil
		}
		if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
			return validationErrorf("end", "invalid end timestamp: %s", endStr), nil
		}

		orderByCol := "cpu"
		if col, ok := args["orderBy"].(string); ok && col != "" {
			switch col {
			case "cpu", "memory", "wait", "load15":
				orderByCol = col
			default:
				return validationErrorf("orderBy", "invalid orderBy value: %q. Must be one of: cpu, memory, wait, load15", col), nil
			}
		}

		order := "desc"
		if o, ok := args["order"].(string); ok && o != "" {
			if o == "asc" || o == "desc" {
				order = o
			} else {
				return validationErrorf("order", "invalid order value: %q. Must be 'asc' or 'desc'", o), nil
			}
		}

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}

		offset := 0
		if offsetStr, ok := args["offset"].(string); ok && offsetStr != "" {
			if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
				offset = o
			}
		}

		hostReq := types.HostListRequest{
			Start:   start,
			End:     end,
			Filters: types.HostListFilter{Items: []any{}, Op: "AND"},
			GroupBy: []any{},
			OrderBy: &types.HostOrderBy{ColumnName: orderByCol, Order: order},
			Offset:  offset,
			Limit:   limit,
		}

		reqJSON, err := json.Marshal(hostReq)
		if err != nil {
			return InternalErrorResult("failed to marshal request: " + err.Error()), nil
		}

		client, err := h.GetClient(ctx)
		if err != nil {
			return clientError(err), nil
		}
		respJSON, err := client.ListHosts(ctx, reqJSON)
		if err != nil {
			h.logger.ErrorContext(ctx, "Failed to list hosts", slog.Any("error", err))
			return clientError(err), nil
		}

		// Parse and simplify the response for LLM consumption
		var hostResp types.HostListResponse
		if err := json.Unmarshal(respJSON, &hostResp); err != nil {
			h.logger.ErrorContext(ctx, "Failed to parse hosts response", slog.Any("error", err))
			return mcp.NewToolResultText(string(respJSON)), nil
		}

		// Build simplified output
		type hostSummary struct {
			HostName string  `json:"hostName"`
			Active   bool    `json:"active"`
			OS       string  `json:"os"`
			CPU      float64 `json:"cpu"`
			Memory   float64 `json:"memory"`
			Wait     float64 `json:"wait"`
			Load15   float64 `json:"load15"`
		}

		hosts := make([]hostSummary, 0, len(hostResp.Data.Records))
		for _, r := range hostResp.Data.Records {
			osType := r.OS
			if osType == "" {
				if v, ok := r.Meta["os.type"]; ok {
					osType = v
				}
			}
			hosts = append(hosts, hostSummary{
				HostName: r.HostName,
				Active:   r.Active,
				OS:       osType,
				CPU:      r.CPU,
				Memory:   r.Memory,
				Wait:     r.Wait,
				Load15:   r.Load15,
			})
		}

		result := map[string]any{
			"hosts":                  hosts,
			"total":                  hostResp.Data.Total,
			"sentAnyHostMetricsData": hostResp.Data.SentAnyHostMetricsData,
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			return InternalErrorResult("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})
}
