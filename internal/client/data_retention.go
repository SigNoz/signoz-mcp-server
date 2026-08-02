package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/sync/errgroup"
)

const (
	retentionSignalMetrics = "metrics"
	retentionSignalTraces  = "traces"
	retentionSignalLogs    = "logs"

	retentionStatusIdle    = "idle"
	retentionStatusPending = "pending"
	retentionStatusFailed  = "failed"
	retentionStatusSuccess = "success"
)

// DataRetention is the normalized workspace retention configuration returned
// by signoz_get_data_retention.
type DataRetention struct {
	Metrics RetentionPolicy `json:"metrics" jsonschema:"Metrics retention configuration."`
	Traces  RetentionPolicy `json:"traces" jsonschema:"Traces retention configuration."`
	Logs    RetentionPolicy `json:"logs" jsonschema:"Logs retention configuration, including custom overrides when configured."`
	WebURL  string          `json:"webUrl,omitempty" jsonschema:"Absolute SigNoz Settings URL where retention can be reviewed. Use this URL verbatim."`
}

// RetentionPolicy reports the current retention configuration and the latest
// pending or failed target when one exists.
// CurrentStateKnown is false when the upstream API exposes a pending or failed
// custom-log attempt but not the previously active policy.
type RetentionPolicy struct {
	CurrentStateKnown                bool                  `json:"currentStateKnown" jsonschema:"Whether the currently configured retention state is known. If false, report the current value as unknown and use changeStatus plus webUrl for follow-up; do not treat a pending or failed attempt as active."`
	CurrentRetentionHours            *int64                `json:"currentRetentionHours,omitempty" jsonschema:"Currently configured default deletion-retention period in hours for newly ingested data. Older data can retain an earlier TTL. Omitted when currentStateKnown is false."`
	CurrentColdStorageMoveAfterHours *int64                `json:"currentColdStorageMoveAfterHours,omitempty" jsonschema:"Currently configured time after ingestion before data moves to cold storage, in hours. When currentStateKnown is true, omission means cold-storage movement is disabled; when false, omission means the value is unknown."`
	TargetRetentionHours             *int64                `json:"targetRetentionHours,omitempty" jsonschema:"Requested default deletion-retention period in hours from the latest pending or failed change. This is an attempted target, not the active value. Omitted when there is no pending or failed target."`
	TargetColdStorageMoveAfterHours  *int64                `json:"targetColdStorageMoveAfterHours,omitempty" jsonschema:"Requested time before data moves to cold storage, in hours, from the latest pending or failed change. This is an attempted target, not the active value. When targetRetentionHours is present, omission means cold-storage movement is disabled for that target."`
	ChangeStatus                     string                `json:"changeStatus" jsonschema:"Latest retention-change status: idle, pending, failed, or success."`
	CustomRules                      []CustomRetentionRule `json:"customRules,omitempty" jsonschema:"Active custom deletion-retention overrides for newly ingested logs. Rules are evaluated in order; the first matching rule wins. Omitted for non-log signals, when no active overrides exist, or when currentStateKnown is false; in the last case active overrides are unknown."`
	TargetCustomRules                []CustomRetentionRule `json:"targetCustomRules,omitempty" jsonschema:"Requested ordered custom log-retention overrides from the latest pending or failed change. These are attempted targets, not active rules. Omitted when there is no target or the target has no custom overrides."`
}

type CustomRetentionRule struct {
	Conditions     []RetentionCondition `json:"conditions" jsonschema:"All conditions that must match for this retention override."`
	RetentionHours int64                `json:"retentionHours" jsonschema:"Retention period applied by this rule, in hours."`
}

type RetentionCondition struct {
	Key    string   `json:"key" jsonschema:"Log field key matched by the condition."`
	Values []string `json:"values" jsonschema:"Field values matched by the condition."`
}

type legacyRetentionResponse struct {
	MetricsRetentionHours               *int64  `json:"metrics_ttl_duration_hrs"`
	MetricsColdStorageMoveHours         *int64  `json:"metrics_move_ttl_duration_hrs"`
	ExpectedMetricsRetentionHours       *int64  `json:"expected_metrics_ttl_duration_hrs"`
	ExpectedMetricsColdStorageMoveHours *int64  `json:"expected_metrics_move_ttl_duration_hrs"`
	TracesRetentionHours                *int64  `json:"traces_ttl_duration_hrs"`
	TracesColdStorageMoveHours          *int64  `json:"traces_move_ttl_duration_hrs"`
	ExpectedTracesRetentionHours        *int64  `json:"expected_traces_ttl_duration_hrs"`
	ExpectedTracesColdStorageMoveHours  *int64  `json:"expected_traces_move_ttl_duration_hrs"`
	LogsRetentionHours                  *int64  `json:"logs_ttl_duration_hrs"`
	LogsColdStorageMoveHours            *int64  `json:"logs_move_ttl_duration_hrs"`
	ExpectedLogsRetentionHours          *int64  `json:"expected_logs_ttl_duration_hrs"`
	ExpectedLogsColdStorageMoveHours    *int64  `json:"expected_logs_move_ttl_duration_hrs"`
	Status                              *string `json:"status"`
}

type logsRetentionResponse struct {
	Version                  *string                 `json:"version"`
	Status                   *string                 `json:"status"`
	DefaultRetentionDays     *int64                  `json:"default_ttl_days"`
	ColdStorageMoveAfterDays *int64                  `json:"cold_storage_ttl_days"`
	CustomRules              []upstreamRetentionRule `json:"ttl_conditions"`
}

type upstreamRetentionRule struct {
	Conditions []RetentionCondition `json:"conditions"`
	TTLDays    *int64               `json:"ttlDays"`
}

// GetDataRetention fetches all three signal policies in one call. Metrics and
// traces use the signal-specific v1 API. Logs first use the custom-retention-aware
// v2 API, then re-read v1 when the workspace is in legacy mode so hour values
// remain exact. The reads are all-or-nothing so a global upstream failure cannot
// be mistaken for a partial configuration.
func (s *SigNoz) GetDataRetention(ctx context.Context) (*DataRetention, error) {
	ctx = s.ensureTenantContext(ctx)

	var result DataRetention
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		policy, err := s.fetchLegacyRetention(gctx, retentionSignalMetrics)
		if err != nil {
			return fmt.Errorf("metrics retention: %w", err)
		}
		result.Metrics = policy
		return nil
	})
	g.Go(func() error {
		policy, err := s.fetchLegacyRetention(gctx, retentionSignalTraces)
		if err != nil {
			return fmt.Errorf("traces retention: %w", err)
		}
		result.Traces = policy
		return nil
	})
	g.Go(func() error {
		policy, err := s.fetchLogsRetention(gctx)
		if err != nil {
			return fmt.Errorf("logs retention: %w", err)
		}
		result.Logs = policy
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *SigNoz) fetchLegacyRetention(ctx context.Context, signal string) (RetentionPolicy, error) {
	params := url.Values{}
	params.Set("type", signal)
	reqURL := fmt.Sprintf("%s/api/v1/settings/ttl?%s", s.baseURL, params.Encode())

	s.logger.DebugContext(ctx, "Fetching data retention", slog.String("signal", signal))
	body, err := s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
	if err != nil {
		return RetentionPolicy{}, err
	}

	var response legacyRetentionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, "response is not valid JSON", err)
	}

	return s.normalizeLegacyRetention(ctx, signal, response)
}

func (s *SigNoz) fetchLogsRetention(ctx context.Context) (RetentionPolicy, error) {
	reqURL := fmt.Sprintf("%s/api/v2/settings/ttl", s.baseURL)
	s.logger.DebugContext(ctx, "Fetching data retention", slog.String("signal", retentionSignalLogs))
	body, err := s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
	if err != nil {
		if isRetentionRouteNotFound404(err) {
			s.logger.DebugContext(ctx, "Custom log retention endpoint unavailable; using legacy retention endpoint")
			return s.fetchLegacyRetention(ctx, retentionSignalLogs)
		}
		return RetentionPolicy{}, err
	}

	var response logsRetentionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, "response is not valid JSON", err)
	}
	if response.Version == nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, "version is missing", nil)
	}
	if *response.Version == "v1" {
		// The v2 compatibility response converts legacy hours to whole days,
		// truncating policies that are not exact day multiples. Re-read v1 so
		// the client-visible hours remain exact.
		s.logger.DebugContext(ctx, "Custom log retention is unavailable; reading exact legacy retention hours")
		return s.fetchLegacyRetention(ctx, retentionSignalLogs)
	}
	return s.normalizeLogsRetention(ctx, response)
}

func isRetentionRouteNotFound404(err error) bool {
	var statusErr *HTTPStatusError
	return errors.As(err, &statusErr) &&
		statusErr.StatusCode == http.StatusNotFound &&
		strings.TrimSpace(statusErr.Body) == "404 page not found"
}

func (s *SigNoz) normalizeLegacyRetention(ctx context.Context, signal string, response legacyRetentionResponse) (RetentionPolicy, error) {
	status, err := normalizeRetentionStatus(response.Status)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, err.Error(), nil)
	}

	current, currentColdStorageMove, target, targetColdStorageMove, err := legacyRetentionFields(signal, response)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, err.Error(), nil)
	}
	if current == nil || *current <= 0 {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, "current retention hours are missing or non-positive", nil)
	}
	currentColdStorageMove, err = normalizeColdStorageMoveHours(currentColdStorageMove)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, "current cold-storage move hours are invalid", err)
	}

	policy := RetentionPolicy{
		CurrentStateKnown:                true,
		CurrentRetentionHours:            current,
		CurrentColdStorageMoveAfterHours: currentColdStorageMove,
		ChangeStatus:                     status,
	}
	if status != retentionStatusPending && status != retentionStatusFailed {
		return policy, nil
	}
	if target == nil || *target <= 0 {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, "target retention hours are missing or non-positive", nil)
	}
	targetColdStorageMove, err = normalizeColdStorageMoveHours(targetColdStorageMove)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, signal, "target cold-storage move hours are invalid", err)
	}
	policy.TargetRetentionHours = target
	policy.TargetColdStorageMoveAfterHours = targetColdStorageMove
	return policy, nil
}

func (s *SigNoz) normalizeLogsRetention(ctx context.Context, response logsRetentionResponse) (RetentionPolicy, error) {
	if response.Version == nil || *response.Version != "v2" {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, "version is missing or unsupported", nil)
	}
	status, err := normalizeRetentionStatus(response.Status)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, err.Error(), nil)
	}
	defaultHours, err := retentionDaysToHours(response.DefaultRetentionDays)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, "default_ttl_days is missing or invalid", err)
	}
	coldStorageMoveHours, err := normalizeColdStorageMoveDays(response.ColdStorageMoveAfterDays)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, "cold_storage_ttl_days is invalid", err)
	}
	rules, err := normalizeCustomRetentionRules(response.CustomRules)
	if err != nil {
		return RetentionPolicy{}, s.retentionContractError(ctx, retentionSignalLogs, "custom retention rules are invalid", err)
	}

	if status == retentionStatusPending || status == retentionStatusFailed {
		// The v2 API exposes only the latest attempted custom-log configuration,
		// not the previously active policy. Preserve it as a target without
		// presenting the attempt as current.
		return RetentionPolicy{
			CurrentStateKnown:               false,
			TargetRetentionHours:            defaultHours,
			TargetColdStorageMoveAfterHours: coldStorageMoveHours,
			ChangeStatus:                    status,
			TargetCustomRules:               rules,
		}, nil
	}

	return RetentionPolicy{
		CurrentStateKnown:                true,
		CurrentRetentionHours:            defaultHours,
		CurrentColdStorageMoveAfterHours: coldStorageMoveHours,
		ChangeStatus:                     status,
		CustomRules:                      rules,
	}, nil
}

func legacyRetentionFields(signal string, response legacyRetentionResponse) (*int64, *int64, *int64, *int64, error) {
	switch signal {
	case retentionSignalMetrics:
		return response.MetricsRetentionHours, response.MetricsColdStorageMoveHours,
			response.ExpectedMetricsRetentionHours, response.ExpectedMetricsColdStorageMoveHours, nil
	case retentionSignalTraces:
		return response.TracesRetentionHours, response.TracesColdStorageMoveHours,
			response.ExpectedTracesRetentionHours, response.ExpectedTracesColdStorageMoveHours, nil
	case retentionSignalLogs:
		return response.LogsRetentionHours, response.LogsColdStorageMoveHours,
			response.ExpectedLogsRetentionHours, response.ExpectedLogsColdStorageMoveHours, nil
	default:
		return nil, nil, nil, nil, fmt.Errorf("unsupported signal %q", signal)
	}
}

func normalizeRetentionStatus(raw *string) (string, error) {
	if raw == nil {
		return "", errors.New("status is missing")
	}
	switch *raw {
	case "":
		return retentionStatusIdle, nil
	case retentionStatusPending, retentionStatusFailed, retentionStatusSuccess:
		return *raw, nil
	default:
		return "", fmt.Errorf("status %q is unsupported", *raw)
	}
}

func retentionDaysToHours(days *int64) (*int64, error) {
	if days == nil || *days <= 0 {
		return nil, errors.New("retention days must be positive")
	}
	if *days > math.MaxInt64/24 {
		return nil, errors.New("retention days overflow hours")
	}
	hours := *days * 24
	return &hours, nil
}

func normalizeColdStorageMoveHours(hours *int64) (*int64, error) {
	if hours == nil || *hours == 0 || *hours == -1 {
		return nil, nil
	}
	if *hours < -1 {
		return nil, errors.New("cold-storage move hours must be positive or disabled")
	}
	return hours, nil
}

func normalizeColdStorageMoveDays(days *int64) (*int64, error) {
	if days == nil || *days == 0 || *days == -1 {
		return nil, nil
	}
	if *days < -1 {
		return nil, errors.New("cold-storage move days must be positive or disabled")
	}
	return retentionDaysToHours(days)
}

func normalizeCustomRetentionRules(upstream []upstreamRetentionRule) ([]CustomRetentionRule, error) {
	if len(upstream) == 0 {
		return nil, nil
	}
	rules := make([]CustomRetentionRule, 0, len(upstream))
	for i, rule := range upstream {
		hours, err := retentionDaysToHours(rule.TTLDays)
		if err != nil {
			return nil, fmt.Errorf("rule %d ttlDays: %w", i, err)
		}
		if len(rule.Conditions) == 0 {
			return nil, fmt.Errorf("rule %d has no conditions", i)
		}
		for j, condition := range rule.Conditions {
			if condition.Key == "" || len(condition.Values) == 0 {
				return nil, fmt.Errorf("rule %d condition %d is missing key or values", i, j)
			}
		}
		rules = append(rules, CustomRetentionRule{
			Conditions:     rule.Conditions,
			RetentionHours: *hours,
		})
	}
	return rules, nil
}

func (s *SigNoz) retentionContractError(ctx context.Context, signal, reason string, cause error) error {
	s.logger.WarnContext(ctx, "Unexpected response shape from data retention endpoint — upstream contract may have changed",
		slog.String("signal", signal), slog.String("reason", reason))
	if cause != nil {
		return fmt.Errorf("unexpected %s retention response: %s: %w", signal, reason, cause)
	}
	return fmt.Errorf("unexpected %s retention response: %s", signal, reason)
}
