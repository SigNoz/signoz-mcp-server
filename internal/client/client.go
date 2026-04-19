package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

const (
	SignozApiKey = "SIGNOZ-API-KEY"
	ContentType  = "Content-Type"

	// DefaultQueryTimeout is used for read-only API calls.
	DefaultQueryTimeout = 600 * time.Second
	// DashboardWriteTimeout is used for dashboard create/update operations.
	DashboardWriteTimeout = 30 * time.Second

	// analyticsIdentityCacheTTL keeps /me out of the hot analytics path;
	// identity rarely changes, so 10 min is long enough to absorb bursts.
	analyticsIdentityCacheTTL = 10 * time.Minute
)

var ErrUnauthorized = errors.New("signoz credentials rejected")

// AnalyticsIdentity is the identity tuple used for analytics attribution.
// UserID holds the service-account ID for API-key sessions, or the SigNoz
// user ID for auth-token sessions. Name is the service-account name or the
// user's displayName, respectively.
type AnalyticsIdentity struct {
	OrgID     string
	UserID    string
	Name      string
	Email     string
	Principal string
}

type SigNoz struct {
	baseURL        string
	apiKey         string
	authHeaderName string
	logger         *zap.Logger
	httpClient     *http.Client
	customHeaders  map[string]string

	identityMu       sync.Mutex
	cachedIdentity   *AnalyticsIdentity
	identityCachedAt time.Time
}

func NewClient(log *zap.Logger, baseURL, apiKey, authHeaderName string, customHeaders map[string]string) *SigNoz {
	return &SigNoz{
		logger:         log,
		baseURL:        baseURL,
		apiKey:         apiKey,
		authHeaderName: authHeaderName,
		customHeaders:  customHeaders,
		httpClient: &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// requestLogger returns a logger enriched with session_id and search_context
// from the request context, so every client-level log line can be correlated
// back to the MCP session and user query.
func (s *SigNoz) requestLogger(ctx context.Context) *zap.Logger {
	l := s.logger
	if s.baseURL != "" {
		l = l.With(zap.String("mcp.tenant_url", s.baseURL))
	}
	if sid, ok := util.GetSessionID(ctx); ok && sid != "" {
		l = l.With(zap.String("mcp.session.id", sid))
	}
	if sc, ok := util.GetSearchContext(ctx); ok && sc != "" {
		l = l.With(zap.String("mcp.search_context", sc))
	}
	// Add trace context for log-trace correlation.
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		l = l.With(
			zap.String("trace_id", spanCtx.TraceID().String()),
			zap.String("span_id", spanCtx.SpanID().String()),
		)
	}
	return l
}

// ValidateCredentials performs a lightweight authenticated request against the
// SigNoz API so the OAuth flow can reject bad API keys or instance URLs before
// redirecting back to the MCP client.
//
// It first tries the user endpoint (/api/v1/user/me). A 404 response indicates
// the API key belongs to a service account (newer SigNoz releases), so it
// retries against /api/v1/service_accounts/me. Any other response from user/me
// is returned directly.
func (s *SigNoz) ValidateCredentials(ctx context.Context) error {
	log := s.requestLogger(ctx)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	userURL := fmt.Sprintf("%s/api/v1/user/me", s.baseURL)
	status, body, err := s.doValidationRequest(ctx, userURL)
	if err != nil {
		log.Error("SigNoz credential validation request failed", zap.String("url", userURL), zap.Error(err))
		return fmt.Errorf("failed to reach SigNoz API: %w", err)
	}

	// 404 means the key is a service-account key; validate via service account endpoint.
	if status == http.StatusNotFound {
		log.Debug("user/me returned non-user status, retrying with service_accounts/me", zap.Int("status", status))
		saURL := fmt.Sprintf("%s/api/v1/service_accounts/me", s.baseURL)
		status, body, err = s.doValidationRequest(ctx, saURL)
		if err != nil {
			log.Error("SigNoz credential validation request failed", zap.String("url", saURL), zap.Error(err))
			return fmt.Errorf("failed to reach SigNoz API: %w", err)
		}
	}

	return s.evaluateValidationResponse(log, status, body)
}

// GetAnalyticsIdentity returns the org + user identity for the current
// credentials, cached per-client and mutex-serialized so a burst of events
// produces a single /me roundtrip.
//
// Auth-token clients hit /api/v2/users/me (v2 is required — it returns
// orgId, v1 doesn't). API-key clients hit /api/v1/service_accounts/me.
func (s *SigNoz) GetAnalyticsIdentity(ctx context.Context) (*AnalyticsIdentity, error) {
	s.identityMu.Lock()
	defer s.identityMu.Unlock()

	if s.cachedIdentity != nil && time.Since(s.identityCachedAt) < analyticsIdentityCacheTTL {
		return s.cachedIdentity, nil
	}

	identity, err := s.fetchAnalyticsIdentity(ctx)
	if err != nil {
		return nil, err
	}

	s.cachedIdentity = identity
	s.identityCachedAt = time.Now()
	return identity, nil
}

func (s *SigNoz) fetchAnalyticsIdentity(ctx context.Context) (*AnalyticsIdentity, error) {
	log := s.requestLogger(ctx)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	endpoint := "/api/v1/service_accounts/me"
	principal := "service_account"
	if strings.EqualFold(s.authHeaderName, "Authorization") {
		endpoint = "/api/v2/users/me"
		principal = "user"
	}

	reqURL := fmt.Sprintf("%s%s", s.baseURL, endpoint)
	status, body, err := s.doValidationRequest(ctx, reqURL)
	if err != nil {
		log.Error("SigNoz analytics identity request failed", zap.String("url", reqURL), zap.Error(err))
		return nil, fmt.Errorf("failed to reach SigNoz API: %w", err)
	}
	if status != http.StatusOK {
		return nil, s.evaluateValidationResponse(log, status, body)
	}

	identity, err := parseAnalyticsIdentity(body, principal)
	if err != nil {
		return nil, fmt.Errorf("parse %s response: %w", endpoint, err)
	}

	return identity, nil
}

type analyticsIdentityResponse struct {
	Data struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		OrgID       string `json:"orgId"`
		Name        string `json:"name"`        // service_accounts/me
		DisplayName string `json:"displayName"` // users/me
	} `json:"data"`
}

func parseAnalyticsIdentity(body []byte, principal string) (*AnalyticsIdentity, error) {
	var resp analyticsIdentityResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Data.ID == "" {
		return nil, fmt.Errorf("missing data.id")
	}
	if resp.Data.OrgID == "" {
		return nil, fmt.Errorf("missing data.orgId")
	}

	name := resp.Data.Name
	if name == "" {
		name = resp.Data.DisplayName
	}

	return &AnalyticsIdentity{
		OrgID:     resp.Data.OrgID,
		UserID:    resp.Data.ID,
		Name:      name,
		Email:     resp.Data.Email,
		Principal: principal,
	}, nil
}

// doValidationRequest sends a GET request to the given URL with auth headers
// and returns the HTTP status code, response body, and any transport error.
func (s *SigNoz) doValidationRequest(ctx context.Context, reqURL string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create validation request: %w", err)
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(s.authHeaderName, s.apiKey)

	for k, v := range s.customHeaders {
		if !strings.EqualFold(k, ContentType) && !strings.EqualFold(k, s.authHeaderName) {
			req.Header.Set(k, v)
		}
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 64 KiB holds the full /api/v2/users/me payload (roles, groups, nested
	// org metadata); anything smaller risks truncating valid JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read validation response: %w", err)
	}

	return resp.StatusCode, body, nil
}

// evaluateValidationResponse maps the final HTTP status to a Go error.
func (s *SigNoz) evaluateValidationResponse(log *zap.Logger, status int, body []byte) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		log.Warn("SigNoz credential validation failed", zap.Int("status", status))
		return fmt.Errorf("%w: status %d", ErrUnauthorized, status)
	default:
		log.Warn("SigNoz credential validation returned unexpected status", zap.Int("status", status), zap.String("response", string(body)))
		return fmt.Errorf("unexpected status %d: %s", status, string(body))
	}
}

const (
	maxRetries    = 3
	retryBaseWait = 100 * time.Millisecond
	retryMultiply = 4
)

// doRequest performs an HTTP request with standard headers, timeout, status
// checking, body reading, and retry with exponential backoff for transient
// failures (429, 502, 503, 504, network errors).
func (s *SigNoz) doRequest(ctx context.Context, method, reqURL string, body io.Reader, timeout time.Duration) (json.RawMessage, error) {
	log := s.requestLogger(ctx)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Buffer the body so we can retry POST/PUT requests.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}

	var lastErr error
	wait := retryBaseWait

	for attempt := range maxRetries {
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set(ContentType, "application/json")

		req.Header.Set(s.authHeaderName, s.apiKey)

		for k, v := range s.customHeaders {
			if strings.EqualFold(k, ContentType) || strings.EqualFold(k, s.authHeaderName) {
				log.Warn("Custom header overrides a reserved header",
					zap.String("header", k), zap.String("value", v))
				continue
			}
			req.Header.Set(k, v)
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			// Don't retry on context cancellation.
			if ctx.Err() != nil {
				return nil, fmt.Errorf("request cancelled: %w", err)
			}
			lastErr = fmt.Errorf("failed to do request: %w", err)
			log.Warn("Request failed, will retry",
				zap.String("url", reqURL), zap.Int("attempt", attempt+1), zap.Error(err))
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("retry aborted: %w", lastErr)
			case <-time.After(wait):
			}
			wait *= retryMultiply
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if readErr != nil {
			return nil, fmt.Errorf("failed to read response body: %w", readErr)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBody, nil
		}

		// Retry on transient server errors.
		if isRetryableStatus(resp.StatusCode) && attempt < maxRetries-1 {
			lastErr = fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
			log.Warn("Retryable status, will retry",
				zap.String("url", reqURL), zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("retry aborted: %w", lastErr)
			case <-time.After(wait):
			}
			wait *= retryMultiply
			continue
		}

		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil, lastErr
}

func isRetryableStatus(code int) bool {
	return code == 429 || code == 502 || code == 503 || code == 504
}

func (s *SigNoz) ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
	params := url.Values{}
	if start > 0 {
		params.Set("start", fmt.Sprintf("%d", start))
	}
	if end > 0 {
		params.Set("end", fmt.Sprintf("%d", end))
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if searchText != "" {
		params.Set("searchText", searchText)
	}
	if source != "" {
		params.Set("source", source)
	}

	reqURL := fmt.Sprintf("%s/api/v2/metrics?%s", s.baseURL, params.Encode())
	s.requestLogger(ctx).Debug("Listing metrics", zap.String("searchText", searchText))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) ListMetricKeys(ctx context.Context) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/metrics/filters/keys", s.baseURL)
	s.requestLogger(ctx).Debug("Making request to SigNoz API", zap.String("method", "GET"), zap.String("endpoint", "/api/v1/metrics/filters/keys"))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) ListAlerts(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/alerts", s.baseURL)
	if qp := params.QueryParams(); len(qp) > 0 {
		reqURL += "?" + qp.Encode()
	}
	s.requestLogger(ctx).Debug("Fetching alerts from SigNoz", zap.String("url", reqURL))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) GetAlertByRuleID(ctx context.Context, ruleID string) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/rules/%s", s.baseURL, url.PathEscape(ruleID))
	s.requestLogger(ctx).Debug("Fetching alert rule details", zap.String("ruleID", ruleID))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

// ListDashboards filters data as it returns too much data even the ui tags
// so we filter and only return required information which might help to get
// detailed info of a dashboard.
func (s *SigNoz) ListDashboards(ctx context.Context) (json.RawMessage, error) {
	log := s.requestLogger(ctx)
	reqURL := fmt.Sprintf("%s/api/v1/dashboards", s.baseURL)
	log.Debug("Fetching dashboards from SigNoz")

	body, err := s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
	if err != nil {
		return nil, err
	}

	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	data, ok := rawResponse["data"].([]interface{})
	if !ok {
		return body, nil
	}

	simplifiedDashboards := make([]map[string]interface{}, 0, len(data))
	for _, dashboard := range data {
		dash, ok := dashboard.(map[string]interface{})
		if !ok {
			continue
		}
		var (
			name any
			desc any
			tags any
		)
		if v, ok := dash["data"].(map[string]interface{}); ok {
			name = v["title"]
			desc = v["description"]
			tags = v["tags"]
		}

		simplifiedDashboards = append(simplifiedDashboards, map[string]interface{}{
			"uuid":        dash["id"],
			"name":        name,
			"description": desc,
			"tags":        tags,
			"createdAt":   dash["createdAt"],
			"updatedAt":   dash["updatedAt"],
			"createdBy":   dash["createdBy"],
			"updatedBy":   dash["updatedBy"],
		})
	}

	simplifiedResponse := map[string]interface{}{
		"status": rawResponse["status"],
		"data":   simplifiedDashboards,
	}

	simplifiedJSON, err := json.Marshal(simplifiedResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal simplified response: %w", err)
	}

	log.Debug("Successfully retrieved and simplified dashboards", zap.Int("count", len(simplifiedDashboards)))
	return simplifiedJSON, nil
}

func (s *SigNoz) GetDashboard(ctx context.Context, uuid string) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/dashboards/%s", s.baseURL, url.PathEscape(uuid))
	s.requestLogger(ctx).Debug("Fetching dashboard details", zap.String("uuid", uuid))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) ListServices(ctx context.Context, start, end string) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/services", s.baseURL)
	payload := map[string]string{"start": start, "end": end}
	bodyBytes, _ := json.Marshal(payload)

	s.requestLogger(ctx).Debug("Fetching services from SigNoz", zap.String("start", start), zap.String("end", end))
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes), DefaultQueryTimeout)
}

func (s *SigNoz) GetServiceTopOperations(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/service/top_operations", s.baseURL)
	payload := map[string]any{"start": start, "end": end, "service": service, "tags": tags}
	bodyBytes, _ := json.Marshal(payload)

	s.requestLogger(ctx).Debug("Fetching service top operations", zap.String("service", service))
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes), DefaultQueryTimeout)
}

func (s *SigNoz) QueryBuilderV5(ctx context.Context, body []byte) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v5/query_range", s.baseURL)
	s.requestLogger(ctx).Debug("sending request", zap.String("url", reqURL), zap.Any("body", json.RawMessage(body)))
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(otelpkg.MCPQueryPayloadKey.String(string(body)))
	}
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewBuffer(body), DefaultQueryTimeout)
}

func (s *SigNoz) GetAlertHistory(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/rules/%s/history/timeline", s.baseURL, url.PathEscape(ruleID))
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	s.requestLogger(ctx).Debug("Fetching alert history", zap.String("ruleID", ruleID))
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewBuffer(reqBody), DefaultQueryTimeout)
}

func (s *SigNoz) CreateAlertRule(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/rules", s.baseURL)
	s.requestLogger(ctx).Debug("Creating alert rule")
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewBuffer(alertJSON), DashboardWriteTimeout)
}

func (s *SigNoz) ListLogViews(ctx context.Context) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/explorer/views?sourcePage=logs", s.baseURL)
	s.requestLogger(ctx).Debug("Fetching log views from SigNoz")
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) GetLogView(ctx context.Context, viewID string) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/explorer/views/%s", s.baseURL, url.PathEscape(viewID))
	s.requestLogger(ctx).Debug("Fetching log view details", zap.String("viewID", viewID))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) GetFieldKeys(ctx context.Context, signal, metricName, searchText, fieldContext, fieldDataType, source string) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("signal", signal)
	if metricName != "" {
		params.Set("metricName", metricName)
	}
	if searchText != "" {
		params.Set("searchText", searchText)
	}
	if fieldContext != "" {
		params.Set("fieldContext", fieldContext)
	}
	if fieldDataType != "" {
		params.Set("fieldDataType", fieldDataType)
	}
	if source != "" {
		params.Set("source", source)
	}

	reqURL := fmt.Sprintf("%s/api/v1/fields/keys?%s", s.baseURL, params.Encode())
	s.requestLogger(ctx).Debug("Fetching field keys", zap.String("signal", signal), zap.String("searchText", searchText))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) GetFieldValues(ctx context.Context, signal, name, metricName, searchText, source string) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("signal", signal)
	params.Set("name", name)
	if metricName != "" {
		params.Set("metricName", metricName)
	}
	if searchText != "" {
		params.Set("searchText", searchText)
	}
	if source != "" {
		params.Set("source", source)
	}

	reqURL := fmt.Sprintf("%s/api/v1/fields/values?%s", s.baseURL, params.Encode())
	s.requestLogger(ctx).Debug("Fetching field values", zap.String("signal", signal), zap.String("name", name))
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) GetTraceDetails(ctx context.Context, traceID string, includeSpans bool, startTime, endTime int64) (json.RawMessage, error) {
	if startTime == 0 || endTime == 0 {
		return nil, fmt.Errorf("start and end time parameters are required")
	}

	filterExpression := fmt.Sprintf("traceID = '%s'", traceID)
	limit := 1000

	queryPayload := types.BuildTracesQueryPayload(startTime, endTime, filterExpression, limit)
	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query payload: %w", err)
	}

	return s.QueryBuilderV5(ctx, queryJSON)
}

func (s *SigNoz) CreateDashboard(ctx context.Context, dashboard types.Dashboard) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/dashboards", s.baseURL)
	dashboardJSON, err := json.Marshal(dashboard)
	if err != nil {
		return nil, fmt.Errorf("marshal dashboard: %w", err)
	}

	s.requestLogger(ctx).Debug("Creating dashboard")
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewBuffer(dashboardJSON), DashboardWriteTimeout)
}

func (s *SigNoz) UpdateDashboard(ctx context.Context, id string, dashboard types.Dashboard) error {
	reqURL := fmt.Sprintf("%s/api/v1/dashboards/%s", s.baseURL, url.PathEscape(id))
	dashboardJSON, err := json.Marshal(dashboard)
	if err != nil {
		return fmt.Errorf("marshal dashboard: %w", err)
	}

	s.requestLogger(ctx).Debug("Updating dashboard", zap.String("id", id))
	_, err = s.doRequest(ctx, http.MethodPut, reqURL, bytes.NewBuffer(dashboardJSON), DashboardWriteTimeout)
	return err
}

// CreateDashboardRaw creates a dashboard from pre-validated JSON bytes,
// avoiding a round-trip through types.Dashboard.
func (s *SigNoz) CreateDashboardRaw(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/dashboards", s.baseURL)
	s.requestLogger(ctx).Debug("Creating dashboard (raw)")
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewBuffer(dashboardJSON), DashboardWriteTimeout)
}

// UpdateDashboardRaw updates a dashboard from pre-validated JSON bytes,
// avoiding a round-trip through types.Dashboard.
func (s *SigNoz) UpdateDashboardRaw(ctx context.Context, id string, dashboardJSON []byte) error {
	reqURL := fmt.Sprintf("%s/api/v1/dashboards/%s", s.baseURL, url.PathEscape(id))
	s.requestLogger(ctx).Debug("Updating dashboard (raw)", zap.String("id", id))
	_, err := s.doRequest(ctx, http.MethodPut, reqURL, bytes.NewBuffer(dashboardJSON), DashboardWriteTimeout)
	return err
}

func (s *SigNoz) DeleteDashboard(ctx context.Context, id string) error {
	reqURL := fmt.Sprintf("%s/api/v1/dashboards/%s", s.baseURL, url.PathEscape(id))
	s.requestLogger(ctx).Debug("Deleting dashboard", zap.String("id", id))
	_, err := s.doRequest(ctx, http.MethodDelete, reqURL, nil, DashboardWriteTimeout)
	return err
}

// ChannelWriteTimeout is used for notification channel create/test operations.
const ChannelWriteTimeout = 30 * time.Second

func (s *SigNoz) ListNotificationChannels(ctx context.Context) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/channels", s.baseURL)
	s.requestLogger(ctx).Debug("Fetching notification channels from SigNoz")
	return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}

func (s *SigNoz) CreateNotificationChannel(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/channels", s.baseURL)
	s.requestLogger(ctx).Debug("Creating notification channel")
	return s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewReader(receiverJSON), ChannelWriteTimeout)
}

func (s *SigNoz) UpdateNotificationChannel(ctx context.Context, id string, receiverJSON []byte) (json.RawMessage, error) {
	reqURL := fmt.Sprintf("%s/api/v1/channels/%s", s.baseURL, url.PathEscape(id))
	s.requestLogger(ctx).Debug("Updating notification channel", zap.String("id", id))
	return s.doRequest(ctx, http.MethodPut, reqURL, bytes.NewReader(receiverJSON), ChannelWriteTimeout)
}

func (s *SigNoz) TestNotificationChannel(ctx context.Context, receiverJSON []byte) error {
	reqURL := fmt.Sprintf("%s/api/v1/testChannel", s.baseURL)
	s.requestLogger(ctx).Debug("Testing notification channel")
	_, err := s.doRequest(ctx, http.MethodPost, reqURL, bytes.NewReader(receiverJSON), ChannelWriteTimeout)
	return err
}
