package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

func (h *Handler) RegisterFieldsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering fields handlers")

	getFieldKeysTool := mcp.NewTool("signoz_get_field_keys",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription("Get available field keys for a given signal (metrics, traces, or logs). Use this to discover filterable fields before building queries."),
		mcp.WithString("signal", mcp.Required(), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("searchText", mcp.Description("Filter field names by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field keys (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Description("Field context filter (optional).")),
		mcp.WithString("fieldDataType", mcp.Description("Field data type filter (optional).")),
		mcp.WithString("source", mcp.Description("Source filter (optional).")),
	)

	s.AddTool(getFieldKeysTool, h.handleGetFieldKeys)

	getFieldValuesTool := mcp.NewTool("signoz_get_field_values",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithDescription("Get possible values for a specific field key for a given signal (metrics, traces, or logs). Use this to discover valid filter values."),
		mcp.WithString("signal", mcp.Required(), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name', 'http.status_code').")),
		mcp.WithString("searchText", mcp.Description("Filter the returned values by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field values (optional, only relevant when signal=metrics).")),
		mcp.WithString("source", mcp.Description("Source filter (optional).")),
	)

	s.AddTool(getFieldValuesTool, h.handleGetFieldValues)
}

func (h *Handler) handleGetFieldKeys(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	signal, ok := args["signal"].(string)
	if !ok || signal == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "signal" must be one of: "metrics", "traces", "logs"`), nil
	}
	if signal != "metrics" && signal != "traces" && signal != "logs" {
		return mcp.NewToolResultError(`Parameter validation failed: "signal" must be one of: "metrics", "traces", "logs"`), nil
	}

	searchText, _ := args["searchText"].(string)
	metricName, _ := args["metricName"].(string)
	fieldContext, _ := args["fieldContext"].(string)
	fieldDataType, _ := args["fieldDataType"].(string)
	source, _ := args["source"].(string)

	log.Debug("Tool called: signoz_get_field_keys", zap.String("signal", signal), zap.String("searchText", searchText))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetFieldKeys(ctx, signal, metricName, searchText, fieldContext, fieldDataType, source)
	if err != nil {
		log.Error("Failed to get field keys", zap.String("signal", signal), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleGetFieldValues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.tenantLogger(ctx)
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}

	signal, ok := args["signal"].(string)
	if !ok || signal == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "signal" must be one of: "metrics", "traces", "logs"`), nil
	}
	if signal != "metrics" && signal != "traces" && signal != "logs" {
		return mcp.NewToolResultError(`Parameter validation failed: "signal" must be one of: "metrics", "traces", "logs"`), nil
	}

	name, ok := args["name"].(string)
	if !ok || name == "" {
		return mcp.NewToolResultError(`Parameter validation failed: "name" must be a non-empty string. Example: "service.name", "http.status_code"`), nil
	}

	searchText, _ := args["searchText"].(string)
	metricName, _ := args["metricName"].(string)
	source, _ := args["source"].(string)

	log.Debug("Tool called: signoz_get_field_values", zap.String("signal", signal), zap.String("name", name))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetFieldValues(ctx, signal, name, metricName, searchText, source)
	if err != nil {
		log.Error("Failed to get field values", zap.String("signal", signal), zap.String("name", name), zap.Error(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
