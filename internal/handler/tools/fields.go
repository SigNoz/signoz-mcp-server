package tools

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const fieldContextParamDesc = "Restrict results to a single field context (optional). Valid values: " +
	"'resource' (resource attributes, e.g. service.name, k8s.namespace.name), " +
	"'attribute' (user-ingested attributes; 'tag' is accepted as an alias), " +
	"'scope' (instrumentation scope), " +
	"'log' / 'span' / 'metric' (intrinsic/built-in columns of the logs/traces/metrics signal), " +
	"'body' (fields inside a JSON log body). Use this to tell intrinsic columns apart from user attributes."

const fieldDataTypeParamDesc = "Restrict results to a single field data type (optional). " +
	"Valid values: 'string', 'bool', 'int64', 'float64', 'number', or array forms like '[]string'."

func (h *Handler) RegisterFieldsHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering fields handlers")

	getFieldKeysTool := mcp.NewTool("signoz_get_field_keys",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get available field keys for a given signal (metrics, traces, or logs). Use this to discover filterable fields before building queries."),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("metrics", "traces", "logs"), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("searchText", mcp.Description("Filter field names by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field keys (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Description(fieldContextParamDesc)),
		mcp.WithString("fieldDataType", mcp.Description(fieldDataTypeParamDesc)),
		mcp.WithString("source", mcp.Description("Source filter (optional).")),
	)

	h.addTool(s, getFieldKeysTool, h.handleGetFieldKeys)

	getFieldValuesTool := mcp.NewTool("signoz_get_field_values",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get possible values for a specific field key for a given signal (metrics, traces, or logs). Use this to discover valid filter values."),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("metrics", "traces", "logs"), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name', 'http.status_code').")),
		mcp.WithString("searchText", mcp.Description("Filter the returned values by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field values (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Description(fieldContextParamDesc+" Set this when the same key name exists in more than one context to disambiguate which one to fetch values for.")),
		mcp.WithString("source", mcp.Description("Source filter (optional).")),
	)

	h.addTool(s, getFieldValuesTool, h.handleGetFieldValues)
}

func (h *Handler) handleGetFieldKeys(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	signal, ok := args["signal"].(string)
	if !ok || signal == "" || (signal != "metrics" && signal != "traces" && signal != "logs") {
		return validationError("signal", `must be one of: "metrics", "traces", "logs"`), nil
	}

	searchText, _ := args["searchText"].(string)
	metricName, _ := args["metricName"].(string)
	fieldContext, _ := args["fieldContext"].(string)
	fieldDataType, _ := args["fieldDataType"].(string)
	source, _ := args["source"].(string)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_field_keys", slog.String("signal", signal), slog.String("searchText", searchText))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetFieldKeys(ctx, signal, metricName, searchText, fieldContext, fieldDataType, source)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get field keys", err, slog.String("signal", signal))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleGetFieldValues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	signal, ok := args["signal"].(string)
	if !ok || signal == "" || (signal != "metrics" && signal != "traces" && signal != "logs") {
		return validationError("signal", `must be one of: "metrics", "traces", "logs"`), nil
	}

	name, ok := args["name"].(string)
	if !ok || name == "" {
		return validationError("name", `must be a non-empty string. Example: "service.name", "http.status_code"`), nil
	}

	searchText, _ := args["searchText"].(string)
	metricName, _ := args["metricName"].(string)
	fieldContext, _ := args["fieldContext"].(string)
	source, _ := args["source"].(string)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_field_values", slog.String("signal", signal), slog.String("name", name))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetFieldValues(ctx, signal, name, metricName, searchText, fieldContext, source)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get field values", err, slog.String("signal", signal), slog.String("name", name))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
