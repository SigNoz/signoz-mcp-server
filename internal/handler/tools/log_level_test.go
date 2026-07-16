package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

// TestLogUpstreamFailureLevels pins the severity contract of the shared
// upstream-failure log helper: client-driven cancellations (context.Canceled)
// log at DEBUG — but are still emitted, never dropped — while
// context.DeadlineExceeded (a real operational signal) and generic upstream
// failures stay ERROR.
func TestLogUpstreamFailureLevels(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantLevel string
		wantMsg   string
	}{
		{
			name:      "canceled logs debug with cancellation note",
			err:       fmt.Errorf(`Post "https://tenant.signoz.cloud/api/v5/query_range": %w`, context.Canceled),
			wantLevel: "DEBUG",
			wantMsg:   "Failed to search logs (request cancelled by client)",
		},
		{
			name:      "deadline exceeded stays error",
			err:       fmt.Errorf("query: %w", context.DeadlineExceeded),
			wantLevel: "ERROR",
			wantMsg:   "Failed to search logs",
		},
		{
			name:      "generic upstream failure stays error",
			err:       errors.New("boom"),
			wantLevel: "ERROR",
			wantMsg:   "Failed to search logs",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &Handler{logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}

			h.logUpstreamFailure(context.Background(), "Failed to search logs", tc.err, slog.String("filter", "x"))

			// Fail open, never fail silent: the record must always be emitted.
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("expected exactly one emitted log record, got %q: %v", buf.String(), err)
			}
			if rec["level"] != tc.wantLevel {
				t.Fatalf("level = %v, want %s", rec["level"], tc.wantLevel)
			}
			if rec["msg"] != tc.wantMsg {
				t.Fatalf("msg = %v, want %q", rec["msg"], tc.wantMsg)
			}
			if rec["filter"] != "x" {
				t.Fatalf("filter attr = %v, want %q (extra attrs must survive the helper)", rec["filter"], "x")
			}
		})
	}
}
