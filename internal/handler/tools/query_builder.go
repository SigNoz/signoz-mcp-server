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
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription(
			"Use this only when the user needs a SigNoz Query Builder v5 request that the dedicated log, trace, and metric tools cannot express, including multi-query requests, formulas, PromQL, and ClickHouse SQL. "+
				"Use signoz_search_logs/signoz_search_traces for raw rows, signoz_aggregate_logs/signoz_aggregate_traces for grouped or top-N analysis, and signoz_query_metrics for ordinary metrics queries. "+
				"Before composing the query, read the matching signoz://logs/query-builder-guide, signoz://traces/query-builder-guide, or signoz://metrics-aggregation-guide; formulas also require the metrics guide, and PromQL requires signoz://promql/instructions. "+
				"For predictable formulas, explicitly set each input builder_query limit to 10000, the builder_formula result limit to 100, and non-empty spec.order (not dashboard orderBy) on every builder_query and builder_formula; the server normalizes omissions.",
		),
		mcp.WithObject("query", mcp.Required(), mcp.Description("Complete SigNoz Query Builder v5 JSON object with schemaVersion, start, end, requestType, compositeQuery, formatOptions, and variables. For predictable bounds, explicitly supply a positive spec.limit and non-empty spec.order (not dashboard orderBy) for every builder_query and builder_formula; the server inserts signal-aware defaults when they are omitted. Missing or zero standalone and formula-result limits normalize to 100; builder queries feeding a formula normalize to 10000 because input limits apply before formula evaluation.")),
	)

	h.addTool(s, executeQuery, h.handleExecuteBuilderQuery)

	tracesQueryBuilderGuide := mcp.NewResource(
		"signoz://traces/query-builder-guide",
		"Traces Query Builder Guide",
		mcp.WithResourceDescription("Read this before writing Query Builder v5 JSON for traces or filtering on unfamiliar trace fields. It explains filter syntax, field discovery, built-in span fields, row and aggregate queries, limits, ordering, timestamps, and examples for rows, single values, and time series."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(querybuilder.TracesQueryBuilderGuide))),
	)

	h.addResource(s, tracesQueryBuilderGuide, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     querybuilder.TracesQueryBuilderGuide,
			},
		}, nil
	})

	logsQueryBuilderGuide := mcp.NewResource(
		"signoz://logs/query-builder-guide",
		"Logs Query Builder Guide",
		mcp.WithResourceDescription("Read this before writing Query Builder v5 JSON for logs or filtering on unfamiliar log fields. It explains filter syntax, field discovery, body and JSON-path search, row and aggregate queries, stable pagination, limits, ordering, timestamps, and examples for rows, single values, and time series."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(querybuilder.LogsQueryBuilderGuide))),
	)

	h.addResource(s, logsQueryBuilderGuide, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
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
		return InternalErrorResult("failed to marshal query object: " + err.Error()), nil
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
		return InternalErrorResult("failed to marshal validated query payload: " + err.Error()), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	data, err := client.QueryBuilderV5(ctx, finalQueryJSON)
	if err != nil {
		h.logQueryFailure(ctx, "Failed to execute query builder v5", err)
		return upstreamQueryError(err, ""), nil
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
