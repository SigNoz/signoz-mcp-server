package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

type aiAttributeMappingsOutput struct {
	Data         []map[string]json.RawMessage `json:"data"`
	Incomplete   bool                         `json:"incomplete"`
	FailedGroups []aiMappingGroupFailure      `json:"failedGroups,omitempty"`
	NextAction   string                       `json:"nextAction,omitempty"`
}

type aiMappingGroupFailure struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message"`
}

// mappingGroupSummary is the part of a saved group the handlers read; the rest
// passes through untouched.
type mappingGroupSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *Handler) RegisterAIAttributeMappingHandlers(s *mcp.Server) {
	h.logger.Debug("Registering AI attribute mapping handlers")

	listTool := mcp.NewTool("signoz_list_ai_attribute_mappings",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription("Use this when the user asks why AI spans show no model, tokens, or cost, or which vendor attributes SigNoz maps to gen_ai.* keys. At ingest, a mapping group runs on spans whose attribute keys contain one of its condition substrings, and each mapper fills one gen_ai.* key from the first source key present, by priority. Mappings apply only to spans ingested afterwards and are changed on the Attribute Mapping tab of AI Observability in SigNoz. For spans with a model but no cost, use signoz_get_ai_pricing. If incomplete is true, failedGroups names the groups whose mappers could not be read."),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
	)
	h.addTool(s, listTool, h.handleListAIAttributeMappings)

}

func (h *Handler) handleListAIAttributeMappings(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.DebugContext(ctx, "Tool called: signoz_list_ai_attribute_mappings")

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	groups, errResult := h.listMappingGroups(ctx, client)
	if errResult != nil {
		return errResult, nil
	}

	out := aiAttributeMappingsOutput{Data: make([]map[string]json.RawMessage, 0, len(groups))}
	var firstErr error
	for _, raw := range groups {
		group, summary, ok := h.decodeMappingGroup(ctx, raw)
		if !ok {
			out.FailedGroups = append(out.FailedGroups, aiMappingGroupFailure{Code: CodeUpstreamError, Message: "SigNoz returned a mapping group without an id"})
			continue
		}
		body, err := client.ListAIAttributeMappers(ctx, summary.ID)
		if err != nil {
			// Auth, permission, and cancellation are global: one group failing
			// that way means every group would, so the whole call fails.
			if isGlobalUpstreamFailure(ctx, err) {
				h.logUpstreamFailure(ctx, "Failed to list AI attribute mappers", err, slog.String("groupId", summary.ID))
				return upstreamError(err), nil
			}
			h.logger.WarnContext(ctx, "Failed to list mappers for one AI attribute mapping group", slog.String("groupId", summary.ID), logpkg.ErrAttr(err))
			if firstErr == nil {
				firstErr = err
			}
			out.FailedGroups = append(out.FailedGroups, groupFailure(summary, err))
			group["mappers"] = json.RawMessage("null")
			out.Data = append(out.Data, group)
			continue
		}
		mappers, ok := h.decodeMappers(ctx, summary.ID, body)
		if !ok {
			out.FailedGroups = append(out.FailedGroups, aiMappingGroupFailure{GroupID: summary.ID, Name: summary.Name, Code: CodeUpstreamError, Message: "SigNoz returned this group's mappers in an unreadable shape"})
			mappers = json.RawMessage("null")
		}
		group["mappers"] = mappers
		out.Data = append(out.Data, group)
	}

	if len(groups) > 0 && len(out.FailedGroups) == len(groups) && firstErr != nil {
		return upstreamError(firstErr), nil
	}
	if len(out.FailedGroups) > 0 {
		out.Incomplete = true
		out.NextAction = "Retry signoz_list_ai_attribute_mappings; groups in failedGroups have mappers set to null in this result."
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	return structuredResult(payload), nil
}

// listMappingGroups returns the raw saved mapping groups, or a coded error.
func (h *Handler) listMappingGroups(ctx context.Context, client signozclient.Client) ([]json.RawMessage, *mcp.CallToolResult) {
	body, err := client.ListAIAttributeMappingGroups(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list AI attribute mapping groups", err)
		return nil, upstreamError(err)
	}
	var resp struct {
		Data *struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse AI attribute mapping groups response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(body)))
		return nil, upstreamResponseError("failed to parse AI attribute mapping groups response: " + err.Error())
	}
	if resp.Data == nil || resp.Data.Items == nil {
		h.logger.WarnContext(ctx, "Unexpected response shape from the span mapper groups endpoint; upstream contract may have changed",
			slog.String("response", logpkg.TruncBody(body)))
		return nil, upstreamResponseError("SigNoz returned AI attribute mapping groups without data.items; retry, and report it if it persists")
	}
	return resp.Data.Items, nil
}

func (h *Handler) decodeMappingGroup(ctx context.Context, raw json.RawMessage) (map[string]json.RawMessage, mappingGroupSummary, bool) {
	var group map[string]json.RawMessage
	var summary mappingGroupSummary
	if err := json.Unmarshal(raw, &group); err != nil {
		h.logger.WarnContext(ctx, "Unexpected span mapper group shape; upstream contract may have changed", logpkg.ErrAttr(err))
		return nil, summary, false
	}
	if err := json.Unmarshal(raw, &summary); err != nil || summary.ID == "" {
		h.logger.WarnContext(ctx, "Span mapper group without a usable id; upstream contract may have changed", slog.String("group", logpkg.TruncBody(raw)))
		return nil, summary, false
	}
	return group, summary, true
}

// decodeMappers returns a group's mapper list, or false when SigNoz's response
// cannot be read.
func (h *Handler) decodeMappers(ctx context.Context, groupID string, body json.RawMessage) (json.RawMessage, bool) {
	var resp struct {
		Data *struct {
			Items json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Data == nil || len(resp.Data.Items) == 0 {
		h.logger.WarnContext(ctx, "Unexpected response shape from the span mappers endpoint; upstream contract may have changed",
			slog.String("groupId", groupID), slog.String("response", logpkg.TruncBody(body)))
		return nil, false
	}
	return resp.Data.Items, true
}

// isGlobalUpstreamFailure reports failures that would hit every group alike.
func isGlobalUpstreamFailure(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var statusErr *signozclient.HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden
	}
	return false
}

func groupFailure(summary mappingGroupSummary, err error) aiMappingGroupFailure {
	failure := aiMappingGroupFailure{GroupID: summary.ID, Name: summary.Name, Code: CodeUpstreamError, Message: boundedErrorDetail(err.Error())}
	var statusErr *signozclient.HTTPStatusError
	if errors.As(err, &statusErr) {
		failure.Status = statusErr.StatusCode
		failure.Code = upstreamCodeForStatus(statusErr.StatusCode, signozclient.ParseUpstreamErrorBody(statusErr.Body).Type)
	}
	return failure
}
