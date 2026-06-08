# Feature: Cost Meter `source` Parameter — Context & Discussion

## Original Prompt
> Gap 1 — Adding source to query payloads
> Act as senior SDE 3, and implement this. once done, run the tests to see if everything works as expected

## Reference Links
- [GitHub Issue #176](https://github.com/SigNoz/signoz-mcp-server/issues/176)

## Key Decisions & Discussion Log

### 2026-06-02 — Implementation approach

- `source` is already a first-class concept in the codebase (`signoz_list_metrics`, `signoz_get_field_keys`, `signoz_get_field_values` all carry it). Extending it to query tools is consistent.
- `signoz_execute_builder_query` takes the full query payload as a structured object. Adding `Source string json:"source,omitempty"` to `QueryPayload` is sufficient — the field round-trips automatically through unmarshal → validate → marshal.
- `signoz_query_metrics` builds its payload internally via `BuildMetricsQueryPayloadJSON` (anonymous inline struct). Added `source string` param to that function rather than doing a post-build round-trip to avoid touching the validated JSON unnecessarily.
- `omitempty` tag is mandatory — existing round-trip tests must not regress, and omitting the field when empty matches the existing pattern for all optional fields in `QuerySpec` and siblings.
- No changes to `BuildMetricsQueryPayload` (non-JSON variant) since it returns `*QueryPayload` and callers can set `.Source` directly. Only the handler path via `BuildMetricsQueryPayloadJSON` needs the change.

## Open Questions
- [x] Does `signoz_execute_builder_query` need a new top-level parameter? **No** — user includes `source` inside each spec object; `QuerySpec.Source` captures it automatically through the typed round-trip.

### 2026-06-02 — Correction after live testing

Initial implementation placed `Source` on `QueryPayload` (top-level envelope). Live end-to-end testing against the SigNoz demo instance revealed the API rejected this with "unknown field source" — the field does not exist at the envelope level.

Inspection of the actual API payload (snapshot.py output) confirmed `source` is a sibling of `name` and `signal` inside the `spec` object of each `builder_query` entry in `compositeQuery.queries`.

Fix: moved `Source string json:"source,omitempty"` from `QueryPayload` to `QuerySpec`, and updated `BuildMetricsQueryPayloadJSON` to set `spec.Source = source` on each builder spec instead of the payload struct. The round-trip test was also corrected to assert the field at spec level.

Verified working after the fix:
- `signoz_list_metrics` with `source=meter` ✅ — returns all 6 Cost Meter metrics
- `signoz_execute_builder_query` with `"source":"meter"` inside spec ✅ — real data vs null without source
- `signoz_query_metrics` with `source=meter` ✅ — works including groupBy breakdowns

### 2026-06-08 — Review follow-up: direct test + agent-facing guide example

Review of the PR surfaced two gaps, both addressed here:

- **Test coverage.** The existing `TestQueryPayloadRoundTrip_PreservesSource` only exercises the `signoz_execute_builder_query` path (unmarshal → validate → marshal of a hand-written payload). The `signoz_query_metrics` path goes through `BuildMetricsQueryPayloadJSON` — the function that actually gained the `source` argument — and had no direct test. Added `TestBuildMetricsQueryPayloadJSON_AppliesSource`, asserting source lands on every `builder_query` spec, never on a `builder_formula` spec, and is omitted when empty.
- **Discoverability.** The `source` parameter was documented in the README param reference, but the `signoz://metrics-aggregation-guide` MCP resource (`pkg/metricsrules/guide.go`) — the agent-facing guide with payload examples — had no mention of Cost Meter. An agent reading it would never learn to set `source: "meter"`. Added a "Cost Meter (Telemetry Ingestion Volume)" section with the six real meter metric names/units (verified live via `signoz_list_metrics source=meter`: all delta monotonic sums) and a working example payload.

The six Cost Meter metrics (verified live): `signoz.meter.log.count`/`.size`, `signoz.meter.metric.datapoint.count`/`.size`, `signoz.meter.span.count`/`.size`.

### 2026-06-08 — E2E verification against staging + hourly-step caveat

Ran the guide's example payload against `app.us.staging.signoz.cloud` `POST /api/v5/query_range` (the exact path the MCP server uses):
- `source="meter"` + `signoz.meter.log.size` + `increase`/`sum` → real data (~55 MB/h log ingestion). `rate` variant also valid (~16 KB/s).
- Control without `source` → HTTP 200 but `"aggregations":null` — the same metric name returns nothing from the default store. Confirms `source` is load-bearing.

Step-interval sensitivity (this is the observed staging behavior; production/older versions may differ): the v5 endpoint **floors `stepInterval` to 3600 for meter queries** regardless of the requested value (every step of 60/300/1800/3600 returned identical hourly data with `"stepIntervals":{"A":3600}`). A query window under 1 hour returns a single current-hour bucket flagged `partial:true`, not an empty result. Documented this in the guide as a "Hourly aggregation — use `stepInterval: 3600`" subsection, noting that `signoz_query_metrics`'s auto-derived step (`max(60, window/300)`) is sub-hour for windows under ~12.5 days, so meter callers should set `stepInterval: 3600` explicitly.

### 2026-06-08 — Decision: no client-side step coercion; discoverability fixes

- **Q: Should the MCP server floor `stepInterval` to 3600 for meter queries (in `signoz_query_metrics` and/or `QueryPayload.Validate`)?** **Resolved: No.** The backend is the authority on its own rollup granularity and already floors the step (verified on staging); duplicating "meter = 3600" in the thin proxy is fragile coupling and silent rewriting of caller intent. If a backend version returns empty for sub-hour meter steps instead of flooring, that is a backend normalization issue, not something to paper over per-client. Keep it documentation-only (the guide's hourly subsection is caller guidance).
- **Discoverability gap closed.** `signoz_list_metrics` is the recommended first step (`signoz_query_metrics` tells the LLM to "call signoz_list_metrics first"), but its `source` param read only "Filter by source (optional)" — blind to `meter`. Updated the `source` description on `signoz_list_metrics` (and README param ref) to name `"meter"`, mirroring `signoz_query_metrics`. Added a sentence to the guide's Cost Meter intro clarifying that log/span/datapoint volume is queried as metrics (`signal: "metrics"`, `source: "meter"`), not via the logs/traces tools.
- **Tools are otherwise sufficient** to query Cost Meter: `signoz_list_metrics` (discover) → `signoz_query_metrics` / `signoz_execute_builder_query` (query), all `source`-aware. Issue #176 Gaps 2 & 3 (`signoz_get_metrics_stats`, `signoz_get_metric_attributes`) are adjacent metrics-introspection tools, not required for meter querying.

### 2026-06-08 — E2E verification of signoz_list_metrics for meter (staging)

Verified the `signoz_list_metrics` path directly against staging (`GET /api/v2/metrics`, the exact endpoint the tool builds):
- `?source=meter` → HTTP 200, returns the current 6 meter metrics with type/temporality/unit.
- Control without `source` (`searchText=signoz.meter`) → 0 results — meter metrics are invisible in the default store, confirming `source=meter` is required for discovery.

### 2026-06-08 — Concept correction + drop hardcoded metric list

- **"Cost Meter" is broader than telemetry ingestion volume.** It's the store for the metrics SigNoz *meters/bills on*; ingestion volume is today's content, but the name is deliberately general and the set will grow (e.g. AI credit usage). Reframed the guide section heading ("Cost Meter (SigNoz usage / billing metrics)") and intro, and softened the `signoz_list_metrics` `source` param description (tool + README) so it no longer equates meter with ingestion volume.
- **Q: Should the guide enumerate all meter metric names (they're fetchable live via `signoz_list_metrics`)?** **Resolved: No.** The set is live-queryable and evolving, so a hardcoded table (especially a `unit` column) is a staleness trap — it already went stale once (span units read `—` from an earlier instance; staging reports `1`/`By`). Replaced the fixed "Available Cost Meter metrics" table with a "Discover the current meter metrics" subsection that tells the agent to call `signoz_list_metrics source=meter` for the authoritative set + types/units, keeping only a few metrics as illustrative (explicitly "as of this writing"). The per-type aggregation guidance (counters → rate/increase + sum) is framed as applying to the current ingestion metrics, not assumed for future meter metrics.
