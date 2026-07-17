# Feature: Key-Not-Found Guidance — Context & Discussion

## Original Prompt
> Investigate this: https://github.com/SigNoz/nerve-pod/issues/148
> [after investigation] Actually, the reason behind this issue is that the agents assume there's a service.name present in logs, but which may not be the case every time, because it's not spec-compulsory and logs are kind of wild-west. Compared to traces which are much more spec, and logs are generally parsed, so it's also possible that logs are not parsed. And there are multiple reasons behind this issue, so there cannot be any backend fix for this. Obviously, the MCP can be updated to ensure we guide the agents better.
> Let's work on it, then run /codex 5.6 sol high review before creating PR

## Reference Links
- [nerve-pod#148 — key service.name not found spike on search_logs](https://github.com/SigNoz/nerve-pod/issues/148)
- [SigNoz search troubleshooting — key not found](https://signoz.io/docs/userguide/search-troubleshooting/)

## Key Decisions & Discussion Log

### 2026-07-17 — Investigation findings (Claude + Codex gpt-5.6-sol cross-verified)
- The MCP server does **no** field-context mapping on filter expressions; they pass verbatim to `/api/v5/query_range` (`logs_helper.go` → `pkg/types/querybuilder.go` → `QueryBuilderV5`).
- The ``key `X` not found`` error originates in the SigNoz backend's QB v5 key resolution (`pkg/querybuilder/key_resolution.go` upstream): the key is absent from the tenant's per-signal field metadata.
- Backend asymmetry (verified in the signoz repo at HEAD f24102c0): traces **synthesize** unknown keys and continue with a warning (`telemetrytraces/condition_builder.go`); logs **hard-error** unless the key is body-context (`telemetrylogs/condition_builder.go:400`). Same tenant state → 400 only on logs.
- The MCP server pushes agents into the failure: the `service` shortcut silently injects `service.name = '...'` (`logs_helper.go:76`), the `debug_service_errors` prompt hard-codes `search_logs service="..."` as step 1 (`pkg/prompts/prompts.go:66`), tool descriptions use `service.name` as the canonical logs example, and server-instructions rule 2 says "always prefer resource attributes".
- Newer backend error envelope (`errors.JSON`) carries the per-term detail in `error.errors[].message` (main message is just "Found N errors while parsing the search expression."); our `parseUpstreamErrorBody` only reads `error.message`, so on newer backends the actionable detail is dropped from tool error output.

### 2026-07-17 — Root cause per user (decision)
- Root cause: agents assume `service.name` exists on logs. It is not spec-compulsory for logs (unlike traces where SDKs mandate it); log pipelines may be unparsed or enrich with other attributes (`k8s.*`). Multiple tenant-side reasons → **no backend fix**; MCP must guide agents better.
- Issue #148 title/description updated to reflect this.
- Decision: implement MCP-side guidance only. Backend synthesis-for-logs recommendation dropped.

### 2026-07-17 — Implementation decisions
- Detect missing keys by regex ``key `X` not found`` over the raw 400 body — version-agnostic across old ("while parsing the search expression: key ... not found") and new ("Found N errors..." + `error.errors[]`) backend formats. Fail-open: no match → plain `upstreamError`, no enrichment.
- Enrichment lives in a signal-aware wrapper (`upstreamQueryError`) used by the five QB v5 passthrough tools (search/aggregate logs/traces, execute_builder_query); guidance text is signal-specific for logs.
- Missing keys surfaced in StructuredContent as `missingKeys` so clients can branch without string-matching.
- Also extract `error.errors[].message` in `parseUpstreamErrorBody` so the per-term detail reaches the error text on newer backends (benefits all tools, not just QB).
- Key-not-found 400s on QB tools log at WARN (with tool + missingKeys attrs), not ERROR — they are expected agent mistakes, consistent with #242's noise-reduction precedent; still always logged (fail open, never silent).
- Per CLAUDE.md contract-testing rules: the regex is contract-sensitive pattern matching on an upstream message; the WARN log doubles as the drift signal, and fixture tests cover both envelope generations.

### 2026-07-17 — query_metrics included
- `metrics_query.go` is the sixth QueryBuilderV5 caller (per the sibling-caller comment in `query_builder.go`); metric label filters can hit the same key-not-found. Included with signal "metrics" for uniform behavior even though the 7d evidence had no query_metrics rows. Zero-risk when the pattern is absent (identical to `upstreamError`).

### 2026-07-17 — Codex review (gpt-5.6-sol, high) — 0 blockers, 5 should-fix, 2 nits; all applied
- **Correction to the 2026-07-17 skills-check entry above:** `missingKeys` IS an additive output-contract change (new StructuredContent field on error results), so "payload shapes unchanged" was inaccurate. The no-skills-change conclusion still holds, but the correct rationale is that `signoz-generating-queries` already teaches signal-specific key discovery and defers error-shape detail to the MCP server; an additive error field doesn't alter the contract the skills teach.
- Envelope hardening: `error.errors[]` is now decoded best-effort via `json.RawMessage` (documented `[{"message"}]` shape + `[]string` fallback; anything else ignored), so a drifted detail array can never discard the independently parsed type/code/message. Regression-tested.
- Bounded extraction: regex scan capped at 64 matches, per-key length capped at 256 bytes, 10 distinct keys surfaced — the 400 body is upstream-controlled input.
- Prompt no longer implies a discovered key (e.g. k8s.deployment.name) takes the same value as the service name; it now routes through signoz_get_field_values.
- Handler-level table test added covering all six QueryBuilderV5 callers end to end (guidance noun + missingKeys per signal).
- Logs guide's first canonical example now uses guaranteed built-ins; the service.name example is annotated as conditional on discovery. README documents the `missingKeys` structured error field on all six QB tools.
- Nits fixed: general detail dedup (not just vs summary), singular/plural agreement in guidance.

## Open Questions
- [x] Should traces get the same description caveat as logs? — No; traces effectively guarantee `service.name` via SDKs. Traces still get the error-path enrichment (10 + 8 rows in the 7d evidence), with generic wording.
- [x] Does manifest.json need updating? — No; it stores only tool name + top-level description, and only parameter descriptions/error text change. README parameter references do get updated.
- [x] agent-skills impact? — None; the tool contract (names, params, payload shapes) is unchanged — this is additive error guidance + description wording. Outcome stated in PR summary.
