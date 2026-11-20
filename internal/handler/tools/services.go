package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"time"

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
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
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
		mcp.WithString("start", intOrStringType(), mcp.Description(startParamDescription)),
		mcp.WithString("end", intOrStringType(), mcp.Description(endParamDescription)),
		mcp.WithString("tags", mcp.Description("JSON-encoded TagQueryParam array; omit for no tag filter. Example: [{\"key\":\"http.method\",\"tagType\":\"SpanAttribute\",\"operator\":\"In\",\"stringValues\":[\"GET\"]}]. Pass the array as a string, not as a JSON array value.")),
	)

	h.addTool(s, getOpsTool, h.handleGetServiceTopOperations)

	serviceMapTool := mcp.NewTool("signoz_get_service_map",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithDescription("Use this when the user asks which services call a service, what a service depends on, or which dependency is slow or failing. It returns the dependency graph behind SigNoz's Service Map for a time window: one edge per caller (parent) and callee (child) with callCount, callRate (calls per second), errorRate (percent of calls that failed), and p50 to p99 latency in nanoseconds. Set service and direction to keep one service's edges, and environment, cluster, or namespace to narrow the graph. Databases and message systems are callees named by their system, such as redis or kafka; a message system is the child of both its producers and its consumers. Edges come from per-minute rollups of traced calls, so a window shorter than a minute can return nothing, and an uninstrumented caller leaves no edge. Use signoz_list_services for per-service totals, signoz_get_service_top_operations for one service's operations, and signoz_aggregate_traces for custom span statistics."),
		mcp.WithString("service", mcp.Description("Exact service.name value whose edges to keep, or a database or message system named in an edge. Discover service names with signoz_get_field_values with signal=\"traces\", fieldContext=\"resource\", and name=\"service.name\". Omit for the whole graph.")),
		mcp.WithString("direction", mcp.DefaultString("both"), mcp.Enum("both", "upstream", "downstream"), mcp.Description("Which of service's edges to keep: upstream (the services that call it), downstream (the services, databases, and message systems it calls), or both (default). Requires service.")),
		mcp.WithString("timeRange", mcp.DefaultString("6h"), mcp.Description(timeRangeParamDescription("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", intOrStringType(), mcp.Description("Start time in unix milliseconds (optional, defaults to 6 hours ago).")),
		mcp.WithString("end", intOrStringType(), mcp.Description("End time in unix milliseconds (optional, defaults to now).")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum edges per page. Default: 50; max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of edges to skip. Default: 0; use pagination.nextOffset for the next page.")),
		mcp.WithString("environment", mcp.Description("Keep only edges recorded with this deployment.environment resource value. Discover values with signoz_get_field_values with signal=\"traces\", fieldContext=\"resource\", and name=\"deployment.environment\".")),
		mcp.WithString("cluster", mcp.Description("Keep only edges recorded with this k8s.cluster.name resource value. Discover values with signoz_get_field_values with signal=\"traces\", fieldContext=\"resource\", and name=\"k8s.cluster.name\".")),
		mcp.WithString("namespace", mcp.Description("Keep only edges recorded with this k8s.namespace.name resource value. Discover values with signoz_get_field_values with signal=\"traces\", fieldContext=\"resource\", and name=\"k8s.namespace.name\".")),
	)

	h.addTool(s, serviceMapTool, h.handleGetServiceMap)
}

const (
	serviceMapUnitsNote      = "note: p50 to p99 are latencies in nanoseconds; callRate is calls per second over the window; errorRate is the percentage of calls that failed (0.5 means 0.5%). On an edge between two services, callCount and callRate count only the calls whose caller and callee spans SigNoz paired at ingestion, so they can be lower than a span count from signoz_aggregate_traces."
	serviceMapEmptyGraphNote = "note: the graph has no edges for this time range and filters. Edges come from per-minute rollups of traced calls; widen the time range or drop environment, cluster, and namespace before concluding there are no dependencies."
	// The requested name is not echoed back, so a long argument cannot grow the result.
	serviceMapNoSuchServiceNote = "note: no edge in this window has the requested service as parent or child. Service names are exact and case-sensitive; check the name with signoz_get_field_values with signal=\"traces\", fieldContext=\"resource\", and name=\"service.name\", or widen the time range."
)

// SigNoz builds the dependency graph from a rollup that keeps only these
// resource keys and skips a filter on any other key without an error, so the
// tool takes one plain parameter per key instead of SigNoz's tag array.
var serviceMapFilters = []struct{ param, key string }{
	{"environment", "deployment.environment"},
	{"cluster", "k8s.cluster.name"},
	{"namespace", "k8s.namespace.name"},
}

func (h *Handler) handleGetServiceMap(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}

	service, errResult := optionalStringArg(args, "service")
	if errResult != nil {
		return errResult, nil
	}
	direction, errResult := optionalStringArg(args, "direction")
	if errResult != nil {
		return errResult, nil
	}
	switch direction {
	case "":
		direction = "both"
	case "both", "upstream", "downstream":
	default:
		return validationError("direction", `must be one of "both", "upstream", or "downstream"`), nil
	}
	if direction != "both" && service == "" {
		return validationError("direction", `is relative to one service; set "service" to the service whose callers or callees you want`), nil
	}
	tags, errResult := serviceMapTags(args)
	if errResult != nil {
		return errResult, nil
	}

	// Reject a present-but-malformed start/end loudly; otherwise
	// GetTimestampsWithDefaults silently falls back to the default window.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		h.logger.WarnContext(ctx, "Invalid explicit timestamp", logpkg.ErrAttr(err))
		return errorWithCode(CodeValidationFailed, "Parameter validation failed: "+err.Error()), nil
	}
	start, end := timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)
	limit, offset, limitClamped := paginate.ParseParamsClamped(args)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_service_map",
		slog.String("start", start), slog.String("end", end),
		slog.String("service", service), slog.String("direction", direction))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	result, err := client.GetServiceMap(ctx, start, end, tags)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get service map", err, slog.String("start", start), slog.String("end", end))
		return upstreamError(err), nil
	}

	edges, ok := h.decodeServiceMapEdges(ctx, result)
	if !ok {
		return upstreamResponseError("service map response is not an array of edges with parent and child names"), nil
	}

	var notes []string
	switch {
	case len(edges) == 0:
		notes = append(notes, serviceMapEmptyGraphNote)
	case service != "":
		var touched bool
		edges, touched = filterServiceMapEdges(edges, service, direction)
		if !touched {
			notes = append(notes, serviceMapNoSuchServiceNote)
		}
	}
	// A stable order keeps offset pages from skipping or repeating edges.
	sort.SliceStable(edges, func(i, j int) bool {
		pi, ci := serviceMapEdgeEnds(edges[i])
		pj, cj := serviceMapEdgeEnds(edges[j])
		if pi != pj {
			return pi < pj
		}
		return ci < cj
	})

	resultJSON, err := paginate.Wrap(paginate.Array(edges, offset, limit), len(edges), offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap service map with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	res := listResult(resultJSON, limitClamped)
	for _, note := range append(notes, serviceMapUnitsNote) {
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res, nil
}

// serviceMapTags builds SigNoz's tag filter from the environment, cluster, and
// namespace arguments, or returns nil when none is set.
func serviceMapTags(args map[string]any) (json.RawMessage, *mcp.CallToolResult) {
	type tagQuery struct {
		Key          string   `json:"key"`
		TagType      string   `json:"tagType"`
		Operator     string   `json:"operator"`
		StringValues []string `json:"stringValues"`
	}
	var tags []tagQuery
	for _, filter := range serviceMapFilters {
		value, errResult := optionalStringArg(args, filter.param)
		if errResult != nil {
			return nil, errResult
		}
		if value != "" {
			tags = append(tags, tagQuery{Key: filter.key, TagType: "ResourceAttribute", Operator: "In", StringValues: []string{value}})
		}
	}
	if len(tags) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(tags)
	if err != nil {
		return nil, InternalErrorResult("failed to encode the service map filters: " + err.Error())
	}
	return body, nil
}

// decodeServiceMapEdges reads the bare edge array SigNoz returns. Newer SigNoz
// routes wrap payloads as {"status":..,"data":..}, so it also accepts that form
// with a WARN; anything else fails loudly rather than reading as an empty graph.
func (h *Handler) decodeServiceMapEdges(ctx context.Context, raw json.RawMessage) ([]any, bool) {
	var edges []any
	if err := json.Unmarshal(raw, &edges); err != nil {
		var envelope struct {
			Data *[]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
			h.logger.WarnContext(ctx, "Service map response is not an edge array", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(raw)))
			return nil, false
		}
		h.logger.WarnContext(ctx, "Service map response arrived in a status/data envelope instead of a bare array", slog.Int("edges", len(*envelope.Data)))
		edges = *envelope.Data
	}
	// The service filter and the page order read parent and child, so an edge
	// without them would drop out of a service's edges without a trace.
	for i, edge := range edges {
		m, _ := edge.(map[string]any)
		_, hasParent := m["parent"].(string)
		_, hasChild := m["child"].(string)
		if !hasParent || !hasChild {
			h.logger.WarnContext(ctx, "Service map edge has no parent or child name", slog.Int("index", i), slog.String("response", logpkg.TruncBody(raw)))
			return nil, false
		}
	}
	if edges == nil {
		edges = []any{}
	}
	return edges, true
}

func serviceMapEdgeEnds(edge any) (parent, child string) {
	m, _ := edge.(map[string]any)
	parent, _ = m["parent"].(string)
	child, _ = m["child"].(string)
	return parent, child
}

// filterServiceMapEdges keeps the edges of service in direction and reports
// whether any edge in the graph touches service at all.
func filterServiceMapEdges(edges []any, service, direction string) ([]any, bool) {
	kept := make([]any, 0, len(edges))
	var touched bool
	for _, edge := range edges {
		parent, child := serviceMapEdgeEnds(edge)
		if parent == service || child == service {
			touched = true
		}
		if (direction != "upstream" && parent == service) || (direction != "downstream" && child == service) {
			kept = append(kept, edge)
		}
	}
	return kept, touched
}

func (h *Handler) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	window, errResult := serviceWindow(ctx, h, args)
	if errResult != nil {
		return errResult, nil
	}
	start, end := windowNanos(window)
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

	resultJSON, err := json.Marshal(struct {
		paginate.Response
		Window resultWindow `json:"window"`
	}{
		Response: paginate.Response{Data: pagedServices, Pagination: paginate.NewMetadata(total, offset, limit)},
		Window:   newResultWindow(window.StartMs(), window.EndMs()),
	})
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

	window, errResult := serviceWindow(ctx, h, args)
	if errResult != nil {
		return errResult, nil
	}
	start, end := windowNanos(window)

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
	payload, err := json.Marshal(operations)
	if err != nil {
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	notes := []string{windowNote(window.StartMs(), window.EndMs())}
	if len(operations) >= signozclient.TopOperationsLimit {
		notes = append(notes, limitOnlyCompletenessNote(len(operations), signozclient.TopOperationsLimit, true, "a shorter time range or tags"))
	}
	return resultWithNotes(payload, notes...), nil
}

// serviceWindow resolves the service tools' window, which defaults to 6h.
func serviceWindow(ctx context.Context, h *Handler, args map[string]any) (timeutil.Window, *mcp.CallToolResult) {
	w, err := timeutil.ResolveWindow(args, "6h", time.Now())
	if err != nil {
		h.logger.WarnContext(ctx, "Invalid time window", logpkg.ErrAttr(err))
		return timeutil.Window{}, errorWithCode(CodeValidationFailed, validationErrorPrefix+" "+err.Error())
	}
	return w, nil
}

// windowNanos formats w as the service routes take it: Unix nanoseconds in
// strings.
func windowNanos(w timeutil.Window) (start, end string) {
	return strconv.FormatInt(w.StartNs(), 10), strconv.FormatInt(w.EndNs(), 10)
}
