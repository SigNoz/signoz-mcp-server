# Plan: signoz_get_top_metrics Tool

## Status
Done

## Context
Issue #176 identifies a missing tool for telemetry cost optimization: a tool that ranks metrics by ingested sample count. Initially prototyped against `POST /api/v2/metrics/stats`, then switched to `POST /api/v2/metrics/treemap` which returns pre-computed percentages and richer per-metric data. The final tool is named `signoz_get_top_metrics`.

A follow-up tool (`signoz_check_metric_cardinality`) for `GET /api/v2/metrics/{name}/attributes` is planned separately (PR #208).

## Approach

Wrap `POST /api/v2/metrics/treemap`. Request body includes `mode: "samples"`, `treemap: "samples"`, and `limit: 100`. Ordering is hardcoded to samples descending in the client (the only meaningful ranking for cost analysis — no need to expose it as a tool parameter). The handler resolves timestamps via the existing `resolveTimestamps` helper with a default of `7d`.

## Files Modified

- `internal/client/interface.go` — add `GetTopMetrics(ctx, start, end int64, limit int)`
- `internal/client/client.go` — implement with POST to `/api/v2/metrics/treemap`
- `internal/client/mock.go` — add `GetTopMetricsFn` and delegate method
- `internal/handler/tools/metrics_top.go` — new file: `RegisterTopMetricsHandlers` + `handleGetTopMetrics`
- `internal/handler/tools/metrics_top_test.go` — new file: unit tests
- `internal/mcp-server/server.go` — register `RegisterTopMetricsHandlers`
- `README.md` — tool table entry + parameter reference
- `manifest.json` — tool entry

## Verification
- `go test ./...` passes
- Tool callable with no arguments (defaults to 7d), with `timeRange`, and with explicit `start`/`end`
- Response includes top 100 metrics ranked by sample count with pre-computed percentages
