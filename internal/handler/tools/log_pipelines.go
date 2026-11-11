package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// logPipelineSummary is the list view of one pipeline. The processor config and
// filter are deliberately omitted; use signoz_get_log_pipeline for those.
type logPipelineSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Alias          string `json:"alias"`
	Description    string `json:"description"`
	Enabled        bool   `json:"enabled"`
	OrderID        int    `json:"orderId"`
	ProcessorCount int    `json:"processorCount"`
}

func (h *Handler) RegisterLogPipelineHandlers(s *mcp.Server) {
	h.logger.Debug("Registering log pipeline handlers")

	listTool := mcp.NewTool("signoz_list_log_pipelines",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants the log ingestion and processing pipelines configured in this SigNoz instance, such as when checking why incoming logs are not parsed or enriched. It returns one summary per pipeline from the latest configuration: id, name, alias, description, enabled state, order, and processor count. An empty list means no user pipelines are configured. Use signoz_get_log_pipeline for one pipeline's full processors and filter; use signoz_search_logs for log records."),
	)
	h.addTool(s, listTool, h.handleListLogPipelines)

	getTool := mcp.NewTool("signoz_get_log_pipeline",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants one log pipeline's complete configuration by id: its processors, filter, enabled state, and audit fields, such as when explaining why incoming logs are not parsed as expected. Use signoz_list_log_pipelines first when the id is unknown. Pipeline ids change when the configuration is saved, so an id from an older version may no longer exist. Use signoz_search_logs for the logs themselves."),
		mcp.WithString("id", mcp.Description("Log pipeline id from signoz_list_log_pipelines. Required.")),
	)
	h.addTool(s, getTool, h.handleGetLogPipeline)
}

func (h *Handler) handleListLogPipelines(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, errResult := requireArgsMap(req.Params.Arguments); errResult != nil {
		return errResult, nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_list_log_pipelines")
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	raw, err := client.ListLogPipelines(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list log pipelines", err)
		return upstreamError(err), nil
	}

	pipelines, errResult := h.decodeLogPipelines(ctx, raw)
	if errResult != nil {
		return errResult, nil
	}
	summaries := make([]logPipelineSummary, 0, len(pipelines))
	for _, rawPipeline := range pipelines {
		summary, err := summarizeLogPipeline(rawPipeline)
		if err != nil {
			h.logger.WarnContext(ctx, "Log pipeline item is not a valid pipeline object", logpkg.ErrAttr(err), slog.String("pipeline", logpkg.TruncBody(rawPipeline)))
			return upstreamResponseError("log pipelines response contains an invalid pipeline object"), nil
		}
		summaries = append(summaries, summary)
	}

	payload, err := json.Marshal(map[string]any{
		"pipelines": summaries,
		"total":     len(summaries),
	})
	if err != nil {
		return InternalErrorResult("failed to marshal log pipeline list: " + err.Error()), nil
	}
	return structuredResult(payload), nil
}

func (h *Handler) handleGetLogPipeline(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	rawID, present := args["id"]
	id, isString := rawID.(string)
	switch {
	case !present || (isString && id == ""):
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide a log pipeline id from signoz_list_log_pipelines.`), nil
	case !isString:
		return validationError("id", "must be a string; use the id from signoz_list_log_pipelines"), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_log_pipeline", slog.String("id", id))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	raw, err := client.ListLogPipelines(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get log pipeline", err, slog.String("id", id))
		return upstreamError(err), nil
	}

	pipelines, errResult := h.decodeLogPipelines(ctx, raw)
	if errResult != nil {
		return errResult, nil
	}
	for _, rawPipeline := range pipelines {
		var probe struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rawPipeline, &probe); err != nil {
			h.logger.WarnContext(ctx, "Log pipeline item is not a valid pipeline object", logpkg.ErrAttr(err), slog.String("pipeline", logpkg.TruncBody(rawPipeline)))
			return upstreamResponseError("log pipelines response contains an invalid pipeline object"), nil
		}
		if probe.ID == id {
			return structuredResult(rawPipeline), nil
		}
	}

	return errorWithCode(CodeNotFound, fmt.Sprintf(`Log pipeline %q was not found in the latest configuration. Use signoz_list_log_pipelines to find the current id; pipeline ids change when the configuration is saved.`, id)), nil
}

// decodeLogPipelines unwraps the {status, data: {pipelines}} envelope. A null
// pipelines field is a valid empty configuration; a missing field or a
// non-array value is upstream drift and surfaces as a coded error.
func (h *Handler) decodeLogPipelines(ctx context.Context, raw json.RawMessage) ([]json.RawMessage, *mcp.CallToolResult) {
	var envelope struct {
		Data *struct {
			Pipelines json.RawMessage `json:"pipelines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		h.logger.WarnContext(ctx, "Log pipelines response is not valid JSON", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(raw)))
		return nil, upstreamResponseError("log pipelines response is not valid JSON")
	}
	if envelope.Data == nil {
		h.logger.WarnContext(ctx, "Log pipelines response has no data object", slog.String("response", logpkg.TruncBody(raw)))
		return nil, upstreamResponseError("log pipelines response has no data object")
	}
	if len(envelope.Data.Pipelines) == 0 {
		h.logger.WarnContext(ctx, "Log pipelines response has no pipelines field", slog.String("response", logpkg.TruncBody(raw)))
		return nil, upstreamResponseError("log pipelines response has no pipelines field")
	}
	if string(envelope.Data.Pipelines) == "null" {
		return []json.RawMessage{}, nil
	}
	var pipelines []json.RawMessage
	if err := json.Unmarshal(envelope.Data.Pipelines, &pipelines); err != nil {
		h.logger.WarnContext(ctx, "Log pipelines response pipelines field is not an array", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(raw)))
		return nil, upstreamResponseError("log pipelines response has no pipelines array")
	}
	return pipelines, nil
}

func summarizeLogPipeline(raw json.RawMessage) (logPipelineSummary, error) {
	var pipeline struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Alias       string            `json:"alias"`
		Description string            `json:"description"`
		Enabled     bool              `json:"enabled"`
		OrderID     int               `json:"orderId"`
		Config      []json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(raw, &pipeline); err != nil {
		return logPipelineSummary{}, err
	}
	return logPipelineSummary{
		ID:             pipeline.ID,
		Name:           pipeline.Name,
		Alias:          pipeline.Alias,
		Description:    pipeline.Description,
		Enabled:        pipeline.Enabled,
		OrderID:        pipeline.OrderID,
		ProcessorCount: len(pipeline.Config),
	}, nil
}
