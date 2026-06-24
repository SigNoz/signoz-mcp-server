package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func (h *Handler) RegisterServiceHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering service handlers")

	listTool := mcp.NewTool("signoz_list_services",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("List all services in SigNoz. Defaults to last 6 hours if no time specified. IMPORTANT: This tool supports pagination using 'limit' and 'offset' parameters. The response includes 'pagination' metadata with 'total', 'hasMore', and 'nextOffset' fields. When searching for a specific service, ALWAYS check 'pagination.hasMore' - if true, continue paginating through all pages using 'nextOffset' until you find the item or 'hasMore' is false. Never conclude an item doesn't exist until you've checked all pages. Default: limit=50, offset=0."),
		mcp.WithString("timeRange", mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", mcp.Description("Start time in nanoseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in nanoseconds (optional, defaults to now)")),
		mcp.WithString("limit", mcp.Description("Maximum number of services to return per page. Use this to paginate through large result sets. Default: 50. Example: '50' for 50 results, '100' for 100 results. Must be greater than 0.")),
		mcp.WithString("offset", mcp.Description("Number of results to skip before returning results. Use for pagination: offset=0 for first page, offset=50 for second page (if limit=50), offset=100 for third page, etc. Check 'pagination.nextOffset' in the response to get the next page offset. Default: 0. Must be >= 0.")),
	)

	addTool(s, listTool, h.handleListServices)

	getOpsTool := mcp.NewTool("signoz_get_service_top_operations",
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("searchContext", mcp.Description("The user's original question or search text that triggered this tool call. Always include the user's raw query here for better results.")),
		mcp.WithDescription("Get top operations for a specific service. Defaults to last 6 hours if no time specified."),
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
		mcp.WithString("timeRange", mcp.Description(timeRangeDesc("Defaults to last 6 hours if not provided."))),
		mcp.WithString("start", mcp.Description("Start time in nanoseconds (optional, defaults to 6 hours ago)")),
		mcp.WithString("end", mcp.Description("End time in nanoseconds (optional, defaults to now)")),
		mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("Optional list of tag filter strings. A JSON-array string (e.g. \"[\\\"k=v\\\"]\") is also accepted for back-compat.")),
	)

	addTool(s, getOpsTool, h.handleGetServiceTopOperations)
}

func (h *Handler) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	start, end := timeutil.GetTimestampsWithDefaults(args, "ns")
	limit, offset := paginate.ParseParams(req.Params.Arguments)

	h.logger.DebugContext(ctx, "Tool called: signoz_list_services", slog.String("start", start), slog.String("end", end), slog.Int("limit", limit), slog.Int("offset", offset))
	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.ListServices(ctx, start, end)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list services", slog.String("start", start), slog.String("end", end), logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}

	var services []any
	if err := json.Unmarshal(result, &services); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse services response", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to parse response: " + err.Error()), nil
	}

	if base, hasURL := util.GetSigNozURL(ctx); hasURL {
		for _, item := range services {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["serviceName"].(string)
			if webURL, ok := util.ResourceWebURL(base, "service", name); ok {
				m["webUrl"] = webURL
			}
		}
	}

	total := len(services)
	pagedServices := paginate.Array(services, offset, limit)

	resultJSON, err := paginate.Wrap(pagedServices, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap services with pagination", logpkg.ErrAttr(err))
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

// tagsValidationError is the single canonical validation message for a
// malformed "tags" argument. Kept as one constant so the real-array and
// legacy-string paths report identically.
//
// NOTE(family-b-merge): once Family B's shared validationError(field, reason)
// helper lands, route this through it for the canonical envelope.
const tagsValidationError = `parameter validation failed: "tags" must be an array of strings (e.g. {"tags": ["env=prod"]}); null elements, numbers, and other non-string elements are not allowed`

// parseTagsParam normalizes the optional "tags" argument into a JSON array
// body for the upstream call. It enforces a strict array-of-STRINGS contract on
// both the canonical real array (the declared WithArray-of-strings schema) and
// the legacy JSON-array-string form: null elements, numbers, and any other
// non-string element are rejected. An empty/absent value yields an empty array.
// The returned body is always re-marshaled from the validated []string, so the
// upstream call receives a clean, canonical JSON array regardless of input form.
func parseTagsParam(v any) (json.RawMessage, error) {
	switch t := v.(type) {
	case nil:
		return json.RawMessage("[]"), nil
	case string:
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			return json.RawMessage("[]"), nil
		}
		// Legacy form: a JSON-array literal passed as a string. Decode to a
		// generic value and run it through the SAME element validator as the
		// real-array path — decoding straight into []string is too lenient
		// (the JSON literal `null` decodes to a nil slice, and a `null` element
		// silently becomes ""), so we must reject those explicitly.
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return nil, errors.New(tagsValidationError)
		}
		arr, ok := decoded.([]any)
		if !ok {
			// Not a JSON array (e.g. `null`, a number, an object).
			return nil, errors.New(tagsValidationError)
		}
		return validateAndMarshalTags(arr)
	case []any:
		// Canonical form: a real JSON array.
		return validateAndMarshalTags(t)
	default:
		return nil, errors.New(tagsValidationError)
	}
}

// validateAndMarshalTags enforces the strict array-of-STRINGS contract on a
// decoded JSON array and re-marshals it as a canonical JSON array body. Every
// element must be a non-null string: a JSON null decodes to a nil interface and
// numbers/bools/objects decode to their own Go types, so the type assertion
// rejects all of them. An empty array yields "[]" (not "null").
func validateAndMarshalTags(arr []any) (json.RawMessage, error) {
	tags := make([]string, 0, len(arr))
	for _, el := range arr {
		s, ok := el.(string)
		if !ok {
			return nil, errors.New(tagsValidationError)
		}
		tags = append(tags, s)
	}
	if len(tags) == 0 {
		return json.RawMessage("[]"), nil
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return nil, errors.New(tagsValidationError)
	}
	return json.RawMessage(b), nil
}

func (h *Handler) handleGetServiceTopOperations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	service, ok := args["service"].(string)
	if !ok {
		h.logger.WarnContext(ctx, "Invalid service parameter type", slog.Any("type", args["service"]))
		return mcp.NewToolResultError(`Parameter validation failed: "service" must be a string. Example: {"service": "frontend-api", "timeRange": "1h"}`), nil
	}
	if service == "" {
		h.logger.WarnContext(ctx, "Empty service parameter")
		return mcp.NewToolResultError(`Parameter validation failed: "service" cannot be empty. Provide a valid service name. Use signoz_list_services tool to see available services.`), nil
	}

	start, end := timeutil.GetTimestampsWithDefaults(args, "ns")

	tags, err := parseTagsParam(args["tags"])
	if err != nil {
		h.logger.WarnContext(ctx, "Invalid tags parameter", slog.Any("type", args["tags"]))
		return mcp.NewToolResultError(err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_service_top_operations",
		slog.String("start", start),
		slog.String("end", end),
		slog.String("service", service))

	client, err := h.GetClient(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := client.GetServiceTopOperations(ctx, start, end, service, tags)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get service top operations",
			slog.String("start", start),
			slog.String("end", end),
			slog.String("service", service),
			logpkg.ErrAttr(err))
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(result)), nil
}
