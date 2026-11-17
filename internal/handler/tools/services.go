package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func (h *Handler) RegisterServiceHandlers(s *mcp.Server) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("signoz_list_services",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants APM services with trace activity and their call or latency summaries in a time window. It returns traced-service records sorted by name and paginated; absence means no trace activity in that window, not that a matching service.name never appears in logs. For log values use signoz_get_field_values with signal=\"logs\" and name=\"service.name\"; for one service's operations use signoz_get_service_top_operations. Follow pagination.nextOffset until hasMore=false before concluding a traced service is absent."),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeParamDescription("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum services per page. Default: 50; max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of services to skip. Default: 0; use pagination.nextOffset for the next page.")),
	)

	h.addTool(s, listTool, h.handleListServices)

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user wants the built-in operation table for one traced service in a time window. It ranks operation names by p99 latency and returns p50, p95, p99, call count, and error count. Use signoz_list_services to discover active traced service names. For custom aggregation, grouping, time series, cross-service comparison, or arbitrary trace filters, use signoz_aggregate_traces instead. The optional tags parameter is a JSON-encoded TagQueryParam array."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Exact traced service name, typically from signoz_list_services.")),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeParamDescription("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("tags", mcp.Description("JSON-encoded TagQueryParam array; omit for no tag filter. Example: [{\"key\":\"http.method\",\"tagType\":\"SpanAttribute\",\"operator\":\"In\",\"stringValues\":[\"GET\"]}]. Pass the array as a string, not as a JSON array value.")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum operations to return, slowest p99 first. Default: 50; max: 5000 (higher values are clamped). A note says how many operations were omitted.")),
	)

	h.addTool(s, getOpsTool, h.handleGetServiceTopOperations)
}

func (h *Handler) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	h.logger.DebugContext(ctx, "Tool called: signoz_list_services", slog.String("start", start), slog.String("end", end), slog.Int("limit", limit), slog.Int("offset", offset))
	client, base, clientErr := h.getResourceClient(ctx)
	if clientErr != nil {
		return clientErr, nil
	}
	result, err := client.ListServices(ctx, start, end)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list services", err, slog.String("start", start), slog.String("end", end))
		return upstreamError(err), nil
	}

	var envelope struct {
		Data *[]any `json:"data"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil || envelope.Data == nil {
		h.logger.WarnContext(ctx, "Services response is not a data array", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(result)))
		return upstreamResponseError("services response has no data array"), nil
	}
	services := *envelope.Data

	// SigNoz orders services by p99, which moves between calls; a stable
	// order keeps offset pages from skipping or repeating services.
	sort.SliceStable(services, func(i, j int) bool {
		return serviceItemName(services[i]) < serviceItemName(services[j])
	})
	if base != "" {
		for _, item := range services {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if webURL, ok := util.ResourceWebURL(base, "service", serviceItemName(m)); ok {
				m["webUrl"] = webURL
			}
		}
	}

	total := len(services)
	pagedServices := paginate.Array(services, offset, limit)

	resultJSON, err := paginate.Wrap(pagedServices, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap services with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func serviceItemName(item any) string {
	m, _ := item.(map[string]any)
	name, _ := m["serviceName"].(string)
	return name
}

func (h *Handler) handleGetServiceTopOperations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	service, errResult := requireStringArg(args, "service")
	if errResult != nil {
		h.logger.WarnContext(ctx, "Invalid service parameter", slog.Any("type", args["service"]))
		return errResult, nil
	}

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)

	limit, err := intArg(args, "limit", topOperationsDefaultLimit)
	if err != nil {
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()+`. Pass "limit" as a positive integer, for example 50 or "50".`), nil
	}
	limitClamped := limit > signozclient.TopOperationsLimit
	if limitClamped {
		limit = signozclient.TopOperationsLimit
	}

	// tags is passed through to SigNoz verbatim as its tag filter array; an
	// absent or non-string value means no tag filter.
	var tags json.RawMessage
	if t, ok := args["tags"].(string); ok && t != "" {
		tags = json.RawMessage(t)
	} else {
		tags = json.RawMessage("[]")
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_service_top_operations",
		slog.String("start", start),
		slog.String("end", end),
		slog.String("service", service))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get service top operations", err, slog.String("start", start), slog.String("end", end), slog.String("service", service))
		return upstreamError(err), nil
	}

	// SigNoz sends "data": null when the service has no operations in the window.
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	operations := []json.RawMessage{}
	err = json.Unmarshal(result, &envelope)
	if err == nil && len(envelope.Data) == 0 {
		err = errors.New("response has no data field")
	}
	if err == nil && string(envelope.Data) != "null" {
		err = json.Unmarshal(envelope.Data, &operations)
	}
	if err != nil {
		h.logger.WarnContext(ctx, "Top operations response is not a data array", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(result)))
		return upstreamResponseError("top operations response has no data array"), nil
	}
	// SigNoz returns the rows slowest p99 first, so the cut keeps the slowest.
	// The client always asks for TopOperationsLimit rows so the note can count what was cut.
	total := len(operations)
	if total > limit {
		operations = operations[:limit]
	}
	payload, err := json.Marshal(operations)
	if err != nil {
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	return resultWithNotes(payload, topOperationsNotes(len(operations), total, limitClamped)...), nil
}

// topOperationsDefaultLimit keeps a busy service's table inside common MCP
// client output limits; signoz_list_services uses the same default page size.
const topOperationsDefaultLimit = 50

// topOperationsNotes says when the table was cut at the caller's limit and how
// many rows were omitted. When SigNoz itself returned TopOperationsLimit rows,
// the total is only a lower bound.
func topOperationsNotes(returned, total int, limitClamped bool) []string {
	var notes []string
	if limitClamped {
		notes = append(notes, fmt.Sprintf("note: limit clamped to %d, the most operations SigNoz returns in one call.", signozclient.TopOperationsLimit))
	}
	upstreamFull := total >= signozclient.TopOperationsLimit
	switch {
	case returned < total && upstreamFull:
		notes = append(notes, fmt.Sprintf(
			"note: returned the %d slowest operations by p99; at least %d more were omitted (hasMore=true). Raise limit (max %d) or use a shorter time range or tags to see them.",
			returned, total-returned, signozclient.TopOperationsLimit))
	case returned < total:
		notes = append(notes, fmt.Sprintf(
			"note: returned the %d slowest operations by p99 of %d; %d were omitted (hasMore=true). Raise limit (max %d) or use a shorter time range or tags to see them.",
			returned, total, total-returned, signozclient.TopOperationsLimit))
	case upstreamFull:
		notes = append(notes, limitOnlyCompletenessNote(returned, signozclient.TopOperationsLimit, true, "a shorter time range or tags"))
	}
	return notes
}
