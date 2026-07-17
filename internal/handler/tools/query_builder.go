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
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterQueryBuilderV5Handlers(s *server.MCPServer) {
	h.logger.Debug("Registering query builder v5 handlers")

	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription(
			"Execute a raw SigNoz Query Builder v5 query as an escape hatch for multi-query, formula, or other shapes the higher-level tools cannot express. "+
				"Prefer signoz_search_logs/signoz_search_traces for raw rows, signoz_aggregate_logs/signoz_aggregate_traces for grouped or top-N analysis, and signoz_query_metrics for metrics.\n\n"+
				"Read the guide for the signal you are querying: signoz://logs/query-builder-guide for logs, signoz://traces/query-builder-guide for traces, and signoz://metrics-aggregation-guide for metrics or formulas. "+
				"Every builder_query and builder_formula must include a positive limit plus explicit v5 spec.order. Standalone omitted/null/zero bounds normalize to 100 rows or groups; builder queries referenced by a formula normalize to 10000 because base limits are applied before formula evaluation, while the formula result stays at 100. "+
				"The v5 wire field is spec.order, not the dashboard/editor field orderBy.\n\n"+
				"For promql envelopes also read signoz://promql/instructions — "+
				"OTel metric names with dots MUST use the Prometheus 3.x UTF-8 quoted-selector form ({\"metric.name.with.dots\"}). "+
				"Underscored / __name__ / bare-dotted forms silently return no data.\n\n"+
				"See docs: https://signoz.io/docs/userguide/query-builder-v5/",
		),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables")),
	)

	h.addTool(s, executeQuery, h.handleExecuteBuilderQuery)

	tracesQueryBuilderGuide := mcp.NewResource(
		"signoz://traces/query-builder-guide",
		"Traces Query Builder Guide",
		mcp.WithResourceDescription("SigNoz Query Builder v5 traces guide: filter expression syntax, canonical built-in span columns, explicit raw/aggregate result bounds and ordering, and executable examples for raw, scalar, and time-series queries."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(tracesQueryBuilderGuide, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     querybuilder.TracesQueryBuilderGuide,
			},
		}, nil
	})

	logsQueryBuilderGuide := mcp.NewResource(
		"signoz://logs/query-builder-guide",
		"Logs Query Builder Guide",
		mcp.WithResourceDescription("SigNoz Query Builder v5 logs guide: filter expression syntax, explicit raw/aggregate result bounds and ordering, stable timestamp/id pagination, body search, and executable examples for raw, scalar, and time-series queries."),
		mcp.WithMIMEType("text/plain"),
	)

	s.AddResource(logsQueryBuilderGuide, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     querybuilder.LogsQueryBuilderGuide,
			},
		}, nil
	})
}

func (h *Handler) handleExecuteBuilderQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_execute_builder_query")

	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid arguments payload type", slog.Any("type", req.Params.Arguments))
		return notAJSONObjectError(), nil
	}

	queryObj, ok := args["query"].(map[string]any)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid query parameter type", slog.Any("type", args["query"]))
		return validationError("query", "must be a JSON object"), nil
	}

	queryJSON, err := json.Marshal(queryObj)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal query object", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal query object: " + err.Error()), nil
	}

	var queryPayload types.QueryPayload
	if err := json.Unmarshal(queryJSON, &queryPayload); err != nil {
		// User-input structural mistake: code as VALIDATION_FAILED, not a marshal error.
		h.logger.ErrorContext(ctx, "Failed to unmarshal query payload", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "invalid query payload structure: "+err.Error()), nil
	}

	if err := queryPayload.Validate(); err != nil {
		// Validate() rejects user-input mistakes: route through VALIDATION_FAILED.
		h.logger.ErrorContext(ctx, "Query validation failed", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "query validation error: "+err.Error()), nil
	}

	finalQueryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal validated query payload", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal validated query payload: " + err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := client.QueryBuilderV5(ctx, finalQueryJSON)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to execute query builder v5", err)
		return upstreamError(err), nil
	}

	h.logger.DebugContext(ctx, "Successfully executed query builder v5")

	// Surface non-fatal backend warnings as a note + WARN log, matching the five
	// sibling QueryBuilderV5 callers (search/aggregate logs & traces, query_metrics).
	// Returning the body verbatim previously dropped them entirely.
	var notes []string
	if len(queryPayload.AppliedBounds) > 0 {
		notes = append(notes, queryBoundsDecisionsNote(queryPayload.AppliedBounds, queryPayload.RequestType))
	}
	warnings := extractBackendWarningMessages(data)
	warnBackendWarnings(ctx, h.logger, "signoz_execute_builder_query", warnings)
	warnUnparsedWarningEnvelope(ctx, h.logger, "signoz_execute_builder_query", data, len(warnings))
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(data, notes...), nil
}

func queryBoundsDecisionsNote(applied []types.AppliedQueryBounds, requestType string) string {
	var b strings.Builder
	b.WriteString("[Decisions applied]\n")
	for _, bounds := range applied {
		var decisions []string
		if bounds.LimitDefaulted {
			if bounds.FormulaInput {
				decisions = append(decisions, fmt.Sprintf("limit=%d (formula-input default; applied before formula evaluation)", bounds.Limit))
			} else {
				decisions = append(decisions, fmt.Sprintf("limit=%d (request-type default)", bounds.Limit))
			}
		}
		if bounds.OrderDefaulted {
			decisions = append(decisions, fmt.Sprintf("order=%s (signal-safe default)", formatQueryOrder(bounds.Order)))
		}
		b.WriteString(fmt.Sprintf("  %s: %s\n", bounds.QueryName, strings.Join(decisions, ", ")))
	}
	if requestType == "time_series" {
		b.WriteString("  NOTE: time_series limits select top groups using the ordering across the entire time range; a short-lived spike can fall outside the selected groups.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatQueryOrder(order []types.Order) string {
	parts := make([]string, 0, len(order))
	for _, item := range order {
		parts = append(parts, strings.TrimSpace(item.Key.Name)+" "+strings.ToLower(strings.TrimSpace(item.Direction)))
	}
	return strings.Join(parts, ", ")
}
