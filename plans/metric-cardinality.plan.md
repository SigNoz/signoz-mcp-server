# Plan: metric-cardinality

## Status
Done

## Context
Telemetry cost investigations need a way to inspect a metric's label structure to determine whether
high cardinality is real (unbounded labels like UUIDs, pod IDs) or bounded (namespace names, status
codes). This tool provides that data so the agent can make the cardinality assessment.

## Approach
- New tool `signoz_check_metric_cardinality` backed by the existing field-keys API
- Required parameter: `metricName`
- Optional: `timeRange`, `start`, `end` (same pattern as other metric tools)
- Response: label keys with cardinality counts and sample values, sorted highest-cardinality first
- Handler uses `req.GetArguments()` + nil guard (not bare type assertion) to prevent nil map panic
- Covered in `TestHandlers_NilArguments_NoPanic` (required-arg handler)

## Files Modified
- `internal/handler/tools/metric_cardinality.go` — tool registration and handler
- `internal/client/metric_cardinality.go` — client method calling field-keys API
- `internal/client/interface.go` — added GetMetricCardinality to Client interface
- `internal/client/mock.go` — added GetMetricCardinalityFn to MockClient
- `internal/mcp-server/server.go` — registered handler
- `internal/handler/tools/nil_arguments_test.go` — nil-arg regression coverage
- `manifest.json` — tool metadata
- `README.md` — tool table and parameter reference

## Verification
- `go test ./...`
- Confirm tool appears in `tools/list` response
- Confirm nil-arguments request returns validation error, not panic
