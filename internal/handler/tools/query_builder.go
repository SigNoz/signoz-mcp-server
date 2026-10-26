package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

func (h *Handler) RegisterQueryBuilderV5Handlers(s *mcp.Server) {
	h.logger.Debug("Registering query builder v5 handlers")

	// SigNoz Query Builder v5 tool - LLM builds structured query JSON and executes it
	executeQuery := mcp.NewTool("signoz_execute_builder_query",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription(
			"Use this only when dedicated log, trace, and metric tools cannot express the request, including multi-query requests, formulas, PromQL, and ClickHouse SQL. "+
				"Use signoz_search_logs/signoz_search_traces for raw rows, signoz_aggregate_logs/signoz_aggregate_traces for grouped or top-N analysis, and signoz_query_metrics for ordinary metrics queries. "+
				"Read the matching signoz://logs/query-builder-guide, signoz://traces/query-builder-guide, or signoz://metrics-aggregation-guide; formulas require the metrics guide, and PromQL requires signoz://promql/instructions. signoz://query-builder/examples holds a complete request per query type and request type. "+
				"Set each input builder_query limit to 10000, the builder_formula result limit to 100, and non-empty spec.order (not dashboard orderBy) on each builder_query and builder_formula; the server normalizes omissions.",
		),
		mcp.WithInputSchema[types.ExecuteBuilderQueryInput](),
	)

	h.addTool(s, executeQuery, h.handleExecuteBuilderQuery)

	queryBuilderExamples := mcp.NewResource(
		"signoz://query-builder/examples",
		"Query Builder Request Examples",
		mcp.WithResourceDescription("Read this when composing the query argument of signoz_execute_builder_query and a complete request shape is still needed after the signal's guide. It provides one query_range request from the SigNoz OpenAPI spec per query type (builder, formula, PromQL, ClickHouse SQL) and request type (raw, scalar, time_series) across logs, traces, and metrics."),
		mcp.WithMIMEType("text/markdown"),
		mcp.WithResourceSize(int64(len(querybuilder.Examples))),
	)
	h.addResource(s, queryBuilderExamples, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     querybuilder.Examples,
			},
		}, nil
	})

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
		mcp.WithResourceDescription("Read this before writing Query Builder v5 JSON for logs or filtering on unfamiliar log fields. It explains filter syntax, field discovery, body, JSON-path, and cross-field search(), row and aggregate queries, stable pagination, limits, ordering, timestamps, and examples for rows, single values, and time series."),
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

	// SigNoz exhausts its memory on a raw query whose start is missing or 0
	// (SigNoz/nerve-pod#439); this precondition stays until SigNoz rejects it.
	if start, isNumber := queryObj["start"].(float64); queryObj["start"] == nil || isNumber && start == 0 {
		return validationError("query.start", `is required: set start and end to Unix milliseconds, for example "start": 1697385600000`), nil
	}

	// The request is forwarded verbatim apart from the result bounds this server
	// inserts where the caller omitted them; SigNoz validates everything else.
	applied := querybuilder.ApplyBounds(queryObj)
	finalQueryJSON, err := json.Marshal(queryObj)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal query object", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal query object: " + err.Error()), nil
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
	if len(applied) > 0 {
		requestType, _ := queryObj["requestType"].(string)
		notes = append(notes, queryBoundsDecisionsNote(applied, requestType))
	}
	warnings := extractBackendWarningMessages(data)
	warnBackendWarnings(ctx, h.logger, "signoz_execute_builder_query", warnings)
	warnUnparsedWarningEnvelope(ctx, h.logger, "signoz_execute_builder_query", data, len(warnings))
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(data, notes...), nil
}

func queryBoundsDecisionsNote(applied []querybuilder.AppliedBounds, requestType string) string {
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
			decisions = append(decisions, fmt.Sprintf("order=%s (signal-safe default)", bounds.OrderText()))
		}
		fmt.Fprintf(&b, "  %s: %s\n", bounds.QueryName, strings.Join(decisions, ", "))
	}
	if requestType == "time_series" {
		b.WriteString("  NOTE: time_series limits select top groups using the ordering across the entire time range; a short-lived spike can fall outside the selected groups.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
