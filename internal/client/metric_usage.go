package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// MaxMetricUsageNames is the per-call soft cap on metric names.
	// Each name makes 2 sequential HTTP calls.
	// Callers with more names should batch into groups of this size.
	MaxMetricUsageNames = 50

	// metricUsageTotalTimeout bounds the whole batch.
	metricUsageTotalTimeout = 30 * time.Second
)

type metricDashboardRef struct {
	DashboardName string `json:"dashboardName"`
	DashboardID   string `json:"dashboardId"`
}

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

// CheckMetricUsage returns dashboard and alert references for each metric.
// Lookup failures are stored per metric so one transient failure does not cancel
// the whole batch. Metric-not-found 404s are treated as empty usage; route-level
// 404s are reported as errors.
func (s *SigNoz) CheckMetricUsage(ctx context.Context, names []string) (map[string]MetricUsage, error) {
	ctx = s.ensureTenantContext(ctx)

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

	if len(names) > MaxMetricUsageNames {
		return nil, fmt.Errorf(
			"too many metric names: %d exceeds the per-call limit of %d — split into batches of %d and merge results",
			len(names), MaxMetricUsageNames, MaxMetricUsageNames,
		)
	}

	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > metricUsageTotalTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, metricUsageTotalTimeout)
		defer cancel()
	}

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
				// Preserve partial data; the error only marks unknown portions.
				if usage.Dashboards == nil {
					usage.Dashboards = []string{}
				}
				if usage.Alerts == nil {
					usage.Alerts = []string{}
				}
				usage.Error = err.Error()
				results[i] = result{name: name, usage: usage}
				return nil
			}
			results[i] = result{name: name, usage: usage}
			return nil
		})
	}

	_ = g.Wait()

	out := make(map[string]MetricUsage, len(names))
	for _, r := range results {
		out[r.name] = r.usage
	}
	return out, nil
}

func (s *SigNoz) fetchMetricUsage(ctx context.Context, name string) (MetricUsage, error) {
	params := url.Values{}
	params.Set("metricName", name)
	query := params.Encode()

	usage := MetricUsage{
		Dashboards: []string{},
		Alerts:     []string{},
	}
	var errs []string

	dashURL := fmt.Sprintf("%s/api/v2/metrics/dashboards?%s", s.baseURL, query)
	s.logger.DebugContext(ctx, "Fetching metric dashboard refs", slog.String("metric", name))

	dashBody, err := s.doRequest(ctx, http.MethodGet, dashURL, nil, DefaultQueryTimeout)
	if err != nil {
		if !isMetricNotFound404(err) {
			errs = append(errs, fmt.Sprintf("dashboards lookup for %q: %v", name, err))
		}
	} else {
		// Warn on contract drift while keeping fail-open behavior.
		var dashProbe struct {
			Data *struct {
				Dashboards []json.RawMessage `json:"dashboards"`
			} `json:"data"`
		}
		if perr := json.Unmarshal(dashBody, &dashProbe); perr != nil || dashProbe.Data == nil || dashProbe.Data.Dashboards == nil {
			s.logger.WarnContext(ctx, "Unexpected response shape from metric dashboards endpoint — upstream contract may have changed",
				slog.String("metric", name))
		}
		dashNames, err := parseDashboardNames(dashBody)
		if err != nil {
			errs = append(errs, fmt.Sprintf("parsing dashboard refs for %q: %v", name, err))
		} else {
			usage.Dashboards = dashNames
		}
	}

	alertURL := fmt.Sprintf("%s/api/v2/metrics/alerts?%s", s.baseURL, query)
	s.logger.DebugContext(ctx, "Fetching metric alert refs", slog.String("metric", name))

	alertBody, err := s.doRequest(ctx, http.MethodGet, alertURL, nil, DefaultQueryTimeout)
	if err != nil {
		if !isMetricNotFound404(err) {
			errs = append(errs, fmt.Sprintf("alerts lookup for %q: %v", name, err))
		}
	} else {
		// Warn on contract drift while keeping fail-open behavior.
		var alertProbe struct {
			Data *struct {
				Alerts []json.RawMessage `json:"alerts"`
			} `json:"data"`
		}
		if perr := json.Unmarshal(alertBody, &alertProbe); perr != nil || alertProbe.Data == nil || alertProbe.Data.Alerts == nil {
			s.logger.WarnContext(ctx, "Unexpected response shape from metric alerts endpoint — upstream contract may have changed",
				slog.String("metric", name))
		}
		alertNames, err := parseAlertNames(alertBody)
		if err != nil {
			errs = append(errs, fmt.Sprintf("parsing alert refs for %q: %v", name, err))
		} else {
			usage.Alerts = alertNames
		}
	}

	if len(errs) > 0 {
		return usage, errors.New(strings.Join(errs, "; "))
	}
	return usage, nil
}

// isMetricNotFound404 accepts only the live SigNoz metric-not-found envelope,
// not generic route/proxy 404s.
func isMetricNotFound404(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		return false
	}
	var envelope struct {
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal([]byte(statusErr.Body), &envelope); jsonErr != nil {
		return false
	}
	return envelope.Status == "error" &&
		envelope.Error.Code == "not_found" &&
		envelope.Error.Type == "not-found" &&
		strings.HasPrefix(envelope.Error.Message, "metric not found:")
}

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
