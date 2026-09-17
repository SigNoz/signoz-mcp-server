package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// Planned-maintenance (downtime) schedule tools. SigNoz has no Alertmanager
// silences API: muting is done with these schedules, which is also what makes
// signoz_list_alerts report an alert as silenced.
//
// Update (PUT) is intentionally not exposed here.

func (h *Handler) RegisterDowntimeScheduleHandlers(s *server.MCPServer) {
	h.logger.Debug("Registering downtime schedule handlers")

	listTool := mcp.NewTool("signoz_list_downtime_schedules",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants SigNoz planned-maintenance (downtime) schedules — the windows that mute alert notifications and make an alert report as silenced. SigNoz has no Alertmanager silences API, so these schedules are the muting mechanism. Returns each schedule's id, name, window, recurrence, muted alert IDs, scope, and server-derived status/kind. Use signoz_get_downtime_schedule for one schedule by id. Paginate with limit and offset."),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of schedules to return per page. Default: 50, max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of results to skip for pagination. Default: 0.")),
		mcp.WithBoolean("active", boolOrStringType(), mcp.Description("Filter to currently active windows when true, or exclude them when false. Omit to let the SigNoz backend apply its own default.")),
		mcp.WithBoolean("recurring", boolOrStringType(), mcp.Description("Filter to recurring windows when true, or fixed one-off windows when false. Omit to let the SigNoz backend apply its own default.")),
	)
	h.addTool(s, listTool, h.handleListDowntimeSchedules)

	getTool := mcp.NewTool("signoz_get_downtime_schedule",
		withReadOnlyToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithDescription("Use this when the user wants one planned-maintenance (downtime) schedule's full definition by id, including its window, recurrence, muted alert IDs, scope, and server-derived status and kind. It requires a known schedule ID; discover IDs with signoz_list_downtime_schedules."),
		mcp.WithString("id", mcp.Description("Downtime schedule UUIDv7. Required; obtain it from signoz_list_downtime_schedules.")),
	)
	h.addTool(s, getTool, h.handleGetDowntimeSchedule)

	createTool := mcp.NewTool("signoz_create_downtime_schedule",
		withCreateToolAnnotations(),
		mcp.WithDescription("Use this when the user wants to mute SigNoz alert notifications during a maintenance window, by creating a planned-maintenance (downtime) schedule. SigNoz has no Alertmanager silences API, so this is how alerts are silenced. name and schedule are required, and schedule.startTime must precede schedule.endTime; a recurrence needs both repeatType and duration. Omitting alertIds mutes ALL alert rules, so confirm the intended rules first with signoz_list_alert_rules. status and kind are derived by the server and cannot be set."),
		mcp.WithInputSchema[types.CreateDowntimeScheduleInput](),
	)
	h.addTool(s, createTool, h.handleCreateDowntimeSchedule)

	deleteTool := mcp.NewTool("signoz_delete_downtime_schedule",
		withDeleteToolAnnotations(),
		mcp.WithString("searchContext", mcp.Description("Copy the user's entire original request verbatim, including any preflight or confirmation context; do not summarize, shorten, or omit clauses.")),
		mcp.WithString("id", mcp.Description("Downtime schedule UUIDv7. Required; obtain it from signoz_list_downtime_schedules.")),
		mcp.WithDescription("Use this when the user explicitly wants to permanently delete a planned-maintenance (downtime) schedule, which immediately ends the muting it provides. Resolve its ID with signoz_list_downtime_schedules and confirm the exact schedule first; if both steps are already complete, call this tool directly without repeating preflight."),
	)
	h.addTool(s, deleteTool, h.handleDeleteDowntimeSchedule)
}

// serverDerivedDowntimeFields are computed by SigNoz and rejected or ignored on
// write, so they are stripped from any create payload an agent hands us.
var serverDerivedDowntimeFields = []string{
	"id", "status", "kind", "createdAt", "createdBy", "updatedAt", "updatedBy",
}

// decodeDowntimeScheduleList tolerates BOTH upstream shapes for the same route.
// Current SigNoz builds serve downtime schedules through pkg/http/render, which
// wraps payloads as {"status":"success","data":[...]}, but older builds served
// the identical paths from the legacy ee/query-service router with a bare array
// and no envelope. These endpoints are undocumented, so we fail open on either
// shape rather than breaking on the deployment we happen to be pointed at.
//
// Anything else (a wrapped non-array, or an object with no data key) is a real
// contract change: we return an empty list so the tool still answers, and always
// emit a WARN so the drift is detectable — silent degradation is the failure mode
// to design against. A genuinely empty workspace logs nothing, which keeps
// "no schedules" distinguishable from "shape changed".
func (h *Handler) decodeDowntimeScheduleList(ctx context.Context, body []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, err
	}

	switch shaped := parsed.(type) {
	case []any:
		h.logger.WarnContext(ctx, "Downtime schedules response was a bare array, not the expected {status,data} envelope; parsing it as a legacy unwrapped response",
			slog.Int("items", len(shaped)))
		return shaped, nil
	case map[string]any:
		raw, present := shaped["data"]
		if !present {
			h.logger.WarnContext(ctx, "Downtime schedules response has no \"data\" field; treating as empty",
				slog.String("response", logpkg.TruncBody(body)))
			return nil, nil
		}
		switch data := raw.(type) {
		case []any:
			return data, nil
		case nil:
			return nil, nil
		case map[string]any:
			h.logger.WarnContext(ctx, "Downtime schedules response \"data\" was an object, not an array; wrapping it as a single schedule",
				slog.String("response", logpkg.TruncBody(body)))
			return []any{data}, nil
		default:
			h.logger.WarnContext(ctx, "Downtime schedules response \"data\" was neither an array nor an object; treating as empty",
				slog.String("response", logpkg.TruncBody(body)))
			return nil, nil
		}
	default:
		h.logger.WarnContext(ctx, "Downtime schedules response was neither an array nor an object; treating as empty",
			slog.String("response", logpkg.TruncBody(body)))
		return nil, nil
	}
}

func (h *Handler) handleListDowntimeSchedules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_downtime_schedules")
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	limit, offset, limitClamped := paginate.ParseParamsClamped(req.Params.Arguments)

	active, err := parseTriStateBool(args, "active")
	if err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	}
	recurring, err := parseTriStateBool(args, "recurring")
	if err != nil {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Parameter validation failed: %s`, err.Error())), nil
	}

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	body, err := client.ListDowntimeSchedules(ctx, active, recurring)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list downtime schedules", err)
		return upstreamError(err), nil
	}

	schedules, err := h.decodeDowntimeScheduleList(ctx, body)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse downtime schedules response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(body)))
		return upstreamResponseError("failed to parse downtime schedules response: " + err.Error()), nil
	}

	total := len(schedules)
	paged := paginate.Array(schedules, offset, limit)
	resultJSON, err := paginate.Wrap(paged, total, offset, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to wrap downtime schedules with pagination", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}

	return listResult(resultJSON, limitClamped), nil
}

func (h *Handler) handleGetDowntimeSchedule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	id, _ := args["id"].(string)
	if id == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required. Provide the UUIDv7 of the downtime schedule; obtain it from signoz_list_downtime_schedules.`), nil
	}
	if !util.IsUUIDv7(id) {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "id": %q is not a UUIDv7. Obtain the schedule ID from signoz_list_downtime_schedules.`, id)), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_get_downtime_schedule", slog.String("id", id))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}
	body, err := client.GetDowntimeSchedule(ctx, id)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to get downtime schedule", err, slog.String("id", id))
		return upstreamError(err), nil
	}

	return structuredResult(body), nil
}

func (h *Handler) handleCreateDowntimeSchedule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawConfig, ok := req.Params.Arguments.(map[string]any)
	if !ok || len(rawConfig) == 0 {
		h.logger.WarnContext(ctx, "Received empty or invalid arguments map for create downtime schedule.")
		return notAConfigObjectError(), nil
	}

	// searchContext is MCP-observability metadata, never part of the upstream body.
	delete(rawConfig, "searchContext")
	for _, field := range serverDerivedDowntimeFields {
		delete(rawConfig, field)
	}

	if name, _ := rawConfig["name"].(string); name == "" {
		return validationError("name", "is required and must be a non-empty string"), nil
	}
	if _, present := rawConfig["schedule"]; !present {
		return validationError("schedule", "is required and must be an object with startTime and endTime"), nil
	}
	if _, ok := rawConfig["schedule"].(map[string]any); !ok {
		return validationError("schedule", "must be an object with startTime and endTime"), nil
	}

	// scope is an expr-lang boolean expression compiled by the SigNoz backend.
	// We deliberately do not reimplement that validation here: the backend's 400
	// carries the real compiler message, which propagates via upstreamError.

	scheduleJSON, err := json.Marshal(rawConfig)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to marshal downtime schedule payload", logpkg.ErrAttr(err))
		return InternalErrorResult("failed to marshal downtime schedule payload: " + err.Error()), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_create_downtime_schedule")
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	data, err := client.CreateDowntimeSchedule(ctx, scheduleJSON)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to create downtime schedule in SigNoz", err)
		return upstreamError(err), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (h *Handler) handleDeleteDowntimeSchedule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	id, _ := args["id"].(string)
	if id == "" {
		return errorWithCode(CodeValidationFailed, `Parameter validation failed: "id" is required.`), nil
	}
	if !util.IsUUIDv7(id) {
		return errorWithCode(CodeValidationFailed, fmt.Sprintf(`Invalid "id": %q is not a UUIDv7. The SigNoz API will reject this with invalid_input.`, id)), nil
	}

	h.logger.DebugContext(ctx, "Tool called: signoz_delete_downtime_schedule", slog.String("id", id))
	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	if err := client.DeleteDowntimeSchedule(ctx, id); err != nil {
		h.logUpstreamFailure(ctx, "Failed to delete downtime schedule in SigNoz", err, slog.String("id", id))
		return upstreamError(err), nil
	}

	return structuredResult([]byte(fmt.Sprintf(`{"status":"success","id":%q}`, id))), nil
}
