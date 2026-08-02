# Feature: Data Retention Tool — Context & Discussion

## Original Prompt
> Let's work on this: https://github.com/SigNoz/nerve-pod/issues/134

## Reference Links
- [nerve-pod issue #134 — Create a fetch retention/ttl tool](https://github.com/SigNoz/nerve-pod/issues/134)
- [agent-skills PR #61 review that motivated the tool](https://github.com/SigNoz/agent-skills/pull/61#pullrequestreview-4669018014)
- [SigNoz retention-period documentation](https://signoz.io/docs/userguide/retention-period/)

## Key Decisions & Discussion Log
### 2026-08-01 — Initial contract and upstream API audit
- Name the agent-facing tool `signoz_get_data_retention`; reserve `fetch` for document retrieval and avoid backend `ttl` vocabulary in the tool name.
- Return metrics, traces, and logs together because the motivating cost workflow needs one cross-signal retention snapshot and separate signal calls would be predictable round trips.
- Read metrics and traces from the signal-specific v1 retention endpoint. Read logs from the v2 retention endpoint because it supports custom log-retention rules and internally falls back to v1 on legacy schemas.
- Do not use the v1 logs endpoint directly when custom retention is enabled: the current backend parser only recognizes numeric `toIntervalSecond(...)` TTL expressions, while custom retention uses `_retention_days` and `toIntervalDay(...)`, causing a misleading `-1` result.
- Own a stable MCP output envelope in hours. Include configured/default retention for all signals, change status and pending targets when available, cold-storage move thresholds, and custom log-retention overrides.
- The tool is read-only and all-or-nothing. Any upstream authentication, authorization, transport, or parse failure is a top-level coded error rather than a partial signal result.
- Additive server work is not itself a breaking contract change, but the companion `SigNoz/agent-skills` cost-reduction skill should adopt this tool in a follow-up because issue #134 originated from that workflow.

## Open Questions
- [x] Which backend endpoints should the tool use? Metrics/traces use v1; logs use v2 because it is custom-retention-aware and legacy-compatible.
- [x] Should the tool expose one signal per call? No; return all three signals in one read.
- [x] Should custom log-retention rules be omitted? No; omitting them could misrepresent the effective log policy as one workspace-wide period.

## Discussion Log (continued)

### 2026-08-01 — Preserve exact legacy log retention
- A source-level check found that the v2 compatibility response converts legacy log retention from hours to days with integer division. When v2 reports `version: "v1"`, read the v1 logs endpoint as a second step so non-day-aligned policies such as 25 hours are not rounded down to 24 hours.
- Continue using the v2 endpoint first: its version discriminator is what safely distinguishes legacy table TTLs from custom log retention, where the v1 parser can return a misleading value.
- Treat only `-1`, zero, or omission as disabled cold-storage movement; reject more-negative values as upstream contract drift and emit the existing WARN signal.

### 2026-08-01 — Retention scope and external-contract fixture
- Make the newly-ingested-data boundary explicit in the tool and output schemas: after a setting change, older data can retain its prior TTL, so the returned snapshot must not be treated as the policy for every stored row.
- Point ingestion-volume and cost questions to the exact neighboring surface: `signoz_query_metrics` with `source="meter"`.
- A delegated read-only check against a live SigNoz workspace matched the three upstream response shapes and Settings UI values. Preserve sanitized, retention-only copies of those responses under `internal/client/testdata/retention/` as recorded-real-response regression fixtures; no identifiers or credentials are included.

### 2026-08-01 — Correct Cost Meter discovery routing
- A follow-up review found that routing a generic cost question directly to `signoz_query_metrics` still assumes a known metric name. Cost Meter names evolve by workspace and release, so the first call must be `signoz_list_metrics` with `source="meter"`; the metrics aggregation guide then supplies current query examples, and monetary cost remains a separate volume-times-pricing calculation.

### 2026-08-01 — Implementation and verification complete
- Implemented and registered `signoz_get_data_retention` with a typed hours-based output, read-only annotations, top-level non-required `searchContext`, matching text/structured content, Settings `webUrl`, and synchronized README/manifest/inventory coverage.
- Unit coverage pins current-versus-target semantics, custom-rule ordering and normalization, exact legacy log hours, 404-only fallback, top-level 401/403 errors, contract-drift WARN behavior, nil arguments, output-schema validation, and the recorded live response shapes.
- Delegated read-only live verification returned metrics 2160 hours, traces 720 hours, and logs 30 days (`version: "v2"`), all `success` with cold storage disabled and no custom log rules. The Settings UI matched the normalized 2160/720/720-hour result; no discrepancy or mutation occurred.
- Verification passed: `make fmt goimports`; actionlint v1.7.12 for `.github/workflows/guardrails.yaml`; focused client/handler tests; focused race detection for the concurrent reads; `go test -count=1 -run '^TestGuardrail_' ./...`; `go test -count=1 ./...`; `go build ./cmd/server`; and `bash -n scripts/test-mcp-protocol.sh`.
- The CI protocol script itself requires GNU `timeout`, which was unavailable on the macOS host. A manual loopback run with the lockfile-pinned MCP Inspector verified the new catalog entry's read-only annotations, optional `searchContext`, and typed metrics/traces/logs output schema. `shellcheck` was also unavailable locally.
- Two independent final diff reviews found no remaining server-side issues. The companion cost-reduction skill adoption remains the documented post-release follow-up for this additive tool.

### 2026-08-01 — Multi-agent review scope reduction
- Three independent reviews agreed that pending target values and cold-storage movement thresholds expanded issue #134 beyond its requested current deletion-retention snapshot. The client-visible policy is reduced to `currentStateKnown`, `currentRetentionHours`, `changeStatus`, and active `customRules`.
- The v2 custom-log endpoint exposes the latest attempted configuration during `pending` or `failed` changes, not the previously active policy. Those states now return `currentStateKnown=false` plus `changeStatus`, without presenting attempted values or rules as current.
- The tool's Cost Meter boundary is reduced to one complement: use `signoz_list_metrics` with `source="meter"` for ingestion volume. Query-guide and pricing instructions belong to the downstream cost workflow, not this retention tool.
- Legacy fallback is limited to the canonical missing-route 404 response. JSON, HTML, proxy, authorization, and other upstream failures remain top-level errors instead of silently selecting a different contract.
- The plan status returned to `In Progress` because the changes are reviewed locally but not yet shipped.

### 2026-08-01 — Multi-agent re-review and verification
- Correctness re-review found one ambiguity after the scope reduction: omitted `customRules` could mean either no active rules or an unknown active policy when `currentStateKnown=false`. The output schema and README now distinguish those cases explicitly, and a schema test pins the wording.
- Follow-up reviews for correctness, MCP contract synchronization, and overengineering all reported no remaining actionable findings. The reviewers confirmed that concurrency, v2-first probing, exact-hour legacy rereads, strict route-404 fallback, custom-rule normalization, recorded fixtures, and contract inventories are proportionate to the upstream behavior and repository requirements.
- Verification passed after the fixes: focused client and handler tests, focused race detection, workflow lint, the focused guardrail inventory, the full Go suite, `go build ./cmd/server`, `bash -n scripts/test-mcp-protocol.sh`, `git diff --check`, and manifest JSON validation.
- The requested Claude Opus high-effort review remains pending until Claude CLI browser authentication completes; no substitute model result is being treated as that review.

### 2026-08-02 — Add current cold-storage movement
- The user explicitly expanded the requested output to include cold-storage retention after reviewing the deletion-retention E2E result. This supersedes the earlier scope-reduction decision for cold storage, while retaining the decision not to expose pending target attempts.
- Add `currentColdStorageMoveAfterHours` to each signal policy. It represents the currently configured time before data moves to cold storage, normalized to hours; omission means movement is disabled when `currentStateKnown=true`, and unknown when `currentStateKnown=false`.
- Parse the current v1 `*_move_ttl_duration_hrs` fields and the v2 logs `cold_storage_ttl_days` field. Treat omission, zero, and `-1` as disabled, reject values below `-1` as contract drift, and protect day-to-hour conversion from overflow.
- For pending or failed v2 custom-log changes, continue returning only `currentStateKnown=false` and `changeStatus`; attempted deletion, rules, and cold-storage values must not be presented as active.
- This is an additive output-field change. README, manifest, schemas, tests, PR description, and live MCP E2E must be updated together. The companion agent-skills adoption remains a post-release follow-up because no released skill currently teaches this new tool contract.

### 2026-08-02 — Support the full upstream response state
- The user clarified that the tool should support every meaningful response state exposed by the retention APIs, not only the active deletion and cold-storage values.
- Restore the full normalized state/target model: `targetRetentionHours`, `targetColdStorageMoveAfterHours`, and `targetCustomRules` accompany the current fields. Target fields represent pending or failed attempted configurations and must never be described as active.
- For legacy v1 responses, current fields remain known while `expected_*` fields supply targets during `pending` or `failed` states. For v2 custom-log `pending` or `failed` responses, the API exposes only the attempted configuration, so current state remains unknown and the returned default, cold-storage, and rules populate target fields.
- Successful or idle responses expose active/current fields and omit target fields. Unsupported versions, malformed values, missing required state fields, and invalid sentinels remain detectable contract errors rather than silently degraded output.
