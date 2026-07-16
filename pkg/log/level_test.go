package log

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

func TestLevelForError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want slog.Level
	}{
		{"canceled", context.Canceled, slog.LevelDebug},
		{"wrapped canceled", fmt.Errorf(`Post "https://tenant.signoz.cloud/api/v5/query_range": %w`, context.Canceled), slog.LevelDebug},
		{"deadline exceeded", context.DeadlineExceeded, slog.LevelError},
		{"wrapped deadline exceeded", fmt.Errorf("query: %w", context.DeadlineExceeded), slog.LevelError},
		{"generic", errors.New("boom"), slog.LevelError},
		{"nil", nil, slog.LevelError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LevelForError(tc.err); got != tc.want {
				t.Fatalf("LevelForError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
