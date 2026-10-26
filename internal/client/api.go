package client

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient"
)

// ErrNonJSONResponse means SigNoz answered a 2xx with a web page instead of an
// API payload. Self-hosted SigNoz serves its web app for API routes it doesn't
// have, and an auth proxy in front of SigNoz serves its login page.
var ErrNonJSONResponse = errors.New("SigNoz returned an HTML page instead of a JSON API response. " +
	"This SigNoz version may not support this operation, or a proxy in front of SigNoz (such as a login page) intercepted the request. " +
	"Check that SigNoz is on the latest release and that the URL points at the SigNoz API")

// jsonContentType is the request content type for every raw-passthrough body.
const jsonContentType = "application/json"

// apiCall is one request through the generated client.
type apiCall func(ctx context.Context, api *apiclient.Client) (*http.Response, error)

// apiServer is the base URL in the trailing-slash form the generated request
// constructors resolve operation paths against.
func (s *SigNoz) apiServer() string {
	return strings.TrimRight(s.baseURL, "/") + "/"
}

// callAPI runs a generated-client call with the tenant context, timeout, and
// shared transport, and returns the raw body. It rejects a 2xx web page. Mark
// read-only POSTs with withReplaySafe so the transport retries them, and
// mutations on replay-safe methods with withReplaySafe(ctx, false) so it doesn't.
func (s *SigNoz) callAPI(ctx context.Context, timeout time.Duration, call apiCall) (json.RawMessage, error) {
	ctx = s.ensureTenantContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	api := &apiclient.Client{
		Server: s.apiServer(),
		Client: doer{s: s},
	}
	resp, err := call(ctx, api)
	if err != nil {
		return nil, err
	}
	contentType := resp.Header.Get(ContentType)
	body, err := readResponse(resp)
	if err != nil {
		return nil, err
	}
	if strings.Contains(strings.ToLower(contentType), "html") || isHTMLBody(body) {
		s.logger.WarnContext(ctx, "SigNoz returned a web page instead of JSON",
			slog.String("url", resp.Request.URL.String()),
			slog.Int("status", resp.StatusCode),
			slog.String("content_type", contentType),
			slog.Int("response.body.size_bytes", len(body)))
		return nil, ErrNonJSONResponse
	}
	return body, nil
}

// withRawQuery appends query parameters the pinned spec does not document for
// an operation, so a call can keep using the generated path and method.
func withRawQuery(rawQuery string) apiclient.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if rawQuery != "" {
			req.URL.RawQuery = rawQuery
		}
		return nil
	}
}

// The generated parameter structs take pointers so an omitted parameter is
// left off the query string, which keeps SigNoz's own defaults in charge.

func ptr[T any](v T) *T { return &v }

func optString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func optInt(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

func optEnum[T ~string](v string) *T {
	if v == "" {
		return nil
	}
	return ptr(T(v))
}
