package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/sync/errgroup"
)

// metricDashboardRef is the per-widget reference returned by
// GET /api/v2/metrics/{name}/dashboards.
type metricDashboardRef struct {
	DashboardName string `json:"dashboardName"`
	DashboardID   string `json:"dashboardId"`
}

// metricAlertRef is the per-alert reference returned by
// GET /api/v2/metrics/{name}/alerts.
type metricAlertRef struct {
	AlertName string `json:"alertName"`
	AlertID   string `json:"alertId"`
}

// MetricUsage is the compact usage summary returned to the caller for each metric.
type MetricUsage struct {
	Dashboards []string `json:"dashboards"`
	Alerts     []string `json:"alerts"`
	Error      string   `json:"error,omitempty"`
}

// CheckMetricUsage returns dashboard and alert references for each metric in
// names. It fires up to 10 goroutines concurrently (bounded by errgroup.SetLimit)
// — each goroutine fetches the dashboards endpoint then the alerts endpoint for
// one metric, sequentially. A 404 from either endpoint is treated as an empty
// result, not an error. Dashboard names are deduplicated (one metric can appear
// in multiple widgets of the same dashboard).
//
// Errors are stored per-metric in MetricUsage.Error rather than aborting the
// whole batch — a transient 5xx on one metric must not discard results for the
// rest.
func (s *SigNoz) CheckMetricUsage(ctx context.Context, names []string) (map[string]MetricUsage, error) {
	ctx = s.ensureTenantContext(ctx)

	// Deduplicate and filter empty strings to avoid malformed URLs and redundant API calls.
	seen := make(map[string]struct{})
	var filtered []string
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			filtered = append(filtered, name)
		}
	}
	names = filtered

	type result struct {
		name  string
		usage MetricUsage
	}

	results := make([]result, len(names))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for i, name := range names {
		i, name := i, name
		g.Go(func() error {
			usage, err := s.fetchMetricUsage(gctx, name)
			if err != nil {
				// Store per-metric error instead of cancelling the whole batch.
				results[i] = result{name: name, usage: MetricUsage{
					Dashboards: []string{},
					Alerts:     []string{},
					Error:      err.Error(),
				}}
				return nil
			}
			results[i] = result{name: name, usage: usage}
			return nil
		})
	}

	g.Wait() // always nil — goroutines never return errors

	out := make(map[string]MetricUsage, len(names))
	for _, r := range results {
		out[r.name] = r.usage
	}
	return out, nil
}

// fetchMetricUsage fetches dashboards then alerts for a single metric name.
func (s *SigNoz) fetchMetricUsage(ctx context.Context, name string) (MetricUsage, error) {
	escaped := url.PathEscape(name)

	// --- Dashboards ---
	dashURL := fmt.Sprintf("%s/api/v2/metrics/%s/dashboards", s.baseURL, escaped)
	s.logger.DebugContext(ctx, "Fetching metric dashboard refs", slog.String("metric", name))

	dashBody, err := s.doRequest(ctx, http.MethodGet, dashURL, nil, DefaultQueryTimeout)
	dashNames := []string{}
	if err != nil {
		if !isMetricNotFound404(err) {
			return MetricUsage{}, fmt.Errorf("dashboards lookup for %q: %w", name, err)
		}
		// 404 = metric not tracked → empty dashboards
	} else {
		// Fail-open contract check: warn if the expected shape is absent so silent
		// degradation is detectable in production (see CLAUDE.md §Testing across
		// external contracts).
		var dashProbe struct {
			Data *struct {
				Dashboards []json.RawMessage `json:"dashboards"`
			} `json:"data"`
		}
		if perr := json.Unmarshal(dashBody, &dashProbe); perr != nil || dashProbe.Data == nil || dashProbe.Data.Dashboards == nil {
			s.logger.WarnContext(ctx, "Unexpected response shape from metric dashboards endpoint — upstream contract may have changed",
				slog.String("metric", name))
		}
		dashNames, err = parseDashboardNames(dashBody)
		if err != nil {
			return MetricUsage{}, fmt.Errorf("parsing dashboard refs for %q: %w", name, err)
		}
	}

	// --- Alerts ---
	alertURL := fmt.Sprintf("%s/api/v2/metrics/%s/alerts", s.baseURL, escaped)
	s.logger.DebugContext(ctx, "Fetching metric alert refs", slog.String("metric", name))

	alertBody, err := s.doRequest(ctx, http.MethodGet, alertURL, nil, DefaultQueryTimeout)
	alertNames := []string{}
	if err != nil {
		if !isMetricNotFound404(err) {
			return MetricUsage{}, fmt.Errorf("alerts lookup for %q: %w", name, err)
		}
		// 404 = metric not tracked → empty alerts
	} else {
		// Fail-open contract check.
		var alertProbe struct {
			Data *struct {
				Alerts []json.RawMessage `json:"alerts"`
			} `json:"data"`
		}
		if perr := json.Unmarshal(alertBody, &alertProbe); perr != nil || alertProbe.Data == nil || alertProbe.Data.Alerts == nil {
			s.logger.WarnContext(ctx, "Unexpected response shape from metric alerts endpoint — upstream contract may have changed",
				slog.String("metric", name))
		}
		alertNames, err = parseAlertNames(alertBody)
		if err != nil {
			return MetricUsage{}, fmt.Errorf("parsing alert refs for %q: %w", name, err)
		}
	}

	return MetricUsage{
		Dashboards: dashNames,
		Alerts:     alertNames,
	}, nil
}

// isMetricNotFound404 reports whether err is a metric-level 404 from the
// SigNoz API, as opposed to a route-level 404 from the HTTP router.
//
// doRequest formats non-2xx errors as "unexpected status NNN: <body>".
// A SigNoz API 404 (metric not tracked) has a JSON body starting with '{'.
// A router-level 404 (endpoint not registered — e.g. SigNoz < v0.105.0)
// returns plain text ("404 page not found"), which must NOT be silently
// treated as empty usage, as it would incorrectly mark all metrics safe to drop.
func isMetricNotFound404(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "unexpected status 404") {
		return false
	}
	body := strings.TrimSpace(strings.TrimPrefix(msg, "unexpected status 404: "))
	return strings.HasPrefix(body, "{")
}

// parseDashboardNames extracts and deduplicates dashboard names from the
// /api/v2/metrics/{name}/dashboards response.
// Response shape: {"status":"success","data":{"dashboards":[{dashboardName,dashboardId,widgetId,widgetName},...]}}
func parseDashboardNames(body []byte) ([]string, error) {
	var resp struct {
		Data struct {
			Dashboards []metricDashboardRef `json:"dashboards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var names []string
	for _, ref := range resp.Data.Dashboards {
		if _, ok := seen[ref.DashboardName]; !ok {
			seen[ref.DashboardName] = struct{}{}
			names = append(names, ref.DashboardName)
		}
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// parseAlertNames extracts alert names from the
// /api/v2/metrics/{name}/alerts response.
// Response shape: {"status":"success","data":{"alerts":[{alertName,alertId},...]}
func parseAlertNames(body []byte) ([]string, error) {
	var resp struct {
		Data struct {
			Alerts []metricAlertRef `json:"alerts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Data.Alerts))
	for _, ref := range resp.Data.Alerts {
		names = append(names, ref.AlertName)
	}
	return names, nil
}
