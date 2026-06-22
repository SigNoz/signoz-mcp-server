# Plan: Resource Web URLs

## Status
Done

## Context
The AI assistant (SigNoz/signoz-ai-assistant#245) needs an absolute deep link per
resource so it can hand users a clickable link into the SigNoz web UI. Building the
link in the MCP server keeps a single source of truth and benefits all MCP clients
(including Cursor / Claude Desktop, which are not same-origin as the SigNoz
instance).

This adds a `webUrl` deep-link field to the dashboard, alert, service, and trace
resource read-tool outputs.

## Approach

### `pkg/util/weburl.go`
- `ResourceWebURL(base, resourceType, id string) (string, bool)`.
- Supports `dashboard`, `alert`, `service`, `trace`; returns `("", false)` on an
  empty base/id or an unknown type so callers omit the field rather than emit a
  broken link.
- Templates: `/dashboard/<uuid>`, `/alerts/overview?ruleId=<id>`,
  `/services/<url-encoded-name>`, `/trace/<traceId>`. Path/query segments are
  URL-encoded; the base origin is trimmed of a trailing slash.
- `InjectWebURL(data, base, type, id) []byte` for passthrough bodies: shallow
  decode into `map[string]json.RawMessage` — one level deep, two for the
  `{"data":{…}}` wrap — so nested values (span trees, int64 fields, number
  formatting, key order) pass through as verbatim bytes with no re-encoding.
  Fails open on any non-object or unparseable body, including literal `null`.
- `InjectRowsWebURL(data, base, type, idKey) []byte` for v5 raw list bodies:
  walks `data.data.results[].rows[]` and injects a per-row `webUrl` built from
  `rows[].data[idKey]`, with the same shallow-decode guarantees. Whole-body
  fail-open on shape mismatch / empty base; per-row best-effort skips a row whose
  id is missing/empty/non-string rather than dropping links for the whole result.

### `internal/handler/tools/dashboards.go`
- Enrich list items in `handleListDashboards`.
- Enrich the single-get body in `handleGetDashboard` via `enrichDashboardWebURL`.

### `internal/handler/tools/services.go`
- Enrich list items in `handleListServices` using the service name as the id.

### `internal/handler/tools/alerts.go` + `pkg/types/alerts.go`
- Add a `WebURL` field on `Alert` / `AlertRuleSummary` (`json:"webUrl,omitempty"`).
- Enrich `handleListAlerts`, `handleListAlertRules`, and the `handleGetAlert`
  passthrough (`enrichAlertWebURL`).

### `internal/handler/tools/traces.go`
- Enrich the `handleGetTraceDetails` passthrough body via `enrichTraceWebURL`
  (wrapped `{"data":{…}}` + bare, fail-open), using the `traceId` arg.
- Enrich the `handleSearchTraces` passthrough body via `enrichSearchTracesWebURL`
  (delegates to `util.InjectRowsWebURL`), injecting a per-row `webUrl` into each
  v5 raw result row at `data.data.results[].rows[].data.traceID`. The `traceID`
  row-data key is stable: the trace field-mapper aliases each selected column to
  its `field.Name`, so the `traceID` select field always yields a `traceID` key.
  Per-row best-effort + whole-body fail-open keep a malformed row from corrupting
  the response.

### Omission rule
- `webUrl` is omitted when `util.GetSigNozURL(ctx)` is empty (no instance URL on the
  request).

## Files to Modify
- `pkg/util/weburl.go` — new `ResourceWebURL` deep-link builder
- `pkg/util/weburl_test.go` — builder unit tests (incl. trailing-slash trim)
- `internal/handler/tools/dashboards.go` — enrich list + single-get
- `internal/handler/tools/services.go` — enrich list
- `internal/handler/tools/alerts.go` — enrich list/list-rules/get
- `pkg/types/alerts.go` — `WebURL` field on `Alert` / `AlertRuleSummary`
- `internal/handler/tools/traces.go` — enrich `get_trace_details` + `search_traces`
- `README.md` — note on the `webUrl` output field
- `plans/resource-web-urls.*` — this pair

`manifest.json` intentionally unchanged: it documents tool `name`/`description`
only (not output shapes), and no tool descriptions changed.

## Verification
```
go build ./...
go test ./...
go vet ./...
gofmt -l pkg/util/weburl.go internal/handler/tools/dashboards.go \
  internal/handler/tools/services.go internal/handler/tools/alerts.go \
  pkg/types/alerts.go
```
- Unit: `ResourceWebURL` builds each template, URL-encodes ids, trims trailing
  slash, and returns `ok=false` on empty base/id or unknown type.
- Handler: list/get outputs carry `webUrl` when the request has an instance URL and
  omit it when the base is empty.
