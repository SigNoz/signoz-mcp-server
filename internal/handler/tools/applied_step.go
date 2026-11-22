package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// SigNoz raises a step below the window's minimum to it: for logs and traces the
// window over 1500 points, at least 5 s; for the default metrics store the
// automatic step, at least 60 s. tests/e2e/tests/test_applied_step.py pins them.
const (
	minLogTraceStepSeconds = 5
	logTraceMaxPoints      = 1500
	minMetricStepSeconds   = 60
)

// appliedStepIntervals reads data.meta.stepIntervals: the step in seconds
// SigNoz ran each query at, keyed by query name. It returns nil when the
// response has no readable entry.
func appliedStepIntervals(response json.RawMessage) map[string]int64 {
	var resp struct {
		Data struct {
			Meta apitypes.Querybuildertypesv5ExecStats `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &resp); err != nil || resp.Data.Meta.StepIntervals == nil {
		return nil
	}
	applied := make(map[string]int64, len(*resp.Data.Meta.StepIntervals))
	for name, seconds := range *resp.Data.Meta.StepIntervals {
		if seconds > 0 {
			applied[name] = int64(seconds)
		}
	}
	if len(applied) == 0 {
		return nil
	}
	return applied
}

// appliedStep is the step query A ran at, or the first query's by name when no
// query is named A, and 0 when there is none. The tools send every query of a
// request the same step, so one entry stands for all.
func appliedStep(applied map[string]int64) int64 {
	if step, ok := applied["A"]; ok {
		return step
	}
	names := slices.Sorted(maps.Keys(applied))
	if len(names) == 0 {
		return 0
	}
	return applied[names[0]]
}

// warnAppliedStepDrift logs when a response breaks what the step notes assume:
// SigNoz reports the step it ran each query at and never runs one below the
// requested step. reportExpected is false where SigNoz may legitimately omit
// every entry, as for a metric it cannot find in the window.
func warnAppliedStepDrift(ctx context.Context, logger *slog.Logger, tool string, requested int64, applied map[string]int64, reportExpected bool) {
	if logger == nil {
		logger = slog.Default()
	}
	step := appliedStep(applied)
	if step == 0 {
		if reportExpected {
			logger.WarnContext(ctx, "qb response has no stepIntervals", slog.String("tool", tool))
		}
		return
	}
	if step < requested {
		logger.WarnContext(ctx, "qb ran a query below the requested step",
			slog.String("tool", tool),
			slog.Int64("requestedSeconds", requested),
			slog.Int64("appliedSeconds", step))
	}
}

// metricsStepDecision is the decisions-note line for a stepInterval the caller
// passed. It keeps the caller-provided line unless SigNoz raised the step.
func metricsStepDecision(requested int64, applied map[string]int64, source string) string {
	step := appliedStep(applied)
	if step <= requested {
		return fmt.Sprintf("stepInterval: %ds (caller-provided)", requested)
	}
	var next string
	switch {
	case strings.EqualFold(source, "meter"):
		// Cost Meter has its own floor, so name the step this response shows.
		next = fmt.Sprintf("pass %d or more", step)
	case requested < minMetricStepSeconds && step == minMetricStepSeconds:
		next = fmt.Sprintf("metric steps are never smaller than %ds", minMetricStepSeconds)
	case requested < minMetricStepSeconds:
		next = fmt.Sprintf("metric steps are never smaller than %ds, so query a shorter window for %ds steps", minMetricStepSeconds, minMetricStepSeconds)
	default:
		next = fmt.Sprintf("query a shorter window for %ds steps", requested)
	}
	return fmt.Sprintf("stepInterval: %ds (raised from the requested %ds, the minimum SigNoz allows for this window; %s)",
		step, requested, next)
}

// aggregateStepNote is the note a time_series aggregate gets when SigNoz ran
// it at a wider step than the caller asked for, and "" otherwise.
func aggregateStepNote(ctx context.Context, logger *slog.Logger, tool string, reqData *AggregateRequest, payload []byte) string {
	if reqData.RequestType != "time_series" || reqData.StepInterval == nil {
		return ""
	}
	requested := *reqData.StepInterval
	applied := appliedStepIntervals(payload)
	warnAppliedStepDrift(ctx, logger, tool, requested, applied, true)
	largest := appliedStep(applied)
	if largest <= requested {
		return ""
	}
	note := fmt.Sprintf("note: SigNoz raised stepInterval from %d s to %d s, the minimum it allows for this window.", requested, largest)
	target := requested
	if requested < minLogTraceStepSeconds {
		note += fmt.Sprintf(" Buckets are never smaller than %d s.", minLogTraceStepSeconds)
		target = minLogTraceStepSeconds
	}
	if largest > target {
		// Any window up to logTraceMaxPoints buckets of target seconds allows
		// target; round down to whole hours.
		hours := target * logTraceMaxPoints / 3600
		note += fmt.Sprintf(" For %d s buckets, query a window of %d hours or less.", target, hours)
	}
	return note
}
