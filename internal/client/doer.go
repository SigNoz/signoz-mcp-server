package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// replaySafeKey carries the retry decision for one call into the transport. The
// generated client builds requests without knowing which POSTs are reads, so
// callers mark those on the context.
type replaySafeKey struct{}

func withReplaySafe(ctx context.Context, replaySafe bool) context.Context {
	return context.WithValue(ctx, replaySafeKey{}, replaySafe)
}

func replaySafeFor(req *http.Request) bool {
	if replaySafe, ok := req.Context().Value(replaySafeKey{}).(bool); ok {
		return replaySafe
	}
	return isReplaySafeMethod(req.Method)
}

// doer is the transport shared by the hand-written calls and the generated API
// client. It stamps SigNoz headers, retries replay-safe requests, and buffers
// each response under maxResponseBytes. It returns the final response whatever
// its status, so callers decide how to shape errors.
type doer struct {
	s *SigNoz
}

func (d doer) Do(req *http.Request) (*http.Response, error) {
	s := d.s
	ctx := req.Context()
	reqURL := req.URL.String()

	s.setRequestHeaders(ctx, req, true)

	maxAttempts := 1
	// A body without GetBody can't be resent, so such a request gets one attempt.
	canResend := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	if replaySafeFor(req) && canResend {
		maxAttempts = maxRetries
	}

	var lastErr error
	errorEnvelopeDriftWarned := false
	wait := retryBaseWait

	for attempt := range maxAttempts {
		attemptReq := req
		if attempt > 0 {
			attemptReq = req.Clone(ctx)
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("failed to create request: %w", err)
				}
				attemptReq.Body = body
			}
		}

		resp, err := s.httpClient.Do(attemptReq)
		if err != nil {
			// Don't retry on context cancellation.
			if ctx.Err() != nil {
				return nil, fmt.Errorf("request cancelled: %w", err)
			}
			lastErr = fmt.Errorf("failed to do request: %w", err)
			if attempt < maxAttempts-1 {
				s.logger.DebugContext(ctx, "Request failed, will retry",
					slog.String("url", reqURL),
					slog.Int("attempt", attempt+1),
					logpkg.ErrAttr(err))
				if err := sleepCtx(ctx, wait); err != nil {
					return nil, fmt.Errorf("retry aborted: %w", lastErr)
				}
				wait *= retryMultiply
				continue
			}
			if maxAttempts > 1 {
				s.logger.WarnContext(ctx, "Request failed after retries exhausted",
					slog.String("url", reqURL),
					slog.Int("attempt", attempt+1),
					logpkg.ErrAttr(err))
			} else {
				s.logger.WarnContext(ctx, "Request failed and method is not replay-safe",
					slog.String("url", reqURL),
					slog.String("method", req.Method),
					logpkg.ErrAttr(err))
			}
			break
		}

		// Read one byte past the cap to detect (and reject, not truncate) an
		// over-limit response. Oversize is terminal, not retried.
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = resp.Body.Close()

		if readErr != nil {
			return nil, fmt.Errorf("failed to read response body: %w", readErr)
		}
		if int64(len(respBody)) > maxResponseBytes {
			return nil, fmt.Errorf("response body (status %d) exceeds maximum allowed size of %d bytes; if this was a data query, narrow it (reduce limit, time range, or cardinality)", resp.StatusCode, maxResponseBytes)
		}
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		resp.ContentLength = int64(len(respBody))

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		if !errorEnvelopeDriftWarned {
			parsedError := ParseUpstreamErrorBody(string(respBody))
			if parsedError.StatusError && (!parsedError.Recognized || len(parsedError.DriftFields) > 0) {
				attrs := []any{
					slog.Int("status", resp.StatusCode),
					slog.Int("attempt", attempt+1),
					slog.Int("response.body.size_bytes", len(respBody)),
					slog.Bool("recognized", parsedError.Recognized),
				}
				if len(parsedError.DriftFields) > 0 {
					attrs = append(attrs, slog.Any("fields", append([]string(nil), parsedError.DriftFields...)))
				}
				s.logger.WarnContext(ctx, errorEnvelopeWarning, attrs...)
				errorEnvelopeDriftWarned = true
			}
		}

		// Retry on transient server errors.
		if isRetryableStatus(resp.StatusCode) && attempt < maxAttempts-1 {
			lastErr = newHTTPStatusError(resp.StatusCode, respBody)
			s.logger.DebugContext(ctx, "Retryable status, will retry",
				slog.String("url", reqURL),
				slog.Int("status", resp.StatusCode),
				slog.Int("attempt", attempt+1),
				slog.Int("response.body.size_bytes", len(respBody)))
			if err := sleepCtx(ctx, wait); err != nil {
				return nil, fmt.Errorf("retry aborted: %w", lastErr)
			}
			wait *= retryMultiply
			continue
		}

		retryable := maxAttempts > 1 && isRetryableStatus(resp.StatusCode)
		attrs := []any{
			slog.String("url", reqURL),
			slog.Int("status", resp.StatusCode),
			slog.Int("attempt", attempt+1),
			slog.Int("response.body.size_bytes", len(respBody)),
			slog.Bool("retryable", retryable),
		}
		if retryable {
			attrs = append(attrs, slog.Bool("retries_exhausted", true))
		}
		s.logger.WarnContext(ctx, "SigNoz request returned unexpected status", attrs...)
		return resp, nil
	}

	return nil, lastErr
}

func sleepCtx(ctx context.Context, wait time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}
