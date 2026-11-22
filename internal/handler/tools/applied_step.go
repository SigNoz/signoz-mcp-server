package tools

import (
	"context"
	"fmt"
	"log/slog"
)

// SigNoz raises a logs or traces step below the window's minimum to it: the
// window over 1500 points, at least 5 s. tests/e2e/tests/test_applied_step.py
// pins both.
const (
	minLogTraceStepSeconds = 5
	logTraceMaxPoints      = 1500
)

// aggregateStepNote is the note a time_series aggregate gets when SigNoz ran
// it at a wider step than the caller asked for, and "" otherwise.
func aggregateStepNote(ctx context.Context, logger *slog.Logger, tool string, reqData *AggregateRequest, payload []byte) string {
	if reqData.RequestType != "time_series" || reqData.StepInterval == nil {
		return ""
	}
	if logger == nil {
		logger = slog.Default()
	}
	requested := *reqData.StepInterval
	applied := responseStepIntervals(ctx, logger, tool, payload)["A"]
	switch {
	case applied == 0:
		// Fail open: SigNoz reports the step of every query these tools send.
		logger.WarnContext(ctx, "qb response has no step interval for query A", slog.String("tool", tool))
		return ""
	case applied < requested:
		logger.WarnContext(ctx, "qb ran a query below the requested step",
			slog.String("tool", tool),
			slog.Int64("requestedSeconds", requested),
			slog.Int64("appliedSeconds", applied))
		return ""
	case applied == requested:
		return ""
	}
	note := fmt.Sprintf("note: SigNoz raised stepInterval from %d s to %d s, the minimum it allows for this window.", requested, applied)
	target := requested
	if requested < minLogTraceStepSeconds {
		note += fmt.Sprintf(" Buckets are never smaller than %d s.", minLogTraceStepSeconds)
		target = minLogTraceStepSeconds
	}
	if applied > target {
		// Any window up to logTraceMaxPoints buckets of target seconds allows
		// target; round down to whole hours.
		hours := target * logTraceMaxPoints / 3600
		note += fmt.Sprintf(" For %d s buckets, query a window of %d hours or less.", target, hours)
	}
	return note
}
