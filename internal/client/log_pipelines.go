package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// logPipelinesLatestPath returns the log pipelines in the latest agent
// configuration version. SigNoz documents this endpoint in its HTTP handlers,
// not in the pinned OpenAPI spec, so the generated client has no method for it.
const logPipelinesLatestPath = "/api/v1/logs/pipelines/latest"

// ListLogPipelines returns the current log pipeline configuration. There is no
// single-pipeline read endpoint, so callers select one from this response.
func (s *SigNoz) ListLogPipelines(ctx context.Context) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching log pipelines")
	return s.doRequest(ctx, http.MethodGet, s.baseURL+logPipelinesLatestPath, nil, DefaultQueryTimeout)
}
