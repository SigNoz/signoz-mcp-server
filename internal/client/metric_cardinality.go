package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
)

// GetMetricCardinality fetches label/attribute keys for a single metric with
// their cardinality counts and sample values from
// GET /api/v2/metrics/attributes?metricName=...&start=...&end=...
//
// metricName is sent as a query parameter, not a path segment: SigNoz binds it
// from the query string (required) and metric names may legitimately contain
// slashes (e.g. run.googleapis.com/request_latencies), which url.Values encodes
// safely. The server returns attributes sorted highest-cardinality first. The
// raw response is returned as-is — classification (UNBOUNDED, ACCUMULATING,
// etc.) is left to the caller.
func (s *SigNoz) GetMetricCardinality(ctx context.Context, name string, start, end int64) (json.RawMessage, error) {
	params := &apiclient.GetMetricAttributesParams{MetricName: name, Start: ptr(int(start)), End: ptr(int(end))}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching metric cardinality", slog.String("metric", name))

	body, err := s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetMetricAttributes(ctx, params)
	})
	if err != nil {
		return nil, fmt.Errorf("cardinality lookup for %q: %w", name, err)
	}

	// Fail-open contract check: warn if the expected shape is absent so silent
	// degradation is detectable in production (see CONTRIBUTING.md §Testing across
	// external contracts).
	var probe struct {
		Data *struct {
			Attributes []json.RawMessage `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Data == nil || probe.Data.Attributes == nil {
		s.logger.WarnContext(ctx, "Unexpected response shape from metric attributes endpoint — upstream contract may have changed",
			slog.String("metric", name))
	}

	return json.RawMessage(body), nil
}
