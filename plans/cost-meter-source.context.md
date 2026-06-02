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
