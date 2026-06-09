# Plan: signoz_get_metrics_stats Tool

## Status
Done

## Context
Issue #176 identifies a missing tool for telemetry cost optimization: `signoz_get_metrics_stats`, which ranks metrics by ingested sample count via `POST /api/v2/metrics/stats`. This enables workflows that identify the most expensive metrics driving ingestion costs.

A follow-up tool (`signoz_get_metric_attributes`) for `GET /api/v2/metrics/{name}/attributes` is planned separately.

## Approach

Wrap `POST /api/v2/metrics/stats`. Ordering is hardcoded to `samples desc` in the client (the only meaningful ranking for cost analysis — no need to expose it as a tool parameter). The handler resolves timestamps via the existing `resolveTimestamps` helper.

## Files to Modify

- `internal/client/interface.go` — add `GetMetricsStats(ctx, start, end int64, limit int)`
- `internal/client/client.go` — implement with POST JSON body
- `internal/client/mock.go` — add `GetMetricsStatsFn` and delegate method
- `internal/handler/tools/metrics_stats.go` — new file: `RegisterMetricsStatsHandlers` + `handleGetMetricsStats`
- `internal/handler/tools/metrics_stats_test.go` — new file: unit tests
- `internal/mcp-server/server.go` — register `RegisterMetricsStatsHandlers`
- `README.md` — tool table entry + parameter reference
- `manifest.json` — tool entry

## Verification
- `go test ./...` passes
- Tool callable with `timeRange="1h"` and with explicit `start`/`end`
- Response includes metrics ranked by sample count
