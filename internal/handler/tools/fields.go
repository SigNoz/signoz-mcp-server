package tools

import (
	"context"
	"encoding/json"
	"log/slog"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

const fieldContextParamDesc = "Restrict results to a single field context (optional). Valid values: " +
	"'resource' (resource attributes, e.g. service.name, k8s.namespace.name), " +
	"'attribute' (user-ingested span/log attributes), " +
	"'scope' (instrumentation scope), " +
	"'log' / 'span' / 'metric' (intrinsic columns of the logs/traces/metrics signal), " +
	"'body' (fields inside a JSON log body). " +
	"For span/log attributes use 'attribute', not the legacy Query Builder context 'tag': SigNoz ignores 'tag' when listing keys and returns no values for it."

const fieldDataTypeParamDesc = "Restrict results to a single field data type (optional). " +
	"Valid values: 'string', 'bool', 'int64', 'float64', 'number'. " +
	"SigNoz treats an array form like '[]string' as its element type."

const (
	queryTypeBuilder   = "builder_query"
	queryTypeBuilderAI = "builder_ai_query"
)

func (h *Handler) RegisterFieldsHandlers(s *mcp.Server) {
	h.logger.Debug("Registering fields handlers")

	getFieldKeysTool := mcp.NewTool("signoz_get_field_keys",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user needs to discover field names available for filtering or grouping metrics, traces, or logs. It returns keys, not their observed values, scoped by signal and optional metric, context, or data type. SigNoz sets data.complete=false when it truncated the key list; narrow with searchText or fieldContext to see more. For AI trace fields (per-trace cost, tokens, call counts, gen_ai.*), set queryType=builder_ai_query. After choosing a key, use signoz_get_field_values to discover valid values."),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("metrics", "traces", "logs"), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("searchText", mcp.Description("Filter field names by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field keys (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Enum("resource", "attribute", "scope", "log", "span", "metric", "trace", "body"), mcp.Description(fieldContextParamDesc+" 'trace' (fields of a whole trace rather than one span, such as values computed over all of its spans; write them with the trace. prefix in queries).")),
		mcp.WithString("fieldDataType", mcp.Description(fieldDataTypeParamDesc)),
		mcp.WithString("source", mcp.Description("For signal=metrics, set \"meter\" to discover Cost Meter fields; omit for the default metrics store. Omit for logs and traces.")),
		mcp.WithString("queryType", mcp.DefaultString(queryTypeBuilder), mcp.Enum(queryTypeBuilder, queryTypeBuilderAI), mcp.Description("'builder_query' (default) lists the fields of signal. Set 'builder_ai_query' for AI observability queries; it also lists the trace-level fields, such as trace.estimated_total_cost, and gen_ai.* attributes.")),
	)

	h.addTool(s, getFieldKeysTool, h.handleGetFieldKeys)

	getFieldValuesTool := mcp.NewTool("signoz_get_field_values",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user knows a field key and needs its observed values for a metrics, traces, or logs filter. It returns values, not field names; use signoz_get_field_keys when the key is unknown. Match signal and fieldContext to the query that will use the value. SigNoz sets data.complete=false when it truncated the value list; narrow with searchText to see more."),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("metrics", "traces", "logs"), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name', 'http.status_code').")),
		mcp.WithString("searchText", mcp.Description("Filter the returned values by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field values (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Enum("resource", "attribute", "scope", "log", "span", "metric", "body"), mcp.Description(fieldContextParamDesc+" Set this when the same key name exists in more than one context to disambiguate which one to fetch values for.")),
		mcp.WithString("source", mcp.Description("For signal=metrics, set \"meter\" to fetch Cost Meter field values; omit for the default metrics store. Omit for logs and traces.")),
	)

	h.addTool(s, getFieldValuesTool, h.handleGetFieldValues)
}

func (h *Handler) handleGetFieldKeys(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	signal, _ := args["signal"].(string)
	searchText, _ := args["searchText"].(string)
	metricName, _ := args["metricName"].(string)
	fieldContext, _ := args["fieldContext"].(string)
	fieldDataType, _ := args["fieldDataType"].(string)
	source, _ := args["source"].(string)
	queryType, _ := args["queryType"].(string)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_field_keys", slog.String("signal", signal), slog.String("queryType", queryType), slog.String("searchText", searchText))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	var result json.RawMessage
	// Like an out-of-enum signal, an out-of-enum queryType runs best-effort (here on the
	// default endpoint) and the schema validation notice flags it.
	if queryType == queryTypeBuilderAI {
		result, err = client.GetAIFieldKeys(ctx, searchText, fieldContext, fieldDataType)
	} else {
		result, err = client.GetFieldKeys(ctx, signal, metricName, searchText, fieldContext, fieldDataType, source)
	}
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get field keys", err, slog.String("signal", signal), slog.String("queryType", queryType))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}

func (h *Handler) handleGetFieldValues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return notAJSONObjectError(), nil
	}

	signal, _ := args["signal"].(string)
	name, _ := args["name"].(string)
	searchText, _ := args["searchText"].(string)
	metricName, _ := args["metricName"].(string)
	fieldContext, _ := args["fieldContext"].(string)
	source, _ := args["source"].(string)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_field_values", slog.String("signal", signal), slog.String("name", name))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.GetFieldValues(ctx, signal, name, metricName, searchText, fieldContext, source)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get field values", err, slog.String("signal", signal), slog.String("name", name))
		return upstreamError(err), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
