# Plan: Cost Meter `source` Parameter

## Status
Done

## Context
The SigNoz API's `/api/v5/query_range` accepts a `source` field (value `"meter"`) to route queries to the Cost Meter data source. Live testing confirmed it belongs inside each `builder_query` spec object — as a sibling of `name` and `signal` — not at the top-level `QueryPayload` envelope. Neither `signoz_query_metrics` nor `signoz_execute_builder_query` exposed this field, blocking cost analysis workflows.

## Approach

### 1. `pkg/types/querybuilder.go`
- Add `Source string json:"source,omitempty"` to `QuerySpec` (sibling of `Name` and `Signal`).
- Update `BuildMetricsQueryPayloadJSON` signature to accept `source string` and set `spec.Source = source` on each `builder_query` spec built inside the function. Formula specs (`builder_formula`) are skipped — source only applies to data-fetching specs.

### 2. `internal/handler/tools/metrics_helper.go`
- Add `Source string` to `metricsQueryRequest`.
- Parse `source` string arg in `parseMetricsQueryArgs`.

### 3. `internal/handler/tools/metrics_query.go`
- Pass `mqr.Source` to `BuildMetricsQueryPayloadJSON`.

### 4. `internal/handler/tools/metrics.go`
- Add `source` `mcp.WithString` parameter to the `signoz_query_metrics` tool definition.

### 5. `pkg/types/querybuilder_test.go`
- Add `TestQueryPayloadRoundTrip_PreservesSource` — verifies `source` inside a `builder_query` spec survives unmarshal → validate → marshal, and that an empty source is omitted (`omitempty`).
- Add `TestBuildMetricsQueryPayloadJSON_AppliesSource` — covers the `signoz_query_metrics` build path directly: source lands on every `builder_query` spec, never on `builder_formula`, and is omitted when empty.

### 6. `pkg/metricsrules/guide.go` (agent-facing discoverability)
- Add a "Cost Meter (Telemetry Ingestion Volume)" section to the `signoz://metrics-aggregation-guide` resource: explains the `source: "meter"` field placement, lists the six real meter metrics with units, and includes a working example payload so an agent can discover and use Cost Meter queries.

### `signoz_execute_builder_query`
No new parameter needed. `QuerySpec.Source` captures `source` from within the user's spec object automatically through the typed round-trip.

## Files to Modify
- `pkg/types/querybuilder.go` — add field + update `BuildMetricsQueryPayloadJSON`
- `pkg/types/querybuilder_test.go` — round-trip test + direct build-path test
- `pkg/metricsrules/guide.go` — Cost Meter section + example in the metrics guide resource
- `internal/handler/tools/metrics_helper.go` — add field + parse
- `internal/handler/tools/metrics_query.go` — pass source through
- `internal/handler/tools/metrics.go` — add tool parameter

## Verification
```
go test ./pkg/types/... -v -run TestQueryPayload
go test ./internal/handler/tools/... -v
go test ./... 
```
