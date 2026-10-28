package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/paginate"
)

const (
	aiPricingViewRules          = "rules"
	aiPricingViewUnpricedModels = "unpriced_models"
	// SigNoz clamps one page of pricing rules to 100.
	aiPricingRulesUpstreamPageSize = 100
)

type aiPricingOutput struct {
	View       string            `json:"view"`
	Data       []json.RawMessage `json:"data"`
	Pagination paginate.Metadata `json:"pagination"`
}

func (h *Handler) RegisterAIPricingHandlers(s *mcp.Server) {
	h.logger.Debug("Registering AI pricing handlers")

	tool := mcp.NewTool("signoz_get_ai_pricing",
		withReadOnlyToolAnnotations(),
		mcp.WithDescription("Use this when the user asks which per-token price SigNoz uses for an AI model's cost, or which models have no price. view=rules (default) lists the pricing rules: provider, model name and match patterns, input, output, and cache prices in USD per million tokens, and whether each rule is enabled. view=unpriced_models lists the models seen in spans during the last hour that no rule prices, with span counts; their spans carry no cost. SigNoz stamps cost on each span at ingest, so a price change does not touch spans already stored. Prices are changed on the Model pricing tab of AI Observability in SigNoz; this tool cannot change them."),
		mcp.WithString("searchContext", mcp.Description(searchContextParamDescription)),
		mcp.WithString("view", mcp.DefaultString(aiPricingViewRules), mcp.Enum(aiPricingViewRules, aiPricingViewUnpricedModels),
			mcp.Description("Which list to return. rules: the configured model prices. unpriced_models: models seen in the last hour that no rule prices. Default: rules.")),
		mcp.WithString("searchText", mcp.Description("Case-insensitive substring of the model name or provider, for example 'anthropic'; 'gpt-4o' also matches gpt-4o-mini. An empty page with searchText means no entry matched; omit it to see whether any entries exist.")),
		mcp.WithString("limit", mcp.DefaultString("50"), intOrStringType(), mcp.Description("Maximum number of entries to return per page. Default: 50, max: 1000 (higher values are clamped).")),
		mcp.WithString("offset", mcp.DefaultString("0"), intOrStringType(), mcp.Description("Number of entries to skip. Use pagination.nextOffset from the previous page. Default: 0.")),
	)
	h.addTool(s, tool, h.handleGetAIPricing)
}

func (h *Handler) handleGetAIPricing(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, errResult := requireArgsMap(req.Params.Arguments)
	if errResult != nil {
		return errResult, nil
	}
	view := aiPricingViewRules
	if raw, ok := args["view"].(string); ok && strings.TrimSpace(raw) != "" {
		view = strings.TrimSpace(raw)
	}
	if view != aiPricingViewRules && view != aiPricingViewUnpricedModels {
		return validationErrorf("view", "must be %q or %q, got %q", aiPricingViewRules, aiPricingViewUnpricedModels, view), nil
	}
	searchText, _ := args["searchText"].(string)
	searchText = strings.TrimSpace(searchText)
	for _, key := range []string{"limit", "offset"} {
		if value, present, ok := looseInt(args[key]); present && (!ok || value < 0) {
			return validationErrorf(key, "must be a non-negative integer, got %v", args[key]), nil
		}
	}
	limit, offset, limitClamped := paginate.ParseParamsClamped(args)

	h.logger.DebugContext(ctx, "Tool called: signoz_get_ai_pricing", slog.String("view", view))

	client, err := h.GetClient(ctx)
	if err != nil {
		return clientError(err), nil
	}

	var out aiPricingOutput
	if view == aiPricingViewRules {
		out, errResult = h.listAIPricingRules(ctx, client, searchText, offset, limit)
	} else {
		out, errResult = h.listUnpricedAIModels(ctx, client, searchText, offset, limit)
	}
	if errResult != nil {
		return errResult, nil
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return InternalErrorResult("failed to marshal response: " + err.Error()), nil
	}
	return listResult(payload, limitClamped), nil
}

// listAIPricingRules assembles the requested window from as many upstream
// pages as it spans, because SigNoz serves at most 100 rules per call.
func (h *Handler) listAIPricingRules(ctx context.Context, client signozclient.Client, searchText string, offset, limit int) (aiPricingOutput, *mcp.CallToolResult) {
	items := make([]json.RawMessage, 0, min(limit, aiPricingRulesUpstreamPageSize))
	total := 0
	for pageOffset := offset; pageOffset < offset+limit; {
		pageLimit := min(aiPricingRulesUpstreamPageSize, offset+limit-pageOffset)
		body, err := client.ListAIPricingRules(ctx, pageOffset, pageLimit, searchText)
		if err != nil {
			h.logUpstreamFailure(ctx, "Failed to list AI pricing rules", err)
			return aiPricingOutput{}, upstreamError(err)
		}
		var resp struct {
			Data *struct {
				Items []json.RawMessage `json:"items"`
				Total *int              `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			h.logger.ErrorContext(ctx, "Failed to parse AI pricing rules response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(body)))
			return aiPricingOutput{}, upstreamResponseError("failed to parse AI pricing rules response: " + err.Error())
		}
		if resp.Data == nil || resp.Data.Total == nil || resp.Data.Items == nil {
			// Without items and a total the page cannot be read or paged, so the
			// result would look complete when it is not.
			h.logger.WarnContext(ctx, "Unexpected response shape from the pricing rules endpoint; upstream contract may have changed",
				slog.String("response", logpkg.TruncBody(body)))
			return aiPricingOutput{}, upstreamResponseError("SigNoz returned AI pricing rules without data.items and data.total; retry, and report it if it persists")
		}
		total = *resp.Data.Total
		items = append(items, resp.Data.Items...)
		pageOffset += len(resp.Data.Items)
		if len(resp.Data.Items) < pageLimit || pageOffset >= total {
			break
		}
	}
	return aiPricingOutput{
		View:       aiPricingViewRules,
		Data:       items,
		Pagination: paginate.NewMetadata(total, offset, limit),
	}, nil
}

// listUnpricedAIModels pages and filters locally: SigNoz returns the whole
// unpriced list in one response and has no search parameter for it.
func (h *Handler) listUnpricedAIModels(ctx context.Context, client signozclient.Client, searchText string, offset, limit int) (aiPricingOutput, *mcp.CallToolResult) {
	body, err := client.ListUnpricedAIModels(ctx)
	if err != nil {
		h.logUpstreamFailure(ctx, "Failed to list unpriced AI models", err)
		return aiPricingOutput{}, upstreamError(err)
	}
	var resp struct {
		Data *struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		h.logger.ErrorContext(ctx, "Failed to parse unpriced AI models response", logpkg.ErrAttr(err), slog.String("response", logpkg.TruncBody(body)))
		return aiPricingOutput{}, upstreamResponseError("failed to parse unpriced AI models response: " + err.Error())
	}
	if resp.Data == nil || resp.Data.Items == nil {
		h.logger.WarnContext(ctx, "Unexpected response shape from the unpriced models endpoint; upstream contract may have changed",
			slog.String("response", logpkg.TruncBody(body)))
		return aiPricingOutput{}, upstreamResponseError("SigNoz returned unpriced AI models without data.items; retry, and report it if it persists")
	}

	matched := make([]json.RawMessage, 0, len(resp.Data.Items))
	needle := strings.ToLower(searchText)
	for _, item := range resp.Data.Items {
		if needle == "" {
			matched = append(matched, item)
			continue
		}
		var model struct {
			ModelName string `json:"modelName"`
			Provider  string `json:"provider"`
		}
		if err := json.Unmarshal(item, &model); err != nil {
			h.logger.WarnContext(ctx, "Unexpected unpriced model entry shape; upstream contract may have changed", logpkg.ErrAttr(err))
			continue
		}
		if strings.Contains(strings.ToLower(model.ModelName), needle) || strings.Contains(strings.ToLower(model.Provider), needle) {
			matched = append(matched, item)
		}
	}

	page := []json.RawMessage{}
	if offset < len(matched) {
		page = matched[offset:min(offset+limit, len(matched))]
	}
	return aiPricingOutput{
		View:       aiPricingViewUnpricedModels,
		Data:       page,
		Pagination: paginate.NewMetadata(len(matched), offset, limit),
	}, nil
}
