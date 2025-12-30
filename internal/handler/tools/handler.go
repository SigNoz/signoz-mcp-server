package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const (
	// Timestamp parameter descriptions
	startTimestampDesc   = "Start time (optional, defaults to 6 hours ago). Supports: numeric timestamps (milliseconds since epoch), ISO 8601/RFC3339 (e.g., '2006-01-02T15:04:05Z'), common formats (e.g., '2006-01-02 15:04:05'), relative times ('now', 'today', 'yesterday'), or natural language (e.g., 'Dec 3rd 5 PM', '5 PM')."
	endTimestampDesc     = "End time (optional, defaults to now). Supports: numeric timestamps (milliseconds since epoch), ISO 8601/RFC3339 (e.g., '2006-01-02T15:04:05Z'), common formats (e.g., '2006-01-02 15:04:05'), relative times ('now', 'today', 'yesterday'), or natural language (e.g., 'Dec 3rd 5 PM', '5 PM')."
	startTimestampNsDesc = "Start time in nanoseconds (optional, defaults to 6 hours ago). Supports: numeric timestamps (nanoseconds since epoch), ISO 8601/RFC3339 (e.g., '2006-01-02T15:04:05Z'), common formats (e.g., '2006-01-02 15:04:05'), relative times ('now', 'today', 'yesterday'), or natural language (e.g., 'Dec 3rd 5 PM', '5 PM')."
	endTimestampNsDesc   = "End time in nanoseconds (optional, defaults to now). Supports: numeric timestamps (nanoseconds since epoch), ISO 8601/RFC3339 (e.g., '2006-01-02T15:04:05Z'), common formats (e.g., '2006-01-02 15:04:05'), relative times ('now', 'today', 'yesterday'), or natural language (e.g., 'Dec 3rd 5 PM', '5 PM')."
)

type Handler struct {
	client      *signozclient.SigNoz
	logger      *zap.Logger
	signozURL   string
	clientCache map[string]*signozclient.SigNoz
	cacheMutex  sync.RWMutex
}

func NewHandler(log *zap.Logger, client *signozclient.SigNoz, signozURL string) *Handler {
	return &Handler{
		client:      client,
		logger:      log,
		signozURL:   signozURL,
		clientCache: make(map[string]*signozclient.SigNoz),
	}
}

// getClient returns the appropriate client based on the context
// If an API key is found in the context, it returns a cached client with that key
// Otherwise, it returns the default client
func (h *Handler) GetClient(ctx context.Context) *signozclient.SigNoz {
	if apiKey, ok := util.GetAPIKey(ctx); ok && apiKey != "" && h.signozURL != "" {
		// Check cache first
		h.cacheMutex.RLock()
		if cachedClient, exists := h.clientCache[apiKey]; exists {
			h.cacheMutex.RUnlock()
			return cachedClient
		}
		h.cacheMutex.RUnlock()

		h.cacheMutex.Lock()
		defer h.cacheMutex.Unlock()

		// just to check if other goroutine created client
		if cachedClient, exists := h.clientCache[apiKey]; exists {
			return cachedClient
		}

		h.logger.Debug("Creating client with API key from context")
		newClient := signozclient.NewClient(h.logger, h.signozURL, apiKey)
		h.clientCache[apiKey] = newClient
		return newClient
	}
	return h.client
}

func (h *Handler) RegisterMetricsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering metrics handlers")

	listKeysTool := mcp.NewTool("signoz_list_metric_keys",
		mcp.WithDescription("List available metric keys from SigNoz. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. Use limit to control the number of results returned (default: 50). Use offset to skip results for pagination (default: 0). For large result sets, paginate by incrementing offset: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc."),
		mcp.WithString("limit", mcp.Description("Maximum number of keys to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_metric_keys")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		client := h.GetClient(ctx)
		resp, err := client.ListMetricKeys(ctx)
		if err != nil {
			h.logger.Error("Failed to list metric keys", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		// received api data - {"data": {"attributeKeys": [...]}}
		var response map[string]any
		if err := json.Unmarshal(resp, &response); err != nil {
			h.logger.Error("Failed to parse metric keys response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		dataObj, ok := response["data"].(map[string]any)
		if !ok {
			h.logger.Error("Invalid metric keys response format", zap.Any("data", response["data"]))
			return mcp.NewToolResultError("invalid response format: expected data object"), nil
		}

		attributeKeys, ok := dataObj["attributeKeys"].([]any)
		if !ok {
			h.logger.Error("Invalid attributeKeys format", zap.Any("attributeKeys", dataObj["attributeKeys"]))
			return mcp.NewToolResultError("invalid response format: expected attributeKeys array"), nil
		}

		total := len(attributeKeys)
		pagedKeys := paginate.Array(attributeKeys, offset, limit)

		// response wrapped in paged structured format
		resultJSON, err := paginate.Wrap(pagedKeys, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap metric keys with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	searchKeysTool := mcp.NewTool("signoz_search_metric_by_text",
		mcp.WithDescription("Search metrics by text (substring autocomplete)"),
		mcp.WithString("searchText", mcp.Required(), mcp.Description("Search text for metric keys")),
	)

	s.AddTool(searchKeysTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		searchText, ok := req.Params.Arguments.(map[string]any)["searchText"].(string)
		if !ok {
			h.logger.Warn("Invalid searchText parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "searchText" must be a string. Example: {"searchText": "cpu_usage"}`), nil
		}
		if searchText == "" {
			h.logger.Warn("Empty searchText parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "searchText" cannot be empty. Provide a search term like "cpu", "memory", or "request"`), nil
		}

		h.logger.Debug("Tool called: signoz_search_metric_by_text", zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		resp, err := client.SearchMetricByText(ctx, searchText)
		if err != nil {
			h.logger.Error("Failed to search metric by text", zap.String("searchText", searchText), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(resp)), nil
	})

	getMetricsAvailableFieldsTool := mcp.NewTool("signoz_get_metrics_available_fields",
		mcp.WithDescription("Get available field names for metric queries"),
		mcp.WithString("searchText", mcp.Description("Search text to filter available fields (optional)")),
	)

	s.AddTool(getMetricsAvailableFieldsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Debug("Tool called: signoz_get_metrics_available_fields", zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		result, err := client.GetMetricsAvailableFields(ctx, searchText)
		if err != nil {
			h.logger.Error("Failed to get metrics available fields", zap.String("searchText", searchText), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getMetricsFieldValuesTool := mcp.NewTool("signoz_get_metrics_field_values",
		mcp.WithDescription("Get available field values for metric queries"),
		mcp.WithString("fieldName", mcp.Required(), mcp.Description("Field name to get values for (e.g., metric name)")),
		mcp.WithString("searchText", mcp.Description("Search text to filter values (optional)")),
	)

	s.AddTool(getMetricsFieldValuesTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			h.logger.Error("Invalid arguments type", zap.Any("arguments", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: invalid arguments format. Expected object with "fieldName" string.`), nil
		}

		fieldName, ok := args["fieldName"].(string)
		if !ok || fieldName == "" {
			h.logger.Warn("Missing or invalid fieldName", zap.Any("args", args), zap.Any("fieldName", args["fieldName"]))
			return mcp.NewToolResultError(`Parameter validation failed: "fieldName" must be a non-empty string. Examples: {"fieldName": "aws_ApplicationELB_ConsumedLCUs_max"}, {"fieldName": "cpu_usage"}`), nil
		}

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Debug("Tool called: signoz_get_metrics_field_values", zap.String("fieldName", fieldName), zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		result, err := client.GetMetricsFieldValues(ctx, fieldName, searchText)
		if err != nil {
			h.logger.Error("Failed to get metrics field values", zap.String("fieldName", fieldName), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})
}

func (h *Handler) RegisterAlertsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering alerts handlers")

	alertsTool := mcp.NewTool("signoz_list_alerts",
		mcp.WithDescription("List alerts from SigNoz. Returns list of alerts with: alert name, rule ID, severity, start time, end time, and state. Use 'activeOnly=true' (default) to get only currently firing alerts, or 'activeOnly=false' to get resolved alerts. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific alert, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0, activeOnly=true."),
		mcp.WithString("limit", mcp.Description("Maximum number of alerts to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
		mcp.WithString("activeOnly", mcp.Description("If 'true' (default), returns only active/firing alerts. If 'false', returns resolved alerts. Use 'false' to investigate alerts that have already resolved.")),
	)
	s.AddTool(alertsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Info("Tool called: signoz_list_alerts")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		// Extract activeOnly parameter, default to true
		activeOnly := true
		if args, ok := req.Params.Arguments.(map[string]any); ok {
			if ao, ok := args["activeOnly"].(bool); ok {
				activeOnly = ao
			} else if aoStr, ok := args["activeOnly"].(string); ok && aoStr != "" {
				// Handle string "true"/"false" for compatibility
				activeOnly = aoStr == "true"
			}
		}

		client := h.GetClient(ctx)
		alerts, err := client.ListAlerts(ctx, activeOnly)
		if err != nil {
			h.logger.Error("Failed to list alerts", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Debug: Log raw response preview
		responsePreview := string(alerts)
		if len(responsePreview) > 1000 {
			responsePreview = responsePreview[:1000] + "..."
		}
		h.logger.Info("Raw alerts response", zap.String("response_preview", responsePreview), zap.Int("response_length", len(alerts)))

		// Debug: Parse as generic map to inspect structure
		var rawResponse map[string]interface{}
		if err := json.Unmarshal(alerts, &rawResponse); err == nil {
			// Extract top-level keys
			topLevelKeys := make([]string, 0, len(rawResponse))
			for k := range rawResponse {
				topLevelKeys = append(topLevelKeys, k)
			}

			// Determine data type
			dataType := "nil"
			if rawResponse["data"] != nil {
				dataType = fmt.Sprintf("%T", rawResponse["data"])
			}

			h.logger.Info("Response structure inspection",
				zap.Strings("top_level_keys", topLevelKeys),
				zap.Bool("has_data", rawResponse["data"] != nil),
				zap.Bool("has_status", rawResponse["status"] != nil),
				zap.String("data_type", dataType))

			// Log first item structure if data is an array
			if dataArray, ok := rawResponse["data"].([]interface{}); ok && len(dataArray) > 0 {
				if firstItem, ok := dataArray[0].(map[string]interface{}); ok {
					firstItemKeys := make([]string, 0, len(firstItem))
					for k := range firstItem {
						firstItemKeys = append(firstItemKeys, k)
					}
					h.logger.Info("First item structure",
						zap.Strings("first_item_keys", firstItemKeys),
						zap.Any("first_item_id", firstItem["id"]),
						zap.Any("first_item_ruleId", firstItem["ruleId"]),
						zap.Any("first_item_labels", firstItem["labels"]))
				}
			}
		}

		// Parse response - handle multiple possible formats
		var alertsList []types.Alert

		// Try to parse as the expected APIAlertsResponse format first
		var apiResponse types.APIAlertsResponse
		if err := json.Unmarshal(alerts, &apiResponse); err == nil && len(apiResponse.Data) > 0 {
			// Standard format: {status: "success", data: [{labels: {...}, status: {...}, ...}]}
			h.logger.Info("Parsed as APIAlertsResponse format",
				zap.String("status", apiResponse.Status),
				zap.Int("data_count", len(apiResponse.Data)))

			alertsList = make([]types.Alert, 0, len(apiResponse.Data))
			for _, apiAlert := range apiResponse.Data {
				alertsList = append(alertsList, types.Alert{
					Alertname: apiAlert.Labels.Alertname,
					RuleID:    apiAlert.Labels.RuleID,
					Severity:  apiAlert.Labels.Severity,
					StartsAt:  apiAlert.StartsAt,
					EndsAt:    apiAlert.EndsAt,
					State:     apiAlert.Status.State,
				})
			}
		} else {
			// Try alternative formats
			var rawResponse map[string]interface{}
			if err := json.Unmarshal(alerts, &rawResponse); err != nil {
				h.logger.Error("Failed to parse alerts response", zap.Error(err), zap.String("response", string(alerts)))
				return mcp.NewToolResultError("failed to parse alerts response: " + err.Error()), nil
			}

			// Extract data array from various possible locations
			var dataArray []interface{}
			if data, ok := rawResponse["data"].([]interface{}); ok {
				// Direct array: {data: [...]}
				dataArray = data
			} else if dataMap, ok := rawResponse["data"].(map[string]interface{}); ok {
				// Nested object with rules array: {data: {rules: [...]}}
				if rules, ok := dataMap["rules"].([]interface{}); ok {
					dataArray = rules
				} else if items, ok := dataMap["items"].([]interface{}); ok {
					dataArray = items
				} else if alerts, ok := dataMap["alerts"].([]interface{}); ok {
					dataArray = alerts
				}
			} else if rawResponse["data"] == nil {
				// Try direct array format
				var directArray []interface{}
				if err := json.Unmarshal(alerts, &directArray); err == nil {
					dataArray = directArray
				}
			}

			if len(dataArray) == 0 {
				statusStr := "unknown"
				if status, ok := rawResponse["status"].(string); ok {
					statusStr = status
				}
				h.logger.Info("Parsed alerts structure - empty data",
					zap.String("status", statusStr),
					zap.Int("data_count", 0))
			} else {
				h.logger.Info("Parsed as alternative format",
					zap.Int("data_count", len(dataArray)))

				alertsList = make([]types.Alert, 0, len(dataArray))
				for _, item := range dataArray {
					itemMap, ok := item.(map[string]interface{})
					if !ok {
						continue
					}

					// Extract ruleId from various possible locations
					var ruleID string
					if id, ok := itemMap["id"].(string); ok {
						ruleID = id
					} else if rid, ok := itemMap["ruleId"].(string); ok {
						ruleID = rid
					} else if labels, ok := itemMap["labels"].(map[string]interface{}); ok {
						if rid, ok := labels["ruleId"].(string); ok {
							ruleID = rid
						} else if id, ok := labels["id"].(string); ok {
							ruleID = id
						}
					}

					// Extract alertname
					var alertname string
					if name, ok := itemMap["alert"].(string); ok {
						// /api/v1/rules uses "alert" field
						alertname = name
					} else if name, ok := itemMap["alertname"].(string); ok {
						alertname = name
					} else if labels, ok := itemMap["labels"].(map[string]interface{}); ok {
						if name, ok := labels["alertname"].(string); ok {
							alertname = name
						}
					} else if name, ok := itemMap["name"].(string); ok {
						alertname = name
					}

					// Extract severity
					var severity string
					if sev, ok := itemMap["severity"].(string); ok {
						severity = sev
					} else if labels, ok := itemMap["labels"].(map[string]interface{}); ok {
						if sev, ok := labels["severity"].(string); ok {
							severity = sev
						}
					}

					// Extract state
					var state string
					if st, ok := itemMap["state"].(string); ok {
						state = st
					} else if status, ok := itemMap["status"].(map[string]interface{}); ok {
						if st, ok := status["state"].(string); ok {
							state = st
						}
					}

					// Extract timestamps
					var startsAt, endsAt string
					if st, ok := itemMap["startsAt"].(string); ok {
						startsAt = st
					}
					if et, ok := itemMap["endsAt"].(string); ok {
						endsAt = et
					}

					// Only add if we have at least a ruleID
					if ruleID != "" {
						alertsList = append(alertsList, types.Alert{
							Alertname: alertname,
							RuleID:    ruleID,
							Severity:  severity,
							StartsAt:  startsAt,
							EndsAt:    endsAt,
							State:     state,
						})
					}
				}
			}
		}

		// Filter alerts based on activeOnly parameter
		// When activeOnly=false (using /api/v1/rules), filter to only show inactive/resolved alerts
		filteredAlerts := alertsList
		if !activeOnly {
			// Filter for resolved/inactive alerts from /api/v1/rules
			// Rules have state: "inactive" for resolved alerts, "active" or "firing" for active ones
			filteredAlerts = make([]types.Alert, 0, len(alertsList))
			for _, alert := range alertsList {
				// Include only inactive alerts (from /api/v1/rules endpoint)
				state := alert.State
				if state == "inactive" {
					filteredAlerts = append(filteredAlerts, alert)
				}
			}
			h.logger.Info("Filtered alerts for resolved/inactive only",
				zap.Int("total_rules", len(alertsList)),
				zap.Int("inactive_rules", len(filteredAlerts)))
		}

		total := len(filteredAlerts)
		alertsArray := make([]any, len(filteredAlerts))
		for i, v := range filteredAlerts {
			alertsArray[i] = v
		}
		pagedAlerts := paginate.Array(alertsArray, offset, limit)

		resultJSON, err := paginate.Wrap(pagedAlerts, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap alerts with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getAlertTool := mcp.NewTool("signoz_get_alert",
		mcp.WithDescription("Get details of a specific alert rule by ruleId"),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert ruleId")),
	)
	s.AddTool(getAlertTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ruleID, ok := req.Params.Arguments.(map[string]any)["ruleId"].(string)
		if !ok {
			h.logger.Warn("Invalid ruleId parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a"}`), nil
		}
		if ruleID == "" {
			h.logger.Warn("Empty ruleId parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "ruleId" cannot be empty. Provide a valid alert rule ID (UUID format)`), nil
		}

		h.logger.Info("Tool called: signoz_get_alert", zap.String("ruleId", ruleID))
		client := h.GetClient(ctx)
		respJSON, err := client.GetAlertByRuleID(ctx, ruleID)
		if err != nil {
			h.logger.Error("Failed to get alert", zap.String("ruleId", ruleID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(respJSON)), nil
	})

	alertHistoryTool := mcp.NewTool("signoz_get_alert_history",
		mcp.WithDescription("Get alert history timeline for a specific rule. Defaults to last 6 hours if no time specified."),
		mcp.WithString("ruleId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description(startTimestampDesc)),
		mcp.WithString("end", mcp.Description(endTimestampDesc)),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
		mcp.WithString("limit", mcp.Description("Limit number of results (default: 20)")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc' (default: 'asc')")),
	)
	s.AddTool(alertHistoryTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		ruleID, ok := args["ruleId"].(string)
		if !ok || ruleID == "" {
			h.logger.Warn("Invalid or empty ruleId parameter", zap.Any("ruleId", args["ruleId"]))
			return mcp.NewToolResultError(`Parameter validation failed: "ruleId" must be a non-empty string. Example: {"ruleId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "24h"}`), nil
		}

		startStr, endStr := timeutil.GetTimestampsWithDefaults(args, "ms")

		var start, end int64
		if _, err := fmt.Sscanf(startStr, "%d", &start); err != nil {
			h.logger.Warn("Invalid start timestamp format", zap.String("start", startStr), zap.Error(err))
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "start" timestamp: "%s". Expected numeric timestamp (milliseconds since epoch), ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "24h")`, startStr)), nil
		}
		if _, err := fmt.Sscanf(endStr, "%d", &end); err != nil {
			h.logger.Warn("Invalid end timestamp format", zap.String("end", endStr), zap.Error(err))
			return mcp.NewToolResultError(fmt.Sprintf(`Invalid "end" timestamp: "%s". Expected numeric timestamp (milliseconds since epoch), ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "24h")`, endStr)), nil
		}

		_, offset := paginate.ParseParams(args)

		limit := 20
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err != nil {
				h.logger.Warn("Invalid limit format", zap.String("limit", limitStr), zap.Error(err))
				return mcp.NewToolResultError(fmt.Sprintf(`Invalid "limit" value: "%s". Expected integer between 1-1000 (e.g., "20", "50", "100")`, limitStr)), nil
			} else if limitInt > 0 {
				limit = limitInt
			}
		}

		order := "asc"
		if orderStr, ok := args["order"].(string); ok && orderStr != "" {
			if orderStr == "asc" || orderStr == "desc" {
				order = orderStr
			} else {
				h.logger.Warn("Invalid order value", zap.String("order", orderStr))
				return mcp.NewToolResultError(fmt.Sprintf(`Invalid "order" value: "%s". Must be either "asc" or "desc"`, orderStr)), nil
			}
		}

		historyReq := types.AlertHistoryRequest{
			Start:  start,
			End:    end,
			Offset: offset,
			Limit:  limit,
			Order:  order,
			Filters: types.AlertHistoryFilters{
				Items: []interface{}{},
				Op:    "AND",
			},
		}

		h.logger.Info("Tool called: signoz_get_alert_history",
			zap.String("ruleId", ruleID),
			zap.Int64("start", start),
			zap.Int64("end", end),
			zap.Int("offset", offset),
			zap.Int("limit", limit),
			zap.String("order", order))

		client := h.GetClient(ctx)
		respJSON, err := client.GetAlertHistory(ctx, ruleID, historyReq)
		if err != nil {
			h.logger.Error("Failed to get alert history",
				zap.String("ruleId", ruleID),
				zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(respJSON)), nil
	})
}

func (h *Handler) RegisterDashboardHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering dashboard handlers")

	tool := mcp.NewTool("signoz_list_dashboards",
		mcp.WithDescription("List all dashboards from SigNoz (returns summary with name, UUID, description, tags, and timestamps). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific dashboard, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0. Supports optional regex filtering via 'namePattern' parameter to filter by name or description."),
		mcp.WithString("namePattern", mcp.Description("Optional regex pattern to filter dashboards by name or description. Matches if pattern is found in either field. Example: 'production.*api.*server' to find production API server dashboards. Case-sensitive.")),
		mcp.WithString("limit", mcp.Description("Maximum number of dashboards to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_list_dashboards")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		// Extract namePattern if provided
		namePattern := ""
		if args, ok := req.Params.Arguments.(map[string]any); ok {
			if pattern, ok := args["namePattern"].(string); ok && pattern != "" {
				namePattern = pattern
			}
		}

		client := h.GetClient(ctx)
		result, err := client.ListDashboards(ctx)
		if err != nil {
			h.logger.Error("Failed to list dashboards", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		var dashboards map[string]any
		if err := json.Unmarshal(result, &dashboards); err != nil {
			h.logger.Error("Failed to parse dashboards response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		data, ok := dashboards["data"].([]any)
		if !ok {
			h.logger.Error("Invalid dashboards response format", zap.Any("data", dashboards["data"]))
			return mcp.NewToolResultError("invalid response format: expected data array"), nil
		}

		// Apply regex filtering if namePattern is provided
		if namePattern != "" {
			re, err := regexp.Compile(namePattern)
			if err != nil {
				h.logger.Warn("Invalid regex pattern", zap.String("pattern", namePattern), zap.Error(err))
				return mcp.NewToolResultError(fmt.Sprintf("Invalid regex pattern: %s", err.Error())), nil
			}

			filteredData := make([]any, 0)
			for _, dashboard := range data {
				if dash, ok := dashboard.(map[string]any); ok {
					name := ""
					desc := ""
					// The client already simplifies the data, so name and description are at top level
					if n, ok := dash["name"].(string); ok {
						name = n
					} else if n, ok := dash["name"].(interface{}); ok && n != nil {
						name = fmt.Sprintf("%v", n)
					}
					if d, ok := dash["description"].(string); ok {
						desc = d
					} else if d, ok := dash["description"].(interface{}); ok && d != nil {
						desc = fmt.Sprintf("%v", d)
					}

					// Match if regex matches name OR description
					if re.MatchString(name) || re.MatchString(desc) {
						filteredData = append(filteredData, dashboard)
					}
				}
			}
			data = filteredData
		}

		total := len(data)
		pagedData := paginate.Array(data, offset, limit)

		resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap dashboards with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getDashboardTool := mcp.NewTool("signoz_get_dashboard",
		mcp.WithDescription("Get full details of a specific dashboard by UUID (returns complete dashboard configuration with all panels and queries)"),
		mcp.WithString("uuid", mcp.Required(), mcp.Description("Dashboard UUID")),
	)

	s.AddTool(getDashboardTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		uuid, ok := req.Params.Arguments.(map[string]any)["uuid"].(string)
		if !ok {
			h.logger.Warn("Invalid uuid parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "uuid" must be a string. Example: {"uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`), nil
		}
		if uuid == "" {
			h.logger.Warn("Empty uuid parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "uuid" cannot be empty. Provide a valid dashboard UUID. Use signoz_list_dashboards tool to see available dashboards.`), nil
		}

		h.logger.Debug("Tool called: signoz_get_dashboard", zap.String("uuid", uuid))
		client := h.GetClient(ctx)
		data, err := client.GetDashboard(ctx, uuid)
		if err != nil {
			h.logger.Error("Failed to get dashboard", zap.String("uuid", uuid), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("signoz_list_services",
		mcp.WithDescription("List all services in SigNoz. Defaults to last 6 hours if no time specified. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific service, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description(startTimestampNsDesc)),
		mcp.WithString("end", mcp.Description(endTimestampNsDesc)),
		mcp.WithString("limit", mcp.Description("Maximum number of services to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, end := timeutil.GetTimestampsWithDefaults(args, "ns")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		h.logger.Debug("Tool called: signoz_list_services", zap.String("start", start), zap.String("end", end), zap.Int("limit", limit), zap.Int("offset", offset))
		client := h.GetClient(ctx)
		result, err := client.ListServices(ctx, start, end)
		if err != nil {
			h.logger.Error("Failed to list services", zap.String("start", start), zap.String("end", end), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		var services []any
		if err := json.Unmarshal(result, &services); err != nil {
			h.logger.Error("Failed to parse services response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		total := len(services)
		pagedServices := paginate.Array(services, offset, limit)

		resultJSON, err := paginate.Wrap(pagedServices, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap services with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		mcp.WithDescription("Get top operations for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description(startTimestampNsDesc)),
		mcp.WithString("end", mcp.Description(endTimestampNsDesc)),
		mcp.WithString("tags", mcp.Description("Optional tags JSON array")),
	)

	s.AddTool(getOpsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok {
			h.logger.Warn("Invalid service parameter type", zap.Any("type", args["service"]))
			return mcp.NewToolResultError(`Parameter validation failed: "service" must be a string. Example: {"service": "frontend-api", "timeRange": "1h"}`), nil
		}
		if service == "" {
			h.logger.Warn("Empty service parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "service" cannot be empty. Provide a valid service name. Use signoz_list_services tool to see available services.`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ns")

		var tags json.RawMessage
		if t, ok := args["tags"].(string); ok && t != "" {
			tags = json.RawMessage(t)
		} else {
			tags = json.RawMessage("[]")
		}

		h.logger.Debug("Tool called: signoz_get_service_top_operations",
			zap.String("start", start),
			zap.String("end", end),
			zap.String("service", service))

		client := h.GetClient(ctx)
		result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
		if err != nil {
			h.logger.Error("Failed to get service top operations",
				zap.String("start", start),
				zap.String("end", end),
				zap.String("service", service),
				zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})
}

func (h *Handler) RegisterQueryBuilderV5Handlers(s *server.MCPServer) {
	h.logger.Debug("Registering query builder v5 handlers")

	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		mcp.WithDescription("Execute a SigNoz Query Builder v5 query. The LLM should build the complete structured query JSON matching SigNoz's Query Builder v5 format. Example structure: {\"schemaVersion\":\"v1\",\"start\":1756386047000,\"end\":1756387847000,\"requestType\":\"raw\",\"compositeQuery\":{\"queries\":[{\"type\":\"builder_query\",\"spec\":{\"name\":\"A\",\"signal\":\"traces\",\"disabled\":false,\"limit\":10,\"offset\":0,\"order\":[{\"key\":{\"name\":\"timestamp\"},\"direction\":\"desc\"}],\"having\":{\"expression\":\"\"},\"selectFields\":[{\"name\":\"service.name\",\"fieldDataType\":\"string\",\"signal\":\"traces\",\"fieldContext\":\"resource\"},{\"name\":\"duration_nano\",\"fieldDataType\":\"\",\"signal\":\"traces\",\"fieldContext\":\"span\"}]}}]},\"formatOptions\":{\"formatTableResultForUI\":false,\"fillGaps\":false},\"variables\":{}}. See docs: https://signoz.io/docs/userguide/query-builder-v5/"),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables")),
	)

	s.AddTool(executeQuery, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Debug("Tool called: signoz_execute_builder_query")

		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			h.logger.Warn("Invalid arguments payload type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError("invalid arguments payload"), nil
		}

		queryObj, ok := args["query"].(map[string]any)
		if !ok {
			h.logger.Warn("Invalid query parameter type", zap.Any("type", args["query"]))
			return mcp.NewToolResultError("query parameter must be a JSON object"), nil
		}

		// Preprocess start/end timestamps if they're strings (human-readable formats)
		now := time.Now()
		if startVal, ok := queryObj["start"]; ok {
			if startStr, ok := startVal.(string); ok {
				// Try parsing as human-readable date/time string
				if parsedTime, err := timeutil.ParseDateTimeString(startStr, now); err == nil {
					parsedMs := parsedTime.UnixMilli()
					queryObj["start"] = parsedMs
					h.logger.Debug("Parsed start timestamp from string", zap.String("input", startStr), zap.Int64("parsed", parsedMs))
				} else {
					// Try parsing as numeric string
					if numVal, err := strconv.ParseInt(startStr, 10, 64); err == nil {
						queryObj["start"] = numVal
					} else {
						h.logger.Warn("Failed to parse start timestamp", zap.String("value", startStr), zap.Error(err))
						return mcp.NewToolResultError(fmt.Sprintf(`Invalid "start" timestamp: "%s". Expected numeric timestamp (milliseconds since epoch), ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), time range (e.g., "24h", "7d"), or natural language (e.g., "Dec 3rd 5 PM")`, startStr)), nil
					}
				}
			}
		}

		if endVal, ok := queryObj["end"]; ok {
			if endStr, ok := endVal.(string); ok {
				// Try parsing as human-readable date/time string
				if parsedTime, err := timeutil.ParseDateTimeString(endStr, now); err == nil {
					parsedMs := parsedTime.UnixMilli()
					queryObj["end"] = parsedMs
					h.logger.Debug("Parsed end timestamp from string", zap.String("input", endStr), zap.Int64("parsed", parsedMs))
				} else {
					// Try parsing as numeric string
					if numVal, err := strconv.ParseInt(endStr, 10, 64); err == nil {
						queryObj["end"] = numVal
					} else {
						h.logger.Warn("Failed to parse end timestamp", zap.String("value", endStr), zap.Error(err))
						return mcp.NewToolResultError(fmt.Sprintf(`Invalid "end" timestamp: "%s". Expected numeric timestamp (milliseconds since epoch), ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), time range (e.g., "24h", "7d"), or natural language (e.g., "Dec 3rd 5 PM")`, endStr)), nil
					}
				}
			}
		}

		queryJSON, err := json.Marshal(queryObj)
		if err != nil {
			h.logger.Error("Failed to marshal query object", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query object: " + err.Error()), nil
		}

		var queryPayload types.QueryPayload
		if err := json.Unmarshal(queryJSON, &queryPayload); err != nil {
			h.logger.Error("Failed to unmarshal query payload", zap.Error(err))
			return mcp.NewToolResultError("invalid query payload structure: " + err.Error()), nil
		}

		if err := queryPayload.Validate(); err != nil {
			h.logger.Error("Query validation failed", zap.Error(err))
			return mcp.NewToolResultError("query validation error: " + err.Error()), nil
		}

		finalQueryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal validated query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal validated query payload: " + err.Error()), nil
		}

		client := h.GetClient(ctx)
		data, err := client.QueryBuilderV5(ctx, finalQueryJSON)
		if err != nil {
			h.logger.Error("Failed to execute query builder v5", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		h.logger.Debug("Successfully executed query builder v5")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func (h *Handler) RegisterLogsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering logs handlers")

	listLogViewsTool := mcp.NewTool("signoz_list_log_views",
		mcp.WithDescription("List all saved log views from SigNoz (returns summary with name, ID, description, and query details). IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific log view, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("limit", mcp.Description("Maximum number of views to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	s.AddTool(listLogViewsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h.logger.Info("Tool called: signoz_list_log_views")
		limit, offset := paginate.ParseParams(req.Params.Arguments)

		client := h.GetClient(ctx)
		result, err := client.ListLogViews(ctx)
		if err != nil {
			h.logger.Error("Failed to list log views", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Debug: Log raw response preview (use Error level for visibility during debugging)
		responsePreview := string(result)
		if len(responsePreview) > 1000 {
			responsePreview = responsePreview[:1000] + "..."
		}
		h.logger.Error("Raw log views response", zap.String("response_preview", responsePreview), zap.Int("response_length", len(result)))

		var logViews map[string]any
		if err := json.Unmarshal(result, &logViews); err != nil {
			h.logger.Error("Failed to parse log views response", zap.Error(err))
			return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
		}

		// Debug: Log parsed structure and data type (use Error level for visibility during debugging)
		h.logger.Error("Parsed log views structure",
			zap.Any("logViews", logViews),
			zap.String("dataType", fmt.Sprintf("%T", logViews["data"])),
			zap.Any("dataValue", logViews["data"]))

		// Debug: Check data before type assertion
		if logViews["data"] == nil {
			h.logger.Error("logViews['data'] is nil")
		} else {
			h.logger.Error("logViews['data'] details",
				zap.Any("data", logViews["data"]),
				zap.String("type", fmt.Sprintf("%T", logViews["data"])))
		}

		// Handle different response structures
		var dataArray []any

		if data, ok := logViews["data"].([]any); ok {
			// Direct array (expected case)
			dataArray = data
		} else if dataObj, ok := logViews["data"].(map[string]any); ok {
			// Data is an object - check common nested array keys
			if items, ok := dataObj["items"].([]any); ok {
				dataArray = items
			} else if views, ok := dataObj["views"].([]any); ok {
				dataArray = views
			} else if results, ok := dataObj["results"].([]any); ok {
				dataArray = results
			} else {
				// Log all keys for debugging
				keys := make([]string, 0, len(dataObj))
				for k := range dataObj {
					keys = append(keys, k)
				}
				h.logger.Error("Could not find array in data object",
					zap.Any("dataObject", dataObj),
					zap.Strings("availableKeys", keys))
				return mcp.NewToolResultError("invalid response format: data object does not contain array"), nil
			}
		} else if logViews["data"] == nil {
			// Empty response
			dataArray = []any{}
		} else {
			// Unknown structure
			h.logger.Error("Unknown data structure",
				zap.Any("data", logViews["data"]),
				zap.String("dataType", fmt.Sprintf("%T", logViews["data"])))
			return mcp.NewToolResultError(fmt.Sprintf("invalid response format: unexpected data type %T", logViews["data"])), nil
		}

		total := len(dataArray)
		pagedData := paginate.Array(dataArray, offset, limit)

		resultJSON, err := paginate.Wrap(pagedData, total, offset, limit)
		if err != nil {
			h.logger.Error("Failed to wrap log views with pagination", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
		}

		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	getLogViewTool := mcp.NewTool("signoz_get_log_view",
		mcp.WithDescription("Get full details of a specific log view by ID (returns complete log view configuration with query structure)"),
		mcp.WithString("viewId", mcp.Required(), mcp.Description("Log view ID")),
	)

	s.AddTool(getLogViewTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		viewID, ok := req.Params.Arguments.(map[string]any)["viewId"].(string)
		if !ok {
			h.logger.Warn("Invalid viewId parameter type", zap.Any("type", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: "viewId" must be a string. Example: {"viewId": "error-logs-view-123"}`), nil
		}
		if viewID == "" {
			h.logger.Warn("Empty viewId parameter")
			return mcp.NewToolResultError(`Parameter validation failed: "viewId" cannot be empty. Provide a valid log view ID. Use signoz_list_log_views tool to see available log views.`), nil
		}

		h.logger.Info("Tool called: signoz_get_log_view", zap.String("viewId", viewID))
		client := h.GetClient(ctx)
		data, err := client.GetLogView(ctx, viewID)
		if err != nil {
			h.logger.Error("Failed to get log view", zap.String("viewId", viewID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})

	getLogsForAlertTool := mcp.NewTool("signoz_get_logs_for_alert",
		mcp.WithDescription("Get logs related to a specific alert (automatically determines time range and service from alert details)"),
		mcp.WithString("alertId", mcp.Required(), mcp.Description("Alert rule ID")),
		mcp.WithString("timeRange", mcp.Description("Time range around alert (optional). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '15m', '30m', '1h', '2h', '6h'. Defaults to '1h' if not provided.")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(getLogsForAlertTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		alertID, ok := args["alertId"].(string)
		if !ok || alertID == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "alertId" must be a non-empty string. Example: {"alertId": "0196634d-5d66-75c4-b778-e317f49dab7a", "timeRange": "1h", "limit": "50"}`), nil
		}

		timeRange := "1h"
		if tr, ok := args["timeRange"].(string); ok && tr != "" {
			timeRange = tr
		}

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				limit = limitInt
			}
		}

		_, offset := paginate.ParseParams(req.Params.Arguments)

		h.logger.Debug("Tool called: signoz_get_logs_for_alert", zap.String("alertId", alertID))
		client := h.GetClient(ctx)
		alertData, err := client.GetAlertByRuleID(ctx, alertID)
		if err != nil {
			h.logger.Error("Failed to get alert details", zap.String("alertId", alertID), zap.Error(err))
			return mcp.NewToolResultError("failed to get alert details: " + err.Error()), nil
		}

		var alertResponse map[string]interface{}
		if err := json.Unmarshal(alertData, &alertResponse); err != nil {
			h.logger.Error("Failed to parse alert data", zap.Error(err))
			return mcp.NewToolResultError("failed to parse alert data: " + err.Error()), nil
		}

		serviceName := ""
		if data, ok := alertResponse["data"].(map[string]interface{}); ok {
			if labels, ok := data["labels"].(map[string]interface{}); ok {
				if service, ok := labels["service_name"].(string); ok {
					serviceName = service
				} else if service, ok := labels["service"].(string); ok {
					serviceName = service
				}
			}
		}

		now := time.Now()
		startTime := now.Add(-1 * time.Hour).UnixMilli()
		endTime := now.UnixMilli()

		if duration, err := timeutil.ParseTimeRange(timeRange); err == nil {
			startTime = now.Add(-duration).UnixMilli()
		}

		filterExpression := "severity_text IN ('ERROR', 'WARN', 'FATAL')"
		if serviceName != "" {
			filterExpression += fmt.Sprintf(" AND service.name in ['%s']", serviceName)
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to get logs for alert", zap.String("alertId", alertID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

	getErrorLogsTool := mcp.NewTool("signoz_get_error_logs",
		mcp.WithDescription("Get logs with ERROR or FATAL severity. Defaults to last 6 hours if no time specified."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("service", mcp.Description("Optional service name to filter by")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 25, max: 200)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(getErrorLogsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		limit := 25
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				if limitInt > 200 {
					limit = 200
				} else if limitInt < 1 {
					limit = 1
				} else {
					limit = limitInt
				}
			}
		}

		_, offset := paginate.ParseParams(req.Params.Arguments)

		filterExpression := "severity_text IN ('ERROR', 'FATAL')"

		if service, ok := args["service"].(string); ok && service != "" {
			filterExpression += fmt.Sprintf(" AND service.name in ['%s']", service)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, end)), nil
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Info("Tool called: signoz_get_error_logs", zap.String("start", start), zap.String("end", end))
		client := h.GetClient(ctx)
		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to get error logs", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	searchLogsByServiceTool := mcp.NewTool("signoz_search_logs_by_service",
		mcp.WithDescription("Search logs for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name to search logs for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("severity", mcp.Description("Log severity filter (DEBUG, INFO, WARN, ERROR, FATAL)")),
		mcp.WithString("searchText", mcp.Description("Text to search for in log body")),
		mcp.WithString("limit", mcp.Description("Maximum number of logs to return (default: 100)")),
		mcp.WithString("offset", mcp.Description("Offset for pagination (default: 0)")),
	)

	s.AddTool(searchLogsByServiceTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok || service == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "service" must be a non-empty string. Example: {"service": "consumer-svc-1", "searchText": "error", "timeRange": "1h", "limit": "50"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				limit = limitInt
			}
		}

		_, offset := paginate.ParseParams(req.Params.Arguments)

		filterExpression := fmt.Sprintf("service.name in ['%s']", service)

		if severity, ok := args["severity"].(string); ok && severity != "" {
			filterExpression += fmt.Sprintf(" AND severity_text = '%s'", severity)
		}

		if searchText, ok := args["searchText"].(string); ok && searchText != "" {
			filterExpression += fmt.Sprintf(" AND body CONTAINS '%s'", searchText)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, end)), nil
		}

		queryPayload := types.BuildLogsQueryPayload(startTime, endTime, filterExpression, limit, offset)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Info("Tool called: signoz_search_logs_by_service", zap.String("service", service), zap.String("start", start), zap.String("end", end))
		client := h.GetClient(ctx)
		result, err := client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to search logs by service", zap.String("service", service), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

	getLogsAvailableFieldsTool := mcp.NewTool("signoz_get_logs_available_fields",
		mcp.WithDescription("Get available field names for log queries"),
		mcp.WithString("searchText", mcp.Description("Search text to filter available fields (optional)")),
	)

	s.AddTool(getLogsAvailableFieldsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Info("Tool called: signoz_get_logs_available_fields", zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		result, err := client.GetLogsAvailableFields(ctx, searchText)
		if err != nil {
			h.logger.Error("Failed to get logs available fields", zap.String("searchText", searchText), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getLogsFieldValuesTool := mcp.NewTool("signoz_get_logs_field_values",
		mcp.WithDescription("Get available field values for log queries"),
		mcp.WithString("fieldName", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name')")),
		mcp.WithString("searchText", mcp.Description("Search text to filter values (optional)")),
	)

	s.AddTool(getLogsFieldValuesTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			h.logger.Error("Invalid arguments type", zap.Any("arguments", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: invalid arguments format. Expected object with "fieldName" string.`), nil
		}

		fieldName, ok := args["fieldName"].(string)
		if !ok || fieldName == "" {
			h.logger.Warn("Missing or invalid fieldName", zap.Any("args", args), zap.Any("fieldName", args["fieldName"]))
			return mcp.NewToolResultError(`Parameter validation failed: "fieldName" must be a non-empty string. Examples: {"fieldName": "service.name"}, {"fieldName": "severity_text"}, {"fieldName": "body"}`), nil
		}

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Info("Tool called: signoz_get_logs_field_values", zap.String("fieldName", fieldName), zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		result, err := client.GetLogsFieldValues(ctx, fieldName, searchText)
		if err != nil {
			h.logger.Error("Failed to get logs field values", zap.String("fieldName", fieldName), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

}

func (h *Handler) RegisterTracesHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering traces handlers")

	getTraceFieldValuesTool := mcp.NewTool("signoz_get_trace_field_values",
		mcp.WithDescription("Get available field values for trace queries"),
		mcp.WithString("fieldName", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name')")),
		mcp.WithString("searchText", mcp.Description("Search text to filter values (optional)")),
	)

	s.AddTool(getTraceFieldValuesTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			h.logger.Error("Invalid arguments type", zap.Any("arguments", req.Params.Arguments))
			return mcp.NewToolResultError(`Parameter validation failed: invalid arguments format. Expected object with "fieldName" string.`), nil
		}

		fieldName, ok := args["fieldName"].(string)
		if !ok || fieldName == "" {
			h.logger.Warn("Missing or invalid fieldName", zap.Any("args", args), zap.Any("fieldName", args["fieldName"]))
			return mcp.NewToolResultError(`Parameter validation failed: "fieldName" must be a non-empty string. Examples: {"fieldName": "service.name"}, {"fieldName": "http.status_code"}, {"fieldName": "operation"}`), nil
		}

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Debug("Tool called: signoz_get_trace_field_values", zap.String("fieldName", fieldName), zap.String("searchText", searchText))
		result, err := h.client.GetTraceFieldValues(ctx, fieldName, searchText)
		if err != nil {
			h.logger.Error("Failed to get trace field values", zap.String("fieldName", fieldName), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceAvailableFieldsTool := mcp.NewTool("signoz_get_trace_available_fields",
		mcp.WithDescription("Get available field names for trace queries"),
		mcp.WithString("searchText", mcp.Description("Search text to filter available fields (optional)")),
	)

	s.AddTool(getTraceAvailableFieldsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		searchText := ""
		if search, ok := args["searchText"].(string); ok && search != "" {
			searchText = search
		}

		h.logger.Debug("Tool called: signoz_get_trace_available_fields", zap.String("searchText", searchText))
		client := h.GetClient(ctx)
		result, err := client.GetTraceAvailableFields(ctx, searchText)
		if err != nil {
			h.logger.Error("Failed to get trace available fields", zap.String("searchText", searchText), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	searchTracesByServiceTool := mcp.NewTool("signoz_search_traces_by_service",
		mcp.WithDescription("Search traces for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name to search traces for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("operation", mcp.Description("Operation name to filter by")),
		mcp.WithString("error", mcp.Description("Filter by error status (true/false)")),
		mcp.WithString("minDuration", mcp.Description("Minimum duration in nanoseconds")),
		mcp.WithString("maxDuration", mcp.Description("Maximum duration in nanoseconds")),
		mcp.WithString("limit", mcp.Description("Maximum number of traces to return (default: 100)")),
	)

	s.AddTool(searchTracesByServiceTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		service, ok := args["service"].(string)
		if !ok || service == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "service" must be a non-empty string. Example: {"service": "frontend-api", "timeRange": "2h", "error": "true", "limit": "100"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		limit := 100
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			if limitInt, err := strconv.Atoi(limitStr); err == nil {
				limit = limitInt
			}
		}

		filterExpression := fmt.Sprintf("service.name in ['%s']", service)

		if operation, ok := args["operation"].(string); ok && operation != "" {
			filterExpression += fmt.Sprintf(" AND name = '%s'", operation)
		}

		if errorFilter, ok := args["error"].(string); ok && errorFilter != "" {
			switch errorFilter {
			case "true":
				filterExpression += " AND hasError = true"
			case "false":
				filterExpression += " AND hasError = false"
			}
		}

		if minDuration, ok := args["minDuration"].(string); ok && minDuration != "" {
			filterExpression += fmt.Sprintf(" AND durationNano >= %s", minDuration)
		}

		if maxDuration, ok := args["maxDuration"].(string); ok && maxDuration != "" {
			filterExpression += fmt.Sprintf(" AND durationNano <= %s", maxDuration)
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, end)), nil
		}

		queryPayload := types.BuildTracesQueryPayload(startTime, endTime, filterExpression, limit)

		queryJSON, err := json.Marshal(queryPayload)
		if err != nil {
			h.logger.Error("Failed to marshal query payload", zap.Error(err))
			return mcp.NewToolResultError("failed to marshal query payload: " + err.Error()), nil
		}

		h.logger.Debug("Tool called: signoz_search_traces_by_service", zap.String("service", service), zap.String("start", start), zap.String("end", end))
		result, err := h.client.QueryBuilderV5(ctx, queryJSON)
		if err != nil {
			h.logger.Error("Failed to search traces by service", zap.String("service", service), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceDetailsTool := mcp.NewTool("signoz_get_trace_details",
		mcp.WithDescription("Get comprehensive trace information including all spans and metadata. Defaults to last 6 hours if no time specified. Set includeLogs=true to also retrieve associated logs for this trace in the same call."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description("Trace ID to get details for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("includeSpans", mcp.Description("Include detailed span information (true/false, default: true)")),
		mcp.WithString("includeLogs", mcp.Description("Include associated logs for this trace (true/false, default: false). When true, returns both trace spans and logs in a single response.")),
	)

	s.AddTool(getTraceDetailsTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		traceID, ok := args["traceId"].(string)
		if !ok || traceID == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "traceId" must be a non-empty string. Example: {"traceId": "abc123def456", "includeSpans": "true", "timeRange": "1h"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		includeSpans := true
		if includeStr, ok := args["includeSpans"].(string); ok && includeStr != "" {
			includeSpans = includeStr == "true"
		}

		includeLogs := false
		if includeStr, ok := args["includeLogs"].(string); ok && includeStr != "" {
			includeLogs = includeStr == "true"
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, end)), nil
		}

		h.logger.Debug("Tool called: signoz_get_trace_details", zap.String("traceId", traceID), zap.Bool("includeSpans", includeSpans), zap.Bool("includeLogs", includeLogs), zap.String("start", start), zap.String("end", end))
		result, err := h.client.GetTraceDetails(ctx, traceID, includeSpans, includeLogs, startTime, endTime)
		if err != nil {
			h.logger.Error("Failed to get trace details", zap.String("traceId", traceID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceErrorAnalysisTool := mcp.NewTool("signoz_get_trace_error_analysis",
		mcp.WithDescription("Analyze error patterns in traces. Defaults to last 6 hours if no time specified."),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
		mcp.WithString("service", mcp.Description("Service name to filter by (optional)")),
	)

	s.AddTool(getTraceErrorAnalysisTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		service := ""
		if s, ok := args["service"].(string); ok && s != "" {
			service = s
		}

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, end)), nil
		}

		h.logger.Debug("Tool called: signoz_get_trace_error_analysis", zap.String("start", start), zap.String("end", end), zap.String("service", service))
		result, err := h.client.GetTraceErrorAnalysis(ctx, startTime, endTime, service)
		if err != nil {
			h.logger.Error("Failed to get trace error analysis", zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

	getTraceSpanHierarchyTool := mcp.NewTool("signoz_get_trace_span_hierarchy",
		mcp.WithDescription("Get trace span relationships and hierarchy. Defaults to last 6 hours if no time specified."),
		mcp.WithString("traceId", mcp.Required(), mcp.Description("Trace ID to get span hierarchy for")),
		mcp.WithString("timeRange", mcp.Description("Time range string (optional, overrides start/end). Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). Examples: '30m', '1h', '2h', '6h', '24h', '7d'. Defaults to last 6 hours if not provided.")),
		mcp.WithString("start", mcp.Description("Start time in milliseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in milliseconds (optional, defaults to now)")),
	)

	s.AddTool(getTraceSpanHierarchyTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments.(map[string]any)

		traceID, ok := args["traceId"].(string)
		if !ok || traceID == "" {
			return mcp.NewToolResultError(`Parameter validation failed: "traceId" must be a non-empty string. Example: {"traceId": "abc123def456", "timeRange": "1h"}`), nil
		}

		start, end := timeutil.GetTimestampsWithDefaults(args, "ms")

		var startTime, endTime int64
		if err := json.Unmarshal([]byte(start), &startTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "start" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, start)), nil
		}
		if err := json.Unmarshal([]byte(end), &endTime); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(`Internal error: Invalid "end" timestamp format: "%s". Expected numeric timestamp, ISO 8601 format (e.g., "2006-01-02T15:04:05Z"), relative time ("now", "today", "yesterday"), natural language (e.g., "Dec 3rd 5 PM"), or use "timeRange" parameter (e.g., "1h", "24h")`, end)), nil
		}

		h.logger.Debug("Tool called: signoz_get_trace_span_hierarchy", zap.String("traceId", traceID), zap.String("start", start), zap.String("end", end))
		result, err := h.client.GetTraceSpanHierarchy(ctx, traceID, startTime, endTime)
		if err != nil {
			h.logger.Error("Failed to get trace span hierarchy", zap.String("traceId", traceID), zap.Error(err))
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	})

}
