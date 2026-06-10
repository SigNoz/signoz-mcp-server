# Plan: Resource Web URLs

## Status
Done

## Context
The AI assistant (SigNoz/signoz-ai-assistant#245) needs an absolute deep link per
resource so it can hand users a clickable link into the SigNoz web UI. Building the
link in the MCP server keeps a single source of truth and benefits all MCP clients
(including Cursor / Claude Desktop, which are not same-origin as the SigNoz
instance).

This adds a `webUrl` deep-link field to the dashboard, alert, and service resource
read-tool outputs.

## Approach

### `pkg/util/weburl.go`
- `ResourceWebURL(base, resourceType, id string) (string, bool)`.
- Supports `dashboard`, `alert`, `service`; returns `("", false)` on an empty
  base/id or an unknown type so callers omit the field rather than emit a broken
  link.
- Templates: `/dashboard/<uuid>`, `/alerts/overview?ruleId=<id>`,
  `/services/<url-encoded-name>`. Path/query segments are URL-encoded; the base
  origin is trimmed of a trailing slash.

### `internal/handler/tools/dashboards.go`
- Enrich list items in `handleListDashboards`.
- Enrich the single-get body in `handleGetDashboard` via `enrichDashboardWebURL`.

### `internal/handler/tools/services.go`
- Enrich list items in `handleListServices` using the service name as the id.

### `internal/handler/tools/alerts.go` + `pkg/types/alerts.go`
- Add a `WebURL` field on `Alert` / `AlertRuleSummary` (`json:"webUrl,omitempty"`).
- Enrich `handleListAlerts`, `handleListAlertRules`, and the `handleGetAlert`
  passthrough (`enrichAlertWebURL`).

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
