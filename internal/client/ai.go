package client

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
)

// ListAIPricingRules returns one page of the org's model pricing rules.
func (s *SigNoz) ListAIPricingRules(ctx context.Context, offset, limit int, search string) (json.RawMessage, error) {
	params := &apiclient.ListLLMPricingRulesParams{Offset: ptr(offset), Limit: optInt(limit), Q: optString(search)}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing AI pricing rules", slog.Int("offset", offset), slog.Int("limit", limit))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListLLMPricingRules(ctx, params)
	})
}

// ListUnpricedAIModels returns the models seen in recent spans that no pricing
// rule matches.
func (s *SigNoz) ListUnpricedAIModels(ctx context.Context) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing unpriced AI models")
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListUnmappedLLMModels(ctx)
	})
}

// ListAIAttributeMappingGroups returns every span attribute mapping group.
func (s *SigNoz) ListAIAttributeMappingGroups(ctx context.Context) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing AI attribute mapping groups")
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListSpanMapperGroups(ctx, &apiclient.ListSpanMapperGroupsParams{})
	})
}

// ListAIAttributeMappers returns the mappers of one mapping group.
func (s *SigNoz) ListAIAttributeMappers(ctx context.Context, groupID string) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing AI attribute mappers", slog.String("groupId", groupID))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListSpanMappers(ctx, groupID)
	})
}

// TestAIAttributeMappings runs sample spans through mapping groups. SigNoz
// simulates the mapping in memory and saves nothing, so the POST is replay-safe.
func (s *SigNoz) TestAIAttributeMappings(ctx context.Context, body []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Testing AI attribute mappings", slog.Int("request.body.size_bytes", len(body)))
	return s.callAPI(withReplaySafe(ctx, true), DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.TestSpanMappersWithBody(ctx, jsonContentType, bytes.NewReader(body))
	})
}
