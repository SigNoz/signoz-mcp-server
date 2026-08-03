package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// GetOrgOverview fetches the deployment-wide aggregate stats snapshot.
// The handler owns the client-facing grouped envelope; the client keeps the
// upstream payload byte-faithful so compatible backend additions can fail open.
func (s *SigNoz) GetOrgOverview(ctx context.Context) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/stats", s.baseURL)
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching deployment overview")

	body, err := s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("organization overview: %w", err)
	}

	return body, nil
}
