# Plan: Migrate `signoz_get_alert_history` to the v2 rule-history API

## Status
In Progress

## Context
The MCP server's `signoz_get_alert_history` tool still calls the v1 endpoint
`POST /api/v1/rules/{id}/history/timeline` with a JSON body, even though rule
CRUD already moved to `/api/v2/rules/*` in the earlier `v2-convention-migration`
work. That work explicitly deferred history because the v2 timeline route did
not exist yet. It now does: upstream serves the full rule-history surface via
`signozapiserver` at `GET /api/v2/rules/{id}/history/timeline`. This plan brings
the one wrapped endpoint (`timeline`) onto v2.

Both v1 and v2 wrap responses in the same `{"status":"success","data":...}`
envelope, and the MCP tool returns that body as a **raw passthrough** — so the
migration is contained to how the request is built (method + params) and how the
completeness/pagination note is derived. There is no typed response struct to
rewrite.

**Codegen / skaff:** not used. skaff generates a Terraform plugin-framework
provider (custom types, TF schemas, oapi-codegen typed client, expand/flatten
convertors) for `terraform-provider-signoz`; it is architecturally mismatched
with this repo's hand-written raw-passthrough client and is absent here. This
one-endpoint change is written by hand from `~/Stash/signoz/docs/api/openapi.yml`,
exactly like every other method on the client. (Rationale in `.context.md`.)

## Approach

### 1. Client layer — `internal/client/client.go`
Rewrite `GetAlertHistory` from POST+body to GET+query string, v1→v2 path:

```go
func (s *SigNoz) GetAlertHistory(ctx context.Context, ruleID string, req types.AlertHistoryRequest) (json.RawMessage, error) {
    reqURL := fmt.Sprintf("%s/api/v2/rules/%s/history/timeline?%s",
        s.baseURL, url.PathEscape(ruleID), req.QueryParams().Encode())
    s.logger.DebugContext(s.ensureTenantContext(ctx), "Fetching alert history", slog.String("ruleID", ruleID))
    return s.doRequest(ctx, http.MethodGet, reqURL, nil, DefaultQueryTimeout)
}
```

- Interface signature (`interface.go:19`) and mock (`mock.go:97`) are **unchanged**
  — still `GetAlertHistory(ctx, ruleID, req types.AlertHistoryRequest)` — so churn
  there is zero; only the struct's internals change.
- Update the base-URL comment at `client.go:117` if it enumerates the history path
  version.

### 2. Request type — `pkg/types/alerts.go`
Reshape `AlertHistoryRequest` to the v2 query params and give it a
`QueryParams() url.Values` method (mirroring the existing `ListAlertsParams`
pattern in the same file):

```go
type AlertHistoryRequest struct {
    Start            int64
    End              int64
    State            string // "" omitted; one of inactive|pending|firing|nodata|disabled
    FilterExpression string // "" omitted; v5 query-builder expression
    Limit            int
    Order            string // "asc" | "desc"
    Cursor           string // "" omitted; opaque base64 from a prior response's nextCursor
}

func (r AlertHistoryRequest) QueryParams() url.Values {
    v := url.Values{}
    v.Set("start", strconv.FormatInt(r.Start, 10))
    v.Set("end", strconv.FormatInt(r.End, 10))
    if r.State != "" { v.Set("state", r.State) }
    if r.FilterExpression != "" { v.Set("filterExpression", r.FilterExpression) }
    if r.Limit > 0 { v.Set("limit", strconv.Itoa(r.Limit)) }
    if r.Order != "" { v.Set("order", r.Order) }
    if r.Cursor != "" { v.Set("cursor", r.Cursor) }
    return v
}
```

- Remove `AlertHistoryFilters` (the `{items,op}` struct) and the `Offset` field —
  both are gone in v2. Grep for other references before deleting.

### 3. Handler — `internal/handler/tools/alerts.go` (`handleGetAlertHistory` + tool registration)
Tool input-schema changes (registration at `alerts.go:80`):
- **Drop** `offset` (no raw offset in v2).
- **Add** `cursor` (string, optional): "Opaque pagination cursor. Pass the `nextCursor` value from a previous response's `data` to fetch the next page. Omit for the first page."
- **Add** `filterExpression` (string, optional): "SigNoz v5 query-builder filter expression to narrow the timeline (e.g. `severity = 'critical'`). Optional."
- Keep `searchContext`, `id`, `timeRange`, `start`, `end`, `state`, `limit`, `order`.
- `state` enum stays `firing`/`inactive` (decided). A later additive expansion would use the exact v2 spellings incl. `nodata`.
- Refresh the `WithDescription` text to say v2 / cursor pagination.

Handler body changes:
- Delete the `paginate.ParseParams` offset read (line ~374) and the `Offset` field
  in the built request. Read `cursor` and `filterExpression` from args instead.
- Keep the existing `timeutil` start/end defaulting (6h), `ValidateExplicitTimestamps`,
  `intArg`/`clampLimit` limit handling (still clamp to `MaxRawResultLimit` before
  forwarding — the v2 server default is 50 with no documented hard cap, so the
  memory guard stays our responsibility), and `order`/`state` validation.
- Build `types.AlertHistoryRequest` with the new fields; call `client.GetAlertHistory`;
  on error return `upstreamError(err)` (unchanged — the shared coded-error path already
  handles the v2 `{"status","error"}` envelope via `HTTPStatusError`).

### 4. Completeness / pagination note
v2 returns `data.nextCursor` directly — more reliable than inferring `hasMore`
from row counts against an offset. Add a small cursor-aware note helper (next to
`completenessNote` in `aggregate_helper.go`) that reads `nextCursor` from the
passthrough body:

- If `data.nextCursor` is a non-empty string → `hasMore=true`; advise "fetch the
  next page by passing cursor=\"<nextCursor>\"".
- Else → "all matching results returned (hasMore=false)".
- Keep `countAlertHistoryRows` for the row count in the message — it already
  handles the `data.items[]` shape, which is exactly the v2 timeline shape, so no
  change is needed there. Retain the existing limit-clamp advisory note.

This replaces the current `completenessNote(returnedRows, limit, offset, rowsKnown)`
call for this handler only (offset-based note stays in use by other tools).

### 5. Alert-summary resource — `internal/handler/tools/resource_templates.go`
`handleAlertSummaryResource` (line ~61) builds an `AlertHistoryRequest{Start, End,
Order:"desc", Limit:10, Offset:0}`. Drop the `Offset:0` field (removed from the
struct); the rest is compatible as-is. Behavior (last 10 transitions, desc) is
unchanged.

### 6. Docs & metadata (CLAUDE.md sync checklist)
- `manifest.json` (entry at line ~69): update the description to reflect v2 +
  cursor pagination + optional `filterExpression`.
- `README.md`: tool-table row (line ~353) and the full parameter section
  (lines ~558-571) — document `cursor` (replaces `offset`), `filterExpression`,
  the v2 path, and the `data.{items,total,nextCursor}` response shape. Note the
  minimum SigNoz version once known.
- **searchContext:** the tool already exposes a top-level `searchContext` string;
  keep it (do not add to `required`).
- **agent-skills check:** no shipped skill teaches the alert-history parameter
  surface (history is read-only; `signoz-creating-alerts`/`-dashboards` cover
  create/update contracts only). Expected outcome: **no agent-skills change
  needed** — state this in the PR summary and re-verify at PR time.
- **Version compatibility:** v2-only on the MCP side (hard cutover, consistent
  with the rule-CRUD migration). Document the minimum SigNoz version that ships
  the `signozapiserver` rule-history routes.

### 7. Tests
- `internal/client/client_test.go`: add a case asserting `GetAlertHistory` issues
  a **GET** to `/api/v2/rules/{id}/history/timeline` with the expected query string
  (start/end/state/limit/order/cursor/filterExpression), and that the `{status,
  data:{items,total,nextCursor}}` envelope round-trips as a raw body.
- `internal/handler/tools/alert_history_limit_test.go`: the mock returns
  `{"data":{"items":[]}}`, which is still a valid v2 timeline body — the limit-clamp
  assertions hold (`capturedReq.Limit` still exists). Adjust only if field renames
  touch it.
- Add handler coverage: (a) `cursor` is forwarded verbatim; (b) `filterExpression`
  is forwarded; (c) a response with a non-empty `data.nextCursor` produces a
  `hasMore=true` note naming the cursor; (d) `offset` is no longer read (a passed
  `offset` is ignored rather than paginating).
- Grep-and-fix any test that constructs `AlertHistoryRequest` with `Offset`/`Filters`
  or asserts the v1 POST/path (`silent_failures_test.go`, `e2e_familya_test.go`
  exercise `countAlertHistoryRows` against `data.items[]`/`data[]` — those shapes are
  unchanged, so they should keep passing; verify).

## Files to Modify
- `internal/client/client.go` — `GetAlertHistory`: POST→GET, v1→v2 path, body→query; base-URL comment.
- `pkg/types/alerts.go` — reshape `AlertHistoryRequest` (drop `Offset`/`Filters`, add `FilterExpression`/`Cursor`), add `QueryParams()`; remove `AlertHistoryFilters`.
- `internal/handler/tools/alerts.go` — tool schema (drop `offset`, add `cursor`+`filterExpression`, refreshed description) and handler wiring.
- `internal/handler/tools/aggregate_helper.go` — add a cursor-aware completeness note helper (keep `countAlertHistoryRows`).
- `internal/handler/tools/resource_templates.go` — drop `Offset:0` in `handleAlertSummaryResource`.
- `internal/client/client_test.go`, `internal/handler/tools/alert_history_limit_test.go` (+ new handler test cases) — v2 GET/query/cursor coverage.
- `manifest.json`, `README.md` — v2 path, `cursor`, `filterExpression`, response shape, min version.
- `internal/client/interface.go`, `internal/client/mock.go` — **no change** (signature stable); listed for completeness.

## Verification
1. `go build ./...` and `go vet ./...`.
2. `go test ./...` — client GET/query test, handler cursor/filterExpression/nextCursor tests, and unchanged `countAlertHistoryRows` coverage all pass.
3. Live smoke against a SigNoz new enough to serve the v2 routes — **delegate to a subagent** per CLAUDE.md (no credentials printed/persisted; report which fields round-tripped):
   - `signoz_get_alert_history` with a real rule `id` and `timeRange="24h"` → expect a `{status,data:{items,total}}` body; items carry `ruleId`/`state`/`unixMilli`.
   - Set a small `limit` on a busy rule → response has `data.nextCursor`; pass it back as `cursor` → next page returns, note reports `hasMore`.
   - `state="firing"` filters to firing transitions; a bad `state` is rejected client-side with `CodeValidationFailed`.
   - `signoz://alert/{id}/summary` resource still returns `recentHistory`.
4. Confirm no lingering references to the v1 path or the removed `Offset`/`Filters` fields: `grep -rn "history/timeline\|AlertHistoryFilters\|\.Offset" internal pkg` comes back clean (except intended v2 usages).
