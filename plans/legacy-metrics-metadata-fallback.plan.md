# Plan: Legacy Metrics Metadata Fallback

## Status
Done

## Context
Xylem runs SigNoz `v0.108.0`. The MCP server's current metric catalog path, `/api/v2/metrics`, is available on newer SigNoz versions but routes to the frontend SPA HTML on Xylem. That breaks `signoz_list_metrics` and also breaks `signoz_query_metrics` when metric metadata is not provided manually.

## Approach
When `ListMetrics` receives HTML from `/api/v2/metrics` and the caller supplied an exact `searchText`, call the legacy exact metadata endpoint:

```text
/api/v2/metrics/metadata?metricName=<metric>
```

Then synthesize the existing list-metrics response shape:

```json
{"status":"success","data":{"metrics":[{"metricName":"...","type":"...","temporality":"...","isMonotonic":false}]}}
```

This keeps downstream parsing unchanged and limits the compatibility behavior to exact metric metadata lookups. Broad catalog listing remains unsupported on this older deployment.

## Files Modified
- `internal/client/client.go` - detect HTML from the newer catalog route and fall back to legacy exact metric metadata.
- `internal/client/metrics_compat_test.go` - regression test for v0.108-style HTML catalog response plus working legacy metadata endpoint.

## Verification
- `go test ./internal/client -run TestListMetrics_FallsBackToLegacyMetadataWhenV2CatalogReturnsHTML -count=1`
- `go test ./internal/client -count=1`
- `go test ./...`
- `go build -ldflags "...Version=$(git describe --tags --always --dirty)" -o bin/signoz-mcp-server ./cmd/server/...`
