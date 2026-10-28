package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

const (
	SignozApiKey = "SIGNOZ-API-KEY"
	ContentType  = "Content-Type"
	UserAgent    = "User-Agent"

	// DefaultQueryTimeout is used for read-only API calls.
	DefaultQueryTimeout = 600 * time.Second
	// DashboardWriteTimeout is used for dashboard create/update operations.
	DashboardWriteTimeout = 30 * time.Second

	// analyticsIdentityCacheTTL keeps /me out of the hot analytics path;
	// identity rarely changes, so 10 min is long enough to absorb bursts.
	analyticsIdentityCacheTTL = 10 * time.Minute
	errorEnvelopeWarning      = "SigNoz error envelope drift or unsafe guidance detected"
)

var (
	ErrUnauthorized = errors.New("signoz credentials rejected")
	// ErrInstanceNotFound means the URL resolves but no SigNoz API answers
	// there — e.g. an expired/deactivated cloud workspace whose ingress serves
	// an HTML 404 page. A live SigNoz API replies to the validation endpoints
	// with JSON, even on 404.
	ErrInstanceNotFound = errors.New("no signoz instance found at URL")
	defaultUserAgent    = version.UserAgent()
)

// HTTPStatusError preserves status and response details from a non-2xx SigNoz API response.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	parsed := ParseUpstreamErrorBody(e.Body)
	if parsed.Recognized {
		if detail := parsed.ClientSafeText(); detail != "" {
			return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, detail)
		}
	}
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "unexpected status 401: authentication failed"
	case http.StatusForbidden:
		return "unexpected status 403: permission denied"
	default:
		return fmt.Sprintf("unexpected status %d", e.StatusCode)
	}
}

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
	logger         *slog.Logger
	httpClient     *http.Client
	customHeaders  map[string]string

	identityMu       sync.Mutex
	cachedIdentity   *AnalyticsIdentity
	identityCachedAt time.Time
	webURLMu         sync.Mutex
	cachedWebURL     string
	webURLExpiresAt  time.Time
	meters           *otelpkg.Meters
}

// sharedTransport is a single process-wide *http.Transport — and therefore a
// single connection pool — reused by every SigNoz client. Go pools idle
// keep-alive connections per host on the Transport, so sharing one transport lets
// all clients (and tenants) reuse connections to a given SigNoz host instead of
// re-handshaking on each request.
//
// We clone http.DefaultTransport (preserving its dial/TLS timeouts, proxy, HTTP/2
// settings, etc.) and raise the idle-connection limits. The stdlib defaults —
// MaxIdleConnsPerHost=2, MaxIdleConns=100 — are tuned for a browser-like client
// and throttle keep-alive reuse under the concurrency a multi-tenant MCP server
// sees: once more than 2 requests to the same SigNoz host are in flight, the
// surplus connections are closed rather than pooled, forcing fresh TCP+TLS
// handshakes on the next request. These values are conservative starting points;
// the per-host cap bounds reuse for a hot host while the global cap bounds total
// idle FDs across many distinct tenant hosts.
var sharedTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 200       // total idle conns across all SigNoz hosts (was 100)
	t.MaxIdleConnsPerHost = 20 // idle conns kept per host for reuse (was 2)
	return t
}()

func NewClient(log *slog.Logger, baseURL, apiKey, authHeaderName string, customHeaders map[string]string) *SigNoz {
	return &SigNoz{
		logger:         log,
		baseURL:        baseURL,
		apiKey:         apiKey,
		authHeaderName: authHeaderName,
		customHeaders:  customHeaders,
		httpClient: &http.Client{
			// Default client span name is just the HTTP method (per OTel HTTP
			// semconv — the client doesn't know a templated route). We keep
			// the default rather than stamping the raw path because several
			// SigNoz API paths embed IDs (/dashboards/{uuid}, /rules/{id},
			// /channels/{id}, /explorer/views/{id}, /rules/{id}/history/...)
			// which would blow up span-name cardinality in the backend. The
			// full URL is still attached as a span attribute for drilling.
			//
			// All clients share sharedTransport so connections to a SigNoz host
			// are pooled/reused process-wide regardless of how many clients exist.
			Transport: otelhttp.NewTransport(sharedTransport),
		},
	}
}

func (s *SigNoz) SetMeters(meters *otelpkg.Meters) {
	s.meters = meters
}

func (s *SigNoz) ensureTenantContext(ctx context.Context) context.Context {
	if _, ok := util.GetSigNozURL(ctx); !ok && s.baseURL != "" {
		return util.SetSigNozURL(ctx, s.baseURL)
	}
	return ctx
}

func (s *SigNoz) setRequestHeaders(ctx context.Context, req *http.Request, warnReserved bool) {
	req.Header.Set(ContentType, "application/json")
	req.Header.Set(s.authHeaderName, s.apiKey)
	req.Header.Set(UserAgent, defaultUserAgent)

	for name, value := range s.customHeaders {
		if strings.EqualFold(name, UserAgent) {
			if value = strings.TrimSpace(value); value != "" {
				req.Header.Set(UserAgent, value+" "+defaultUserAgent)
			}
			continue
		}
		if strings.EqualFold(name, ContentType) || strings.EqualFold(name, s.authHeaderName) {
			// Name only: a custom header that collides with the auth header carries a credential.
			if warnReserved {
				s.logger.WarnContext(ctx, "Ignoring a custom header that collides with a reserved header",
					slog.String("header", name))
			}
			continue
		}
		req.Header.Set(name, value)
	}
}

// ValidateCredentials performs a lightweight authenticated request against the
// SigNoz API so the OAuth flow can reject bad API keys or instance URLs before
// redirecting back to the MCP client.
//
// The OAuth flow only ever supplies a service-account API key, so this hits
// /api/v1/service_accounts/me directly.
func (s *SigNoz) ValidateCredentials(ctx context.Context) error {
	ctx = s.ensureTenantContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := apiclient.NewGetMyServiceAccountRequest(s.apiServer())
	if err != nil {
		return fmt.Errorf("failed to create validation request: %w", err)
	}
	status, body, err := s.doValidationRequest(ctx, req)
	if err != nil {
		s.logger.ErrorContext(ctx, "SigNoz credential validation request failed",
			slog.String("url", req.URL.String()), logpkg.ErrAttr(err))
		return fmt.Errorf("failed to reach SigNoz API: %w", err)
	}

	return s.evaluateValidationResponse(ctx, status, body)
}

// GetAnalyticsIdentity returns the org + user identity for the current
// credentials, cached per-client and mutex-serialized so a burst of events
// produces a single /me roundtrip.
//
// Auth-token clients hit /api/v2/users/me (v2 is required — it returns
// orgId, v1 doesn't). API-key clients hit /api/v1/service_accounts/me.
func (s *SigNoz) GetAnalyticsIdentity(ctx context.Context) (*AnalyticsIdentity, error) {
	ctx = s.ensureTenantContext(ctx)
	s.identityMu.Lock()
	defer s.identityMu.Unlock()

	if s.cachedIdentity != nil && time.Since(s.identityCachedAt) < analyticsIdentityCacheTTL {
		if s.meters != nil {
			attrs := otelpkg.AppendTenantURL(ctx, nil)
			attrs = otelpkg.AppendClientSource(ctx, attrs)
			s.meters.IdentityCacheHits.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
		return s.cachedIdentity, nil
	}
	if s.meters != nil {
		attrs := otelpkg.AppendTenantURL(ctx, nil)
		attrs = otelpkg.AppendClientSource(ctx, attrs)
		s.meters.IdentityCacheMisses.Add(ctx, 1, metric.WithAttributes(attrs...))
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
	ctx = s.ensureTenantContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	newRequest := apiclient.NewGetMyServiceAccountRequest
	principal := "service_account"
	if strings.EqualFold(s.authHeaderName, "Authorization") {
		newRequest = apiclient.NewGetMyUserRequest
		principal = "user"
	}

	req, err := newRequest(s.apiServer())
	if err != nil {
		return nil, fmt.Errorf("failed to create identity request: %w", err)
	}
	status, body, err := s.doValidationRequest(ctx, req)
	if err != nil {
		s.logger.ErrorContext(ctx, "SigNoz analytics identity request failed",
			slog.String("url", req.URL.String()), logpkg.ErrAttr(err))
		return nil, fmt.Errorf("failed to reach SigNoz API: %w", err)
	}
	if status != http.StatusOK {
		return nil, s.evaluateValidationResponse(ctx, status, body)
	}

	identity, err := parseAnalyticsIdentity(body, principal)
	if err != nil {
		return nil, fmt.Errorf("parse %s response: %w", req.URL.Path, err)
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

// doValidationRequest sends a generated identity request with auth headers and
// returns the HTTP status code, response body, and any transport error. It stays
// off the shared transport on purpose: credential checks must not retry, and
// their status handling is evaluateValidationResponse's.
func (s *SigNoz) doValidationRequest(ctx context.Context, req *http.Request) (int, []byte, error) {
	req = req.WithContext(ctx)
	s.setRequestHeaders(ctx, req, false)

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
func (s *SigNoz) evaluateValidationResponse(ctx context.Context, status int, body []byte) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		s.logger.WarnContext(ctx, "SigNoz credential validation failed",
			slog.Int("status", status),
			slog.Int("response.body.size_bytes", len(body)))
		return fmt.Errorf("%w: status %d", ErrUnauthorized, status)
	case http.StatusNotFound:
		if isHTMLBody(body) {
			s.logger.WarnContext(ctx, "no SigNoz API at instance URL (HTML 404)",
				slog.Int("response.body.size_bytes", len(body)))
			return fmt.Errorf("%w: status %d", ErrInstanceNotFound, status)
		}
		fallthrough
	default:
		s.logger.WarnContext(ctx, "SigNoz credential validation returned unexpected status",
			slog.Int("status", status),
			slog.Int("response.body.size_bytes", len(body)))
		return errors.New(newHTTPStatusError(status, body).Error())
	}
}

// isHTMLBody reports whether a response body is a markup document (HTML/XML)
// rather than a JSON API payload — the first non-whitespace byte of JSON is
// never '<'. Empty and plain-text bodies conservatively count as non-HTML so
// they keep the transient "try again" path.
func isHTMLBody(body []byte) bool {
	trimmed := bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	trimmed = bytes.TrimLeft(trimmed, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '<'
}

const (
	maxRetries    = 3
	retryBaseWait = 100 * time.Millisecond
	retryMultiply = 4
)

// maxResponseBytes caps how many bytes doRequest buffers from one backend
// response, so an unbounded response (e.g. a builder query for millions of
// rows) can't OOM the shared pod. We error rather than truncate, so callers
// never get invalid JSON.
const maxResponseBytes int64 = 64 << 20 // 64 MiB

// doRequest is the escape hatch for endpoints the SigNoz spec does not
// document, so the generated client has no method for them. It uses the
// method's default replay policy: mutating POSTs are single-attempt because the
// backend does not accept idempotency keys and a transport failure can happen
// after commit.
func (s *SigNoz) doRequest(ctx context.Context, method, reqURL string, body []byte, timeout time.Duration) (json.RawMessage, error) {
	return s.doRequestWithReplayPolicy(ctx, method, reqURL, body, timeout, isReplaySafeMethod(method))
}

// doReplaySafePost is for read-only upstream operations that happen to use
// POST because their query payload is carried in the request body.
func (s *SigNoz) doReplaySafePost(ctx context.Context, reqURL string, body []byte, timeout time.Duration) (json.RawMessage, error) {
	return s.doRequestWithReplayPolicy(ctx, http.MethodPost, reqURL, body, timeout, true)
}

func (s *SigNoz) doRequestWithReplayPolicy(ctx context.Context, method, reqURL string, body []byte, timeout time.Duration, replaySafe bool) (json.RawMessage, error) {
	return s.callAPI(withReplaySafe(ctx, replaySafe), timeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		var reqBody io.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		return api.Client.Do(req)
	})
}

// readResponse returns a buffered response's body on 2xx and an HTTPStatusError
// otherwise.
func readResponse(resp *http.Response) (json.RawMessage, error) {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}
	return nil, newHTTPStatusError(resp.StatusCode, body)
}

func newHTTPStatusError(statusCode int, respBody []byte) *HTTPStatusError {
	return &HTTPStatusError{
		StatusCode: statusCode,
		Body:       string(respBody),
	}
}

func isRetryableStatus(code int) bool {
	return code == 429 || code == 502 || code == 503 || code == 504
}

func isReplaySafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *SigNoz) ListMetrics(ctx context.Context, start, end int64, limit int, searchText, source string) (json.RawMessage, error) {
	params := &apiclient.ListMetricsParams{
		Start:      optInt(int(start)),
		End:        optInt(int(end)),
		Limit:      optInt(limit),
		SearchText: optString(searchText),
		Source:     optString(source),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing metrics", slog.String("searchText", searchText))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListMetrics(ctx, params)
	})
}

// ListAlerts lists alert instances. The SigNoz spec does not yet document the
// Alertmanager filter parameters of GET /api/v1/alerts, so they are appended to
// the generated request.
func (s *SigNoz) ListAlerts(ctx context.Context, params types.ListAlertsParams) (json.RawMessage, error) {
	query := params.QueryParams().Encode()
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching alerts from SigNoz", slog.String("query", query))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetAlerts(ctx, withRawQuery(query))
	})
}

// ListAlertRules returns one server-side page of rule summaries from
// GET /api/v3/rules. SigNoz clamps limit to its own maximum, so the handler
// walks pages until it has the window it was asked for.
func (s *SigNoz) ListAlertRules(ctx context.Context, limit, offset int) (json.RawMessage, error) {
	params := &apiclient.ListRulesV3Params{Limit: optInt(limit), Offset: optInt(offset)}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching alert rules from SigNoz", slog.Int("limit", limit), slog.Int("offset", offset))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListRulesV3(ctx, params)
	})
}

func (s *SigNoz) GetAlertByRuleID(ctx context.Context, ruleID string) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching alert rule details", slog.String("ruleID", ruleID))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetRuleByID(ctx, ruleID)
	})
}

// ListDashboards returns the v2 dashboard list (GET /api/v2/dashboards). The v2
// API paginates server-side, so limit/offset are forwarded as query params and
// the ListableDashboardV2 response ({dashboards, tags, total}) is passed through
// verbatim. filter (the API's `query` filter DSL), sort, and order are forwarded
// when non-empty; the API applies its own defaults otherwise.
func (s *SigNoz) ListDashboards(ctx context.Context, limit, offset int, filter, sort, order string) (json.RawMessage, error) {
	params := &apiclient.ListDashboardsV2Params{
		Query:  optString(filter),
		Sort:   optEnum[apitypes.DashboardtypesListSort](sort),
		Order:  optEnum[apitypes.DashboardtypesListOrder](order),
		Limit:  optInt(limit),
		Offset: optInt(offset),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching dashboards from SigNoz (v2)")
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListDashboardsV2(ctx, params)
	})
}

func (s *SigNoz) GetDashboard(ctx context.Context, uuid string) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching dashboard details", slog.String("uuid", uuid))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetDashboardV2(ctx, uuid)
	})
}

// ListServices and GetServiceTopOperations call endpoints the SigNoz spec does
// not document, so they stay on the hand-written request path.
func (s *SigNoz) ListServices(ctx context.Context, start, end string) (json.RawMessage, error) {
	payload := map[string]string{"start": start, "end": end}
	bodyBytes, _ := json.Marshal(payload)

	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching services from SigNoz",
		slog.String("start", start), slog.String("end", end))
	return s.doReplaySafePost(ctx, s.baseURL+"/api/v1/services", bodyBytes, DefaultQueryTimeout)
}

func (s *SigNoz) GetServiceTopOperations(ctx context.Context, start, end, service string, tags json.RawMessage) (json.RawMessage, error) {
	payload := map[string]any{"start": start, "end": end, "service": service, "tags": tags}
	bodyBytes, _ := json.Marshal(payload)

	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching service top operations", slog.String("service", service))
	return s.doReplaySafePost(ctx, s.baseURL+"/api/v1/service/top_operations", bodyBytes, DefaultQueryTimeout)
}

func (s *SigNoz) QueryBuilderV5(ctx context.Context, body []byte) (json.RawMessage, error) {
	ctx = s.ensureTenantContext(ctx)
	s.logger.DebugContext(ctx, "sending request",
		slog.String("path", "/api/v5/query_range"),
		slog.String("body", logpkg.TruncBody(body)),
		slog.Int("request.body.size_bytes", len(body)))
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(otelpkg.MCPQueryPayloadKey.String(string(body)))
	}
	return s.callAPI(withReplaySafe(ctx, true), DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.QueryRangeV5WithBody(ctx, jsonContentType, bytes.NewReader(body))
	})
}

func (s *SigNoz) GetAlertHistory(ctx context.Context, ruleID string, params *apiclient.GetRuleHistoryTimelineParams) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching alert history", slog.String("ruleID", ruleID))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetRuleHistoryTimeline(ctx, ruleID, params)
	})
}

func (s *SigNoz) CreateAlertRule(ctx context.Context, alertJSON []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Creating alert rule")
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.CreateRuleWithBody(ctx, jsonContentType, bytes.NewReader(alertJSON))
	})
}

func (s *SigNoz) UpdateAlertRule(ctx context.Context, ruleID string, alertJSON []byte) error {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Updating alert rule", slog.String("ruleID", ruleID))
	_, err := s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.UpdateRuleByIDWithBody(ctx, ruleID, jsonContentType, bytes.NewReader(alertJSON))
	})
	return err
}

func (s *SigNoz) DeleteAlertRule(ctx context.Context, ruleID string) error {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Deleting alert rule", slog.String("ruleID", ruleID))
	_, err := s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.DeleteRuleByID(ctx, ruleID)
	})
	return err
}

func (s *SigNoz) ListViews(ctx context.Context, source, name string) (json.RawMessage, error) {
	params := &apiclient.ListSavedViewsParams{
		Source: ptr(apitypes.SavedviewtypesSource(source)),
		Name:   optString(name),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Listing saved views", slog.String("source", source))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.ListSavedViews(ctx, params)
	})
}

func (s *SigNoz) GetView(ctx context.Context, viewID string) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching saved view", slog.String("viewID", viewID))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetSavedView(ctx, viewID)
	})
}

func (s *SigNoz) CreateView(ctx context.Context, body []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Creating saved view")
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.CreateSavedViewWithBody(ctx, jsonContentType, bytes.NewReader(body))
	})
}

func (s *SigNoz) UpdateView(ctx context.Context, viewID string, body []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Updating saved view", slog.String("viewID", viewID))
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.UpdateSavedViewWithBody(ctx, viewID, jsonContentType, bytes.NewReader(body))
	})
}

func (s *SigNoz) DeleteView(ctx context.Context, viewID string) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Deleting saved view", slog.String("viewID", viewID))
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.DeleteSavedView(ctx, viewID)
	})
}

func (s *SigNoz) GetFieldKeys(ctx context.Context, signal, metricName, searchText, fieldContext, fieldDataType, source string) (json.RawMessage, error) {
	params := &apiclient.GetFieldsKeysParams{
		Signal:        ptr(apitypes.TelemetrytypesSignal(signal)),
		Source:        optEnum[apitypes.TelemetrytypesSource](source),
		FieldContext:  optEnum[apitypes.TelemetrytypesFieldContext](fieldContext),
		FieldDataType: optEnum[apitypes.TelemetrytypesFieldDataType](fieldDataType),
		MetricName:    optString(metricName),
		SearchText:    optString(searchText),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching field keys",
		slog.String("signal", signal),
		slog.String("searchText", searchText))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetFieldsKeys(ctx, params)
	})
}

func (s *SigNoz) GetFieldValues(ctx context.Context, signal, name, metricName, searchText, fieldContext, source string) (json.RawMessage, error) {
	params := &apiclient.GetFieldsValuesParams{
		Signal:       ptr(apitypes.TelemetrytypesSignal(signal)),
		Source:       optEnum[apitypes.TelemetrytypesSource](source),
		FieldContext: optEnum[apitypes.TelemetrytypesFieldContext](fieldContext),
		MetricName:   optString(metricName),
		SearchText:   optString(searchText),
		Name:         ptr(name),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching field values",
		slog.String("signal", signal),
		slog.String("name", name))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetFieldsValues(ctx, params)
	})
}

// The AI fields endpoints always read trace fields, so they take no signal, source, or metricName.
func (s *SigNoz) GetAIFieldKeys(ctx context.Context, searchText, fieldContext, fieldDataType string) (json.RawMessage, error) {
	params := &apiclient.GetAIObservabilityFieldsKeysParams{
		FieldContext:  optEnum[apitypes.TelemetrytypesFieldContext](fieldContext),
		FieldDataType: optEnum[apitypes.TelemetrytypesFieldDataType](fieldDataType),
		SearchText:    optString(searchText),
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching AI field keys", slog.String("searchText", searchText))
	return s.callAPI(ctx, DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetAIObservabilityFieldsKeys(ctx, params)
	})
}

// traceLookupStartMs opens the get_trace_details window. SigNoz narrows a
// trace_id query to the trace's stored time range, so the window only has to
// contain the trace. SigNoz reads a timestamp's unit from its digit count, so
// the floor is the earliest 13-digit millisecond value (2001-09-09).
const traceLookupStartMs = int64(1_000_000_000_000)

// GetTraceDetails returns the spans of one trace by its ID alone.
func (s *SigNoz) GetTraceDetails(ctx context.Context, traceID string, includeSpans bool) (json.RawMessage, error) {
	filterExpression := "trace_id = " + querybuilder.QuoteFilterLiteral(traceID)
	limit := 1000

	queryPayload, err := querybuilder.SpanSearch(traceLookupStartMs, time.Now().UnixMilli(), querybuilder.TraceSearchOptions{
		Filter:       filterExpression,
		Limit:        limit,
		SelectFields: querybuilder.TraceDetailSelectFields,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build query payload: %w", err)
	}
	queryJSON, err := json.Marshal(queryPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal query payload: %w", err)
	}

	return s.QueryBuilderV5(ctx, queryJSON)
}

// CreateDashboardRaw creates a v2 (Perses) dashboard from raw JSON bytes.
// The MCP server is a pass-through: the v2 API validates the payload
// (schemaVersion, DisallowUnknownFields, panel/query rules), so the bytes are
// forwarded as-is to POST /api/v2/dashboards.
func (s *SigNoz) CreateDashboardRaw(ctx context.Context, dashboardJSON []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Creating dashboard (raw)")
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.CreateDashboardV2WithBody(ctx, jsonContentType, bytes.NewReader(dashboardJSON))
	})
}

// UpdateDashboardRaw replaces a v2 dashboard via PUT /api/v2/dashboards/{id}.
// The body is the full UpdatableDashboardV2 post-update state; the v2 API
// rejects locked dashboards and treats name as immutable.
func (s *SigNoz) UpdateDashboardRaw(ctx context.Context, id string, dashboardJSON []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Updating dashboard (raw)", slog.String("id", id))
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.UpdateDashboardV2WithBody(ctx, id, jsonContentType, bytes.NewReader(dashboardJSON))
	})
}

// PatchDashboardRaw applies an RFC 6902 JSON Patch to a v2 dashboard via
// PATCH /api/v2/dashboards/{id}. The body is the JSON Patch operation array.
func (s *SigNoz) PatchDashboardRaw(ctx context.Context, id string, patchJSON []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Patching dashboard (raw)", slog.String("id", id))
	return s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.PatchDashboardV2WithBody(ctx, id, jsonContentType, bytes.NewReader(patchJSON))
	})
}

func (s *SigNoz) DeleteDashboard(ctx context.Context, id string) error {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Deleting dashboard", slog.String("id", id))
	_, err := s.callAPI(ctx, DashboardWriteTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.DeleteDashboardV2(ctx, id)
	})
	return err
}

// ChannelWriteTimeout is used for notification channel create/test operations.
const ChannelWriteTimeout = 30 * time.Second

func (s *SigNoz) GetNotificationChannel(ctx context.Context, id string) (json.RawMessage, error) {
	return s.getNotificationChannelV2(ctx, id)
}

func (s *SigNoz) CreateNotificationChannel(ctx context.Context, receiverJSON []byte) (json.RawMessage, error) {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Creating notification channel")
	response, requestErr := s.notificationChannelWrite(ctx, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.CreateNotificationChannelWithBody(ctx, jsonContentType, bytes.NewReader(receiverJSON))
	})
	if requestErr != nil {
		return nil, requestErr
	}
	channel, err := s.decodeNotificationChannelResponse("create", response, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(channel)
}

func (s *SigNoz) UpdateNotificationChannel(ctx context.Context, id string, receiverJSON []byte) error {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Updating notification channel", slog.String("id", id))
	response, requestErr := s.notificationChannelWrite(ctx, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.UpdateNotificationChannelWithBody(ctx, id, jsonContentType, bytes.NewReader(receiverJSON))
	})
	if requestErr != nil {
		return requestErr
	}
	if _, err := s.decodeNotificationChannelResponse("update", response, id); err != nil {
		return err
	}
	return nil
}

func (s *SigNoz) DeleteNotificationChannel(ctx context.Context, id string) error {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Deleting notification channel", slog.String("id", id))
	_, err := s.notificationChannelWrite(ctx, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.DeleteNotificationChannel(ctx, id)
	})
	return err
}

func (s *SigNoz) GetTopMetrics(ctx context.Context, start, end int64, limit int) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"start":   start,
		"end":     end,
		"limit":   limit,
		"mode":    "samples",
		"treemap": "samples",
		"filter":  map[string]string{"expression": ""},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching metrics treemap",
		slog.Int("limit", limit))
	return s.callAPI(withReplaySafe(ctx, true), DefaultQueryTimeout, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.GetMetricsTreemapWithBody(ctx, jsonContentType, bytes.NewReader(body))
	})
}

func (s *SigNoz) TestNotificationChannel(ctx context.Context, receiverJSON []byte) error {
	s.logger.DebugContext(s.ensureTenantContext(ctx), "Testing notification channel")
	_, err := s.notificationChannelWrite(ctx, func(ctx context.Context, api *apiclient.Client) (*http.Response, error) {
		return api.TestNotificationChannelWithBody(ctx, jsonContentType, bytes.NewReader(receiverJSON))
	})
	return err
}
