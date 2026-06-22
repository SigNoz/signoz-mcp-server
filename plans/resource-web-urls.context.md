# Feature: Resource Web URLs — Context & Discussion

## Original Prompt
> Add an absolute deep-link (`webUrl`) field to the resource read-tool outputs so
> the AI assistant can hand users a clickable link to a dashboard, alert, or
> service in the SigNoz web UI.

## Reference Links
- Issue: [SigNoz/signoz-ai-assistant#245](https://github.com/SigNoz/signoz-ai-assistant/issues/245) (resource links)

## Key Decisions & Discussion Log
### 2026-06-10 — why and where the link is built
- The AI assistant needs an absolute deep link per resource so it can hand users a
  clickable link.
- Decision: build the link in the MCP server (single source of truth; benefits all
  MCP clients incl. Cursor/Claude Desktop which are not same-origin as SigNoz).
- Scope: dashboard, alert, service (single-id). Saved views dropped — they require
  the full encoded `compositeQuery` in the URL; there is no id-only frontend route.
- Templates:
  - `/dashboard/<uuid>`
  - `/alerts/overview?ruleId=<id>`
  - `/services/<url-encoded-name>`
- Origin comes from `util.GetSigNozURL(ctx)`; `webUrl` is omitted on an empty base.
- Helper: `pkg/util/weburl.go` `ResourceWebURL(base, type, id) -> (url, ok)`.

### 2026-06-10 — docs sync (Task A5)
- Added this plan-file pair and a README note under "Available Tools" documenting
  the new `webUrl` output field on the six resource read tools.
- `manifest.json` left unchanged: it documents tool `name`/`description` only (not
  output shapes), and no tool descriptions changed in A1–A4 — only outputs gained a
  field.

### 2026-06-10 — drop inert `tab=AlertRules` from alert webUrl (#245)
- Alert template changed from `/alerts/overview?ruleId=<id>&tab=AlertRules` to
  `/alerts/overview?ruleId=<id>`. The `tab=AlertRules` param is inert on the
  `/alerts/overview` route — that page uses `AlertDetailsTab` (OVERVIEW/HISTORY);
  `AlertRules` is a tab on the `/alerts` list page, not the overview detail page.
  Removed it so the deep link is clean.

### 2026-06-10 — add trace deep link (#245)
- Added a fourth single-id type: `trace` → `/trace/<traceId>`. Verified against a
  live staging instance (`https://<base>/trace/<trace_id>`).
- Enriches `signoz_get_trace_details` (passthrough body → `enrichTraceWebURL`,
  wrapped `{"data":{…}}` + bare, fail-open). `search_traces` is NOT enriched: its
  rows put `traceId` in dynamic query columns, not a stable top-level key, so
  per-row enrichment is fragile — `get_trace_details` is the deep-link use case.
- Frontend companion adds a `trace` case to `ResourceType`/`resourceRoute()` so the
  `open_resource` chip routes to `/trace/:id`.

### 2026-06-11 — InjectWebURL rewritten to shallow RawMessage decode (#245)
- Design review compared the webUrl approach against alternatives (MCP
  `resource_link` content blocks, `structuredContent`, client-side route
  construction, a `get_resource_url` tool, `_meta`). Kept in-band `webUrl`: it is
  the only option visible to the LLM in every MCP host. One implementation
  improvement accepted: stop full-tree decoding passthrough bodies.
- `InjectWebURL` now decodes one level deep into `map[string]json.RawMessage`
  (two levels for the `{"data":{…}}` wrap). Everything below the injection level
  passes through as verbatim bytes, so:
  - int64 precision is preserved by construction — the `UseNumber` guard became
    unnecessary and was removed;
  - nested key order / number formatting are untouched;
  - multi-MiB trace bodies (response cap is 64 MiB) no longer pay the ~3–5×
    allocation cost of a full `map[string]any` tree decode + re-marshal.
- Fixed a latent panic found during the rewrite: a literal `null` body decoded
  to a nil map and the subsequent `obj["webUrl"]=` write panicked
  (`assignment to entry in nil map`). Now fails open. New tests cover verbatim
  inner bytes, `null` body, `"data":null`, and `"data":[…]`.
- [x] Should saved views get a `webUrl`? — No; no id-only frontend route exists
  (the URL requires the full encoded `compositeQuery`).
- [x] Where should the origin come from? — `util.GetSigNozURL(ctx)`; omit `webUrl`
  when empty.
- [x] Should traces get a `webUrl`? — Yes; `/trace/<traceId>` is a clean single-id
  route. Scoped to `get_trace_details` (not `search_traces`).

### 2026-06-19 — enrich search_traces with per-row webUrl (#245, signoz-mcp-server#206)
- Reverses the earlier "`search_traces` is NOT enriched" decision (2026-06-10
  trace entry above; append-only, so that entry stays as the record of why we
  initially scoped it out).
- The original concern was that `traceId` lived in "dynamic query columns, not a
  stable top-level key." On inspection the key IS stable: the v5 raw response
  nests as `data.data.results[].rows[].data.traceID`, and the trace field-mapper
  aliases each selected column to its `field.Name`, so the `traceID` select
  field always yields a `traceID` row-data key. Confirmed against the SigNoz
  backend: `querybuildertypesv5/resp.go` (`QueryRangeResponse`/`RawData`/`RawRow`),
  `querier/consume.go` (`readAsRaw`), `telemetrytraces/field_mapper.go`
  (`ColumnExpressionFor`), `http/render/render.go` (`Success` envelope).
- Why now: the AI assistant only links a `webUrl` a tool returned and never
  hand-builds URLs, so the common trace path (`search_traces` → "show me slow /
  error traces") produced no clickable links; `get_trace_details` alone was
  insufficient.
- Implementation: new `util.InjectRowsWebURL(data, base, type, idKey)` walks the
  rows and injects a per-row `webUrl` sibling, reusing `ResourceWebURL`. Same
  shallow-RawMessage decode as `InjectWebURL` (preserves `durationNano` int64
  precision, sibling bytes, and key order). Whole-body fail-open on shape
  mismatch / empty base; per-row best-effort skips a row whose id is
  missing/empty/non-string rather than dropping links for the whole result.
  Wired via `enrichSearchTracesWebURL` in `handleSearchTraces`.
- [x] Should `search_traces` rows get a `webUrl`? — Yes (reverses 2026-06-10);
  the `traceID` row-data key is stable and enrichment fails open per row.
- [x] Should `search_logs` get the same? — No; logs have no single-resource deep
  link in `ResourceWebURL`, so there is nothing to inject.
