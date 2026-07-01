# Plan: Filter/Query Param Consistency

## Status
In Progress

## Context
The QB filter-expression parameter is named inconsistently across sibling MCP tools — `query` on
`signoz_search_logs`/`signoz_search_traces`, but `filter` on `signoz_aggregate_logs`/`signoz_aggregate_traces`/
`signoz_query_metrics`. Each handler reads only its one key, so when the model generalizes to the majority
name (`filter`, 4 visible occurrences vs 2) and sends it to a search tool, the key is silently dropped, the
filter expression resolves to `""`, and the backend returns the most recent N rows across the whole instance
unfiltered. The model then "gets confused filtering logs" (signoz-ai-assistant#315). Reviewed with Codex
(gpt-5.5/xhigh); see `.context.md`.

## Approach (layered)

### Layer 1 — Standardize on `filter`, alias legacy `query` (the bug fix)
Canonical name = **`filter`** (matches QB v5 `filter.expression`; already the majority; `query` is overloaded
by docs-search / full-QB-object / PromQL).

- Add one shared reader in `aggregate_helper.go` (or a small shared helper file):
  ```go
  // readFilterExpr returns the QB filter expression, accepting the canonical "filter" key and the
  // legacy "query" alias. If both are non-empty and differ (after TrimSpace), it returns an error so
  // the caller can fail loud (the silent-broadening bug we are fixing). Never logs the expression text.
  func readFilterExpr(args map[string]any) (string, error) {
      f := strings.TrimSpace(asString(args["filter"]))
      q := strings.TrimSpace(asString(args["query"]))
      if f != "" && q != "" && f != q {
          return "", fmt.Errorf("both 'filter' and 'query' were provided with different values; " +
              "use only 'filter' (the 'query' alias is legacy)")
      }
      if f != "" { return f, nil }
      return q, nil
  }
  ```
  (Return the raw, non-trimmed winner if trimming risks altering valid expressions — TBD during impl; trim
  only for the comparison. Decide in code review.)
- Route all 5 filter-expression parsers through `readFilterExpr`:
  - `parseSearchLogsArgs` (logs_helper.go:29) — currently reads `args["query"]`.
  - `parseSearchTracesArgs` (traces_helper.go:19) — currently reads `args["query"]`.
  - `parseAggregateLogsArgs` (logs_helper.go:12) — currently reads `args["filter"]`.
  - `parseAggregateTracesArgs` (traces_helper.go:60) — currently reads `args["filter"]`.
  - `parseMetricsQueryArgs` (metrics_helper.go:58) — currently reads `args["filter"]` (top-level only;
    do NOT touch nested `formulaQueries[].filter`).
- Schema changes: rename the advertised param `query` → `filter` on `search_logs` (logs.go:52) and
  `search_traces` (traces.go:57). Do NOT advertise `query` — it remains a silent, undocumented back-compat
  alias read by the handler. The other 3 tools already advertise `filter` (no schema change).
- **Do NOT alias / touch the false friends:** `list_alerts.filter` (alerts.go:152, Alertmanager matcher),
  `search_docs.query` (docs.go:60, BM25), `execute_builder_query.query` (query_builder.go:66, whole QB
  object), nested `compositeQuery` filters in create/update views, nested `formulaQueries[].filter`.

### Layer 2 — Never fail silent (guardrail)
- Conflict hard-error above is the primary loud signal.
- Surface backend warnings. Add an extractor (mirror `extractStepInterval`, metrics_helper.go:167) that
  decodes `data.warning.warnings[].message` from the QB v5 response (confirmed path via SigNoz backend
  `QueryRangeResponse.Warning` rendered under the success envelope). Then:
  - In the shared result wrappers (`rawSearchResult`/`aggregateResult`/`clampedResult`,
    aggregate_helper.go:203-225) compose the warning messages as an ADDITIONAL note block — preserve the raw
    JSON first and support multiple notes (clamp note + warnings), do not overwrite the existing clamp path.
  - Emit a `WarnContext` log when warnings are present (idiom: enrichSearchTracesWebURL, traces.go:230).
  - `query_metrics` already has a `[Decisions applied]` block (metrics_query.go:184/190) — merge backend
    warnings there.

### Layer 3 — Guidance so the model writes correct filters
- Replace the misleading "Supports any log field/attribute" wording (logs.go:52) with honest guidance:
  unknown keys hard-error; ambiguous keys default to `resource` context; disambiguate with `attribute.x` /
  `resource.x`; discover real keys first with `signoz_get_field_keys` / `signoz_get_field_values`. Apply to
  the logs + traces filter param descriptions.
- Add `pkg/querybuilder/logs_guide.go` — an HONEST, logs-specific guide (do not copy the traces guide
  wholesale): filter expression format, field contexts (resource vs attribute vs body), `body.` JSON-path
  search, `severity_text`, complete verified examples. Expose as resource `signoz://logs/query-builder-guide`
  and reference it by URI from the logs tool descriptions (same pattern as traces).
- Add a key-discovery nudge to the filter param description.

## Files to Modify
- `internal/handler/tools/aggregate_helper.go` — `readFilterExpr`, warning extractor, multi-note wrapper.
- `internal/handler/tools/logs_helper.go` — route search/aggregate logs filter reads through `readFilterExpr`.
- `internal/handler/tools/traces_helper.go` — route search/aggregate traces filter reads through it.
- `internal/handler/tools/metrics_helper.go` — route top-level metrics `filter` read through it.
- `internal/handler/tools/logs.go` — rename `query`→`filter` param (search), honest descriptions, logs-guide ref.
- `internal/handler/tools/traces.go` — rename `query`→`filter` param (search), honest description.
- `internal/handler/tools/metrics_query.go` — merge backend warnings into the decisions block.
- `pkg/querybuilder/logs_guide.go` — NEW logs query-builder guide.
- `internal/handler/tools/query_builder.go` (or wherever resources register) — register the logs guide resource.
- `README.md` — rows 613/651 (`query`→`filter`, note legacy alias); verify 600/669/415; document new resource.
- `manifest.json` — update tool descriptions only if wording changes (no per-param schema there).
- Tests — see Verification.

## Verification
- Parser unit tests (aggregate_helper_test.go style), table-driven over the 5 tools:
  `filter` only / legacy `query` only / both equal / both differ → assert `FilterExpression` and that
  both-differ returns an error.
- Regression handler tests: `search_logs` with `filter:` now filters (the exact reported call);
  `aggregate_logs` with `query:` now filters.
- False-friend guard tests: assert `list_alerts`, `search_docs`, `execute_builder_query`, view create/update,
  and `formulaQueries[].filter` are unaffected by the alias.
- Warning-surfacing test: fixture QB response containing `data.warning.warnings[].message`; assert it appears
  in the tool result note block + a WARN is logged.
- Live/integration (CLAUDE.md cross-contract mandate): via a subagent, run the reported call
  (`filter: "orgId = '…' AND testName = 'radio-group'"`) against a real instance and confirm the filter
  round-trips server-side; clean up nothing (read-only).

## Cross-repo: agent-skills (`~/signoz/agent-skills`)
Audited read-only (2026-06-23). The repo deliberately does NOT transcribe MCP schemas (CONTRIBUTING.md:31-32),
so only ONE skill drifts:
- `plugins/signoz/skills/signoz-generating-queries/SKILL.md` — rename the literal `query` param to `filter`
  at L104, L105, L124, L125 (the only place that names the param). Optionally add the honest field-context
  guidance there too.
- Optional: add a `signoz://logs/query-builder-guide` sibling next to the traces-guide references at
  signoz-creating-dashboards/SKILL.md:331, signoz-modifying-dashboards/SKILL.md:123,
  signoz-generating-queries/SKILL.md:108 (only after Layer 3 ships the resource).
- Everything else (false friends, generic "with filter:" prose) is correct as-is — do not touch.
- This is a SEPARATE PR in the agent-skills repo, coordinated with the MCP change.

## Out of Scope (file separately)
- jsonschema-go v0.4.2 ignoring `jsonschema_extras:"description=…"` on typed-struct tools (create/update
  alert/dashboard) — field docs silently dropped; `jsonschema:"required"` emits literal "required".
- Broader consistency: time-unit (ms vs ns), default-window (1h vs 6h), `search_docs.limit` being the only
  `WithNumber` limit, resource-id naming (`ruleId`/`uuid`/`viewId`/`id`).
