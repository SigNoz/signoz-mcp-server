package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

const fieldContextParamDesc = "Restrict results to a single field context (optional). Valid values: " +
	"'resource' (resource attributes, e.g. service.name, k8s.namespace.name), " +
	"'attribute' (user-ingested span/log attributes), " +
	"'scope' (instrumentation scope), " +
	"'log' / 'span' / 'metric' (intrinsic columns of the logs/traces/metrics signal), " +
	"'body' (fields inside a JSON log body). " +
	"For span/log attributes use 'attribute', not the legacy Query Builder context 'tag': SigNoz ignores 'tag' when listing keys and returns no values for it."

const (
	fieldKeysLimitParamDesc = "Maximum number of keys to return for each signal (optional). Omit it for the SigNoz default. " +
		"Intrinsic and materialized fields come on top of the limit, so the list can be longer. Set a limit when a few keys are enough."
	fieldValuesLimitParamDesc = "Maximum number of values to return (optional). Omit it for the SigNoz default."
)

const fieldDataTypeParamDesc = "Restrict results to a single field data type (optional). " +
	"Valid values: 'string', 'bool', 'int64', 'float64', 'number'. " +
	"SigNoz treats an array form like '[]string' as its element type."

func (h *Handler) RegisterFieldsHandlers(s *mcp.Server) {
	h.logger.Debug("Registering fields handlers")

	getFieldKeysTool := mcp.NewTool("signoz_get_field_keys",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user needs to discover field names available for filtering or grouping metrics, traces, or logs. It returns keys, not their observed values, scoped by signal and optional metric, context, or data type. Keys come from all retained metadata; this tool takes no time range. SigNoz sets data.complete=false when it cut the list; narrow with searchText or fieldContext, or raise limit. For AI trace fields (per-trace cost, tokens, call counts, gen_ai.*), set queryType=builder_ai_query. After choosing a key, use signoz_get_field_values to discover valid values."),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("metrics", "traces", "logs"), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("searchText", mcp.Description("Filter field names by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field keys (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Enum("resource", "attribute", "scope", "log", "span", "metric", "trace", "body"), mcp.Description(fieldContextParamDesc+" 'trace' (fields of a whole trace rather than one span, such as values computed over all of its spans; write them with the trace. prefix in queries).")),
		mcp.WithString("fieldDataType", mcp.Description(fieldDataTypeParamDesc)),
		mcp.WithString("source", mcp.Description("For signal=metrics, set \"meter\" to discover Cost Meter fields; omit for the default metrics store. Omit for logs and traces.")),
		mcp.WithString("limit", intOrStringType(), mcp.Description(fieldKeysLimitParamDesc)),
		mcp.WithString("queryType", mcp.DefaultString(querybuilder.QueryTypeBuilder), mcp.Enum(querybuilder.QueryTypeBuilder, querybuilder.QueryTypeBuilderAI), mcp.Description("'builder_query' (default) lists the fields of signal. Set 'builder_ai_query' for AI observability queries; it also lists the trace-level fields, such as trace.estimated_total_cost, and gen_ai.* attributes.")),
	)

	h.addTool(s, getFieldKeysTool, h.handleGetFieldKeys)

	getFieldValuesTool := mcp.NewTool("signoz_get_field_values",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user knows a field key and needs its observed values for a metrics, traces, or logs filter. It returns values, not field names; use signoz_get_field_keys when the key is unknown. Match signal and fieldContext to the query that will use the value. Values come from all retained metadata; this tool takes no time range. SigNoz sets data.complete=false when it cut the list; narrow with searchText, or raise limit."),
		mcp.WithString("signal", mcp.Required(), mcp.Enum("metrics", "traces", "logs"), mcp.Description("Signal type: 'metrics', 'traces', or 'logs'.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Field name to get values for (e.g., 'service.name', 'http.status_code').")),
		mcp.WithString("searchText", mcp.Description("Filter the returned values by substring (optional).")),
		mcp.WithString("metricName", mcp.Description("Metric name to scope field values (optional, only relevant when signal=metrics).")),
		mcp.WithString("fieldContext", mcp.Enum("resource", "attribute", "scope", "log", "span", "metric", "body"), mcp.Description(fieldContextParamDesc+" Set this when the same key name exists in more than one context to disambiguate which one to fetch values for.")),
		mcp.WithString("source", mcp.Description("For signal=metrics, set \"meter\" to fetch Cost Meter field values; omit for the default metrics store. Omit for logs and traces.")),
		mcp.WithString("limit", intOrStringType(), mcp.Description(fieldValuesLimitParamDesc)),
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
	limit, err := intArg(args, "limit", 0)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_field_keys", slog.String("signal", signal), slog.String("queryType", queryType), slog.String("searchText", searchText))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	var result json.RawMessage
	// Like an out-of-enum signal, an out-of-enum queryType runs best-effort (here on the
	// default endpoint) and the schema validation notice flags it.
	if queryType == querybuilder.QueryTypeBuilderAI {
		result, err = client.GetAIFieldKeys(ctx, searchText, fieldContext, fieldDataType, limit)
	} else {
		result, err = client.GetFieldKeys(ctx, signal, metricName, searchText, fieldContext, fieldDataType, source, limit)
	}
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get field keys", err, slog.String("signal", signal), slog.String("queryType", queryType))
		return upstreamError(err), nil
	}
	return h.fieldListResult(ctx, "signoz_get_field_keys", result, "searchText or fieldContext"), nil
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
	limit, err := intArg(args, "limit", 0)
	if err != nil {
		return errorWithCode(CodeValidationFailed, err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_field_values", slog.String("signal", signal), slog.String("name", name))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.GetFieldValues(ctx, signal, name, metricName, searchText, fieldContext, source, limit)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get field values", err, slog.String("signal", signal), slog.String("name", name))
		return upstreamError(err), nil
	}
	return h.fieldListResult(ctx, "signoz_get_field_values", result, "searchText"), nil
}

// fieldListResult adds next steps when SigNoz cut a key or value list. A
// response without data.complete is drift: it passes through with a WARN.
func (h *Handler) fieldListResult(ctx context.Context, toolName string, result json.RawMessage, narrowWith string) *mcp.CallToolResult {
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	// A pointer, because json.Unmarshal leaves a bool false on null.
	var complete *bool
	err := json.Unmarshal(result, &envelope)
	if err == nil {
		raw, ok := envelope.Data["complete"]
		if !ok {
			err = errors.New("response has no data.complete")
		} else {
			err = json.Unmarshal(raw, &complete)
		}
	}
	if err == nil && complete == nil {
		err = errors.New("data.complete is null")
	}
	if err != nil {
		h.logger.WarnContext(ctx, "Field lookup response has no readable data.complete", slog.String("tool", toolName), logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(result)))
		return mcp.NewToolResultText(string(result))
	}
	if *complete {
		return mcp.NewToolResultText(string(result))
	}
	return resultWithNotes(result, "note: SigNoz cut this list (data.complete=false). To see more, narrow it with "+narrowWith+", or raise limit.")
}
