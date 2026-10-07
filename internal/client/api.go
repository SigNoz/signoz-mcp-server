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

// apiCall is one request through the generated client.
type apiCall func(ctx context.Context, api *apiclient.Client) (*http.Response, error)

// callAPI runs a generated-client call with the tenant context, timeout, and
// shared transport, and returns the raw body. Unlike doRequest, it rejects a 2xx
// web page. Mark read-only POSTs with withReplaySafe so the transport retries them.
func (s *SigNoz) callAPI(ctx context.Context, timeout time.Duration, call apiCall) (json.RawMessage, error) {
	ctx = s.ensureTenantContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	api := &apiclient.Client{
		Server: strings.TrimRight(s.baseURL, "/") + "/",
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
