# Plan: metric-cardinality

## Status
Done

## Context
Telemetry cost investigations need a way to inspect a metric's label structure to determine whether
high cardinality is real (unbounded labels like UUIDs, pod IDs) or bounded (namespace names, status
codes). This tool provides that data so the agent can make the cardinality assessment.

## Approach
- New tool `signoz_check_metric_cardinality` backed by `GET /api/v2/metrics/{name}/attributes`
- Required parameter: `metricName`
- Optional: `timeRange`, `start`, `end` (same pattern as other metric tools)
- Response: raw upstream JSON passthrough — label keys with cardinality counts and sample values,
  sorted highest-cardinality first by the server
- Client runs a fail-open contract check (WARN log) if the `data.attributes` shape is absent, so
  upstream drift is detectable without breaking callers
- Error handling matches the shared convention (`errs.go`): `requireStringArg` for the required arg
  (coded `VALIDATION_FAILED`), `errorWithCode(CodeValidationFailed, …)` for timestamp parsing, and
  `upstreamError(err)` for the backend call so SigNoz 401/403 surface `UNAUTHORIZED`/`PERMISSION_DENIED`

## Files Modified
- `internal/handler/tools/metric_cardinality.go` — tool registration and handler
- `internal/client/metric_cardinality.go` — client method calling the metric-attributes API
- `internal/client/interface.go` — added GetMetricCardinality to Client interface
- `internal/client/mock.go` — added GetMetricCardinalityFn to MockClient
- `internal/mcp-server/server.go` — registered handler
- `internal/handler/tools/nil_arguments_test.go` — nil-arg regression coverage
- `internal/handler/tools/metric_cardinality_test.go` — handler tests (attributes, defaults, explicit
  window, validation codes, 401/403 upstream classification)
- `internal/client/metric_cardinality_test.go` — contract-check branch coverage
- `manifest.json` — tool metadata
- `README.md` — tool table and parameter reference

## Verification
- `go test ./...`
- Confirm tool appears in `tools/list` response
- Confirm nil-arguments request returns validation error, not panic
