package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	externalURLCacheTTL = 5 * time.Minute
	externalURLRetryTTL = time.Minute
	externalURLTimeout  = 3 * time.Second
)

// GetExternalURL discovers browser links without changing the API destination.
// Cache empty results too so an unconfigured instance does not add a request to
// every tool call. Auth failures remain errors and are never cached as fallbacks.
func (s *SigNoz) GetExternalURL(ctx context.Context) (string, error) {
	s.webURLMu.Lock()
	defer s.webURLMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if time.Now().Before(s.webURLExpiresAt) {
		return s.cachedWebURL, nil
	}

	data, err := s.doRequest(ctx, http.MethodGet, s.baseURL+"/api/v1/global/config", nil, externalURLTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var statusErr *HTTPStatusError
		status := 0
		if errors.As(err, &statusErr) {
			status = statusErr.StatusCode
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return "", err
			}
		}
		s.logger.WarnContext(ctx, "SigNoz global configuration unavailable; using API URL for resource links", slog.Int("status", status))
		s.cachedWebURL = ""
		s.webURLExpiresAt = time.Now().Add(externalURLRetryTTL)
		return "", nil
	}

	var config struct {
		Data *struct {
			ExternalURL *string `json:"external_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &config); err != nil || config.Data == nil || config.Data.ExternalURL == nil {
		s.logger.WarnContext(ctx, "SigNoz global configuration is missing a readable data.external_url; using API URL for resource links")
		s.cachedWebURL = ""
		s.webURLExpiresAt = time.Now().Add(externalURLRetryTTL)
		return "", nil
	}

	externalURL, err := normalizeExternalURL(*config.Data.ExternalURL)
	if err != nil {
		// Never include the configured value or a net/url parse error: malformed
		// upstream configuration can contain accidentally embedded credentials.
		s.logger.WarnContext(ctx, "SigNoz global external_url is invalid; using API URL for resource links", slog.String("reason", err.Error()))
		s.cachedWebURL = ""
		s.webURLExpiresAt = time.Now().Add(externalURLRetryTTL)
		return "", nil
	}
	s.cachedWebURL = externalURL
	s.webURLExpiresAt = time.Now().Add(externalURLCacheTTL)
	return externalURL, nil
}

func normalizeExternalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "<unset>" || raw == "//<unset>" || raw == "//%3Cunset%3E" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("malformed URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return "", fmt.Errorf("expected an absolute HTTP(S) URL")
	}
	if u.User != nil || strings.ContainsAny(raw, "?#") {
		return "", fmt.Errorf("credentials, queries and fragments are not supported")
	}
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && addr.IsUnspecified() {
		return "", fmt.Errorf("unspecified bind addresses are not browser destinations")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
