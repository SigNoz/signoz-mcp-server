package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
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

func TestHandlePatchDashboardCancellationLogLevel(t *testing.T) {
	var buf bytes.Buffer
	mock := &client.MockClient{
		PatchDashboardRawFn: func(ctx context.Context, id string, patchJSON []byte) (json.RawMessage, error) {
			return nil, fmt.Errorf("patch request aborted: %w", context.Canceled)
		},
	}
	h := newTestHandler(mock)
	h.logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	result, err := h.handlePatchDashboard(context.Background(), makeToolRequest("signoz_patch_dashboard", map[string]any{
		"id": "d-1",
		"patch": []any{
			map[string]any{"op": "replace", "path": "/spec/display/name", "value": "Renamed"},
		},
	}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected upstream error result")
	}
	if code := resultCode(t, result); code != CodeCanceled {
		t.Fatalf("code = %q, want %q", code, CodeCanceled)
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) < 2 {
		t.Fatalf("expected tool-call and failure records, got %q", buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &rec); err != nil {
		t.Fatalf("decode final log record %q: %v", lines[len(lines)-1], err)
	}
	if rec["level"] != "DEBUG" {
		t.Fatalf("level = %v, want DEBUG", rec["level"])
	}
	wantMsg := "Failed to patch dashboard in SigNoz (request cancelled by client)"
	if rec["msg"] != wantMsg {
		t.Fatalf("msg = %v, want %q", rec["msg"], wantMsg)
	}
}
