package docs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	"golang.org/x/time/rate"
)

type Fetcher struct {
	client     *http.Client
	userAgent  string
	timeout    time.Duration
	limiter    *rate.Limiter
	concurrent chan struct{}
	sleep      func(context.Context, time.Duration) error
}

type FetcherConfig struct {
	UserAgent       string
	Timeout         time.Duration
	MaxConcurrency  int
	RequestsPerSec  float64
	Transport       http.RoundTripper
	FollowRedirects int
}

func NewFetcher(cfg FetcherConfig) *Fetcher {
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 20
	}
	if cfg.RequestsPerSec <= 0 {
		cfg.RequestsPerSec = 50
	}
	maxRedirects := cfg.FollowRedirects
	if maxRedirects <= 0 {
		maxRedirects = 5
	}
	client := &http.Client{Timeout: cfg.Timeout, Transport: cfg.Transport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if !IsDocURL(req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &Fetcher{
		client:     client,
		userAgent:  cfg.UserAgent,
		timeout:    cfg.Timeout,
		limiter:    rate.NewLimiter(rate.Limit(cfg.RequestsPerSec), int(cfg.RequestsPerSec)),
		concurrent: make(chan struct{}, cfg.MaxConcurrency),
		sleep: func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) PageFetch {
	canonical, ok := CanonicalDocURL(rawURL)
	if !ok {
		return PageFetch{Status: FetchStatusOutOfScope, URL: rawURL, Err: fmt.Errorf("out of scope URL")}
	}
	select {
	case f.concurrent <- struct{}{}:
		defer func() { <-f.concurrent }()
	case <-ctx.Done():
		return PageFetch{Status: FetchStatusError, URL: canonical, Err: ctx.Err()}
	}
	if err := f.limiter.Wait(ctx); err != nil {
		return PageFetch{Status: FetchStatusError, URL: canonical, Err: err}
	}
	var lastErr error
	var retryCount int
	var retryStatus int
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			retryCount++
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonical, nil)
		if err != nil {
			return PageFetch{Status: FetchStatusError, URL: canonical, Err: err}
		}
		req.Header.Set("Accept", "text/markdown")
		req.Header.Set("User-Agent", f.userAgent)
		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return PageFetch{Status: FetchStatusError, URL: canonical, Err: ctx.Err()}
			}
			if attempt < 3 {
				if sleepErr := f.sleep(ctx, backoffDelay(attempt, 0)); sleepErr != nil {
					return PageFetch{Status: FetchStatusError, URL: canonical, Err: sleepErr, RetryCount: retryCount, RetryStatus: retryStatus}
				}
				continue
			}
			break
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		_ = resp.Body.Close()
		// When http.Client.CheckRedirect refuses an out-of-scope redirect by
		// returning http.ErrUseLastResponse, the resp we receive is the 3xx
		// itself, with resp.Request.URL still pointing at the prior in-scope
		// URL. Probe the Location header first so we correctly flag the page
		// as out-of-scope instead of silently treating the empty 3xx body as
		// a successful in-scope fetch.
		finalURL := resp.Request.URL.String()
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			if loc := resp.Header.Get("Location"); loc != "" {
				target, err := resp.Request.URL.Parse(loc)
				if err == nil {
					resolved := target.String()
					if !IsDocURL(resolved) {
						return PageFetch{Status: FetchStatusOutOfScope, URL: canonical, FinalURL: resolved, StatusCode: resp.StatusCode, Err: fmt.Errorf("redirected out of scope")}
					}
				}
			}
		}
		if !IsDocURL(finalURL) {
			return PageFetch{Status: FetchStatusOutOfScope, URL: canonical, FinalURL: finalURL, StatusCode: resp.StatusCode, Err: fmt.Errorf("redirected out of scope")}
		}
		if resp.StatusCode == http.StatusNotFound {
			return PageFetch{Status: FetchStatusNotFound, URL: canonical, FinalURL: finalURL, StatusCode: resp.StatusCode, FetchedAt: time.Now(), RetryCount: retryCount, RetryStatus: retryStatus}
		}
		if readErr != nil {
			lastErr = readErr
			break
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return PageFetch{Status: FetchStatusOK, URL: canonical, FinalURL: finalURL, StatusCode: resp.StatusCode, Body: string(body), ETag: resp.Header.Get("ETag"), FetchedAt: time.Now(), RetryCount: retryCount, RetryStatus: retryStatus}
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode >= 500 {
			retryStatus = resp.StatusCode
			if attempt < 3 {
				if sleepErr := f.sleep(ctx, backoffDelay(attempt, retryAfter(resp.Header.Get("Retry-After")))); sleepErr != nil {
					return PageFetch{Status: FetchStatusError, URL: canonical, Err: sleepErr, RetryCount: retryCount, RetryStatus: retryStatus}
				}
				continue
			}
		}
		lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
		break
	}
	return PageFetch{Status: FetchStatusError, URL: canonical, Err: lastErr, RetryCount: retryCount, RetryStatus: retryStatus}
}

func backoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	base := time.Second << attempt
	jitter := time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
	return base + jitter
}

func retryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		return time.Until(t)
	}
	return 0
}

func defaultUserAgent() string {
	ver := strings.TrimSpace(version.Version)
	if ver == "" {
		ver = "dev"
	}
	return fmt.Sprintf("signoz-mcp-server/%s (+https://github.com/SigNoz/signoz-mcp-server; docs-indexer)", ver)
}

func RemoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
