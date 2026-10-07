package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
)

// GetOrgOverview fetches the deployment-wide aggregate stats snapshot.
// The handler owns the client-facing grouped envelope; the client keeps the
// upstream payload byte-faithful so compatible backend additions can fail open.
func (s *SigNoz) GetOrgOverview(ctx context.Context) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching deployment overview")

	body, err := s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetStats(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("organization overview: %w", err)
	}

	return body, nil
}
