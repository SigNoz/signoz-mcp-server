# Feature: Org Overview — Context & Discussion

## Original Prompt
> Implement [SigNoz/nerve-pod#50](https://github.com/SigNoz/nerve-pod/issues/50), run a complete independent review against `docs/mcp-best-practices.md`, get a Claude Opus review, and E2E test the tool against the supplied staging SigNoz instance. The supplied bearer credential is intentionally redacted from this audit trail.

## Reference Links
- [SigNoz/nerve-pod#50](https://github.com/SigNoz/nerve-pod/issues/50)
- [`docs/mcp-best-practices.md`](../docs/mcp-best-practices.md)
- SigNoz backend endpoint: `GET /api/v1/stats`

## Key Decisions & Discussion Log

### 2026-08-02 — Initial scope and review requirements
- Add a distinct read-only posture-orientation tool rather than extending an entity-list tool: the task is a one-shot workspace snapshot across telemetry, dashboards, alerts, notification channels, saved views, pipelines, and integrations.
- Use the agent/domain name `signoz_get_org_overview`; do not expose the backend route in the client-visible description.
- Keep the input task-shaped and minimal: only the required-by-policy top-level `searchContext`, which is advertised but not required.
- Treat the user-supplied bearer token as ephemeral test input: never echo, log, persist, or commit it.
- The live E2E run will be delegated as required by repository policy; it must exercise the MCP tool, propagate auth correctly, report observed server fields, and leave no created resources.
- Claude Code 2.1.214 is installed but not currently authenticated. Continue implementation and all other reviews; the requested Opus pass remains gated on Claude authentication.

## Open Questions
- [x] Should the MCP result be a grouped owned envelope, a faithful raw passthrough, or both grouped known fields plus bounded unmapped stats? Resolved: use an owned, grouped, organization-scoped envelope; preserve only compatible new keys inside approved org-scoped observability prefixes via `additionalStats`. Omit malformed known keys and report their names through `invalidStatFields`. Never fall back to the unfiltered upstream bag.
- [x] Which upstream fields are stable enough to group without silently discarding future keys? Resolved: group org-filtered dashboard, configured rule, channel, saved-view, log-pipeline, and cloud-integration keys. Exclude deployment-global telemetry/infra and alert-runtime keys, plus unrelated user/license/config/auth metadata.
- [x] Is server-side caching warranted, or should the backend remain the single owner of freshness/caching? Resolved: no MCP-side cache in this PR; the upstream request is already bounded to 10 seconds, while a shared result cache would complicate tenant isolation and freshness semantics.
- [x] Does the companion `SigNoz/agent-skills` repository require a change for this additive tool? Resolved: no required change under CMP-3 because the contract is additive and the companion has no static tool allowlist. A separate optional follow-up could use the overview as the first coarse step in `signoz-setting-up-observability`.

### 2026-08-02 — Upstream contract and tenant-scope audit
- Verified current SigNoz `main` (`ab91995ee55eee2e1c4845b2eb0af2272ca43255`): the route is Viewer-accessible, has no parameters, returns `{"status":"success","data":{...}}`, and bounds collection to 10 seconds.
- The aggregator deliberately swallows individual collector failures and still returns HTTP 200. Therefore absent fields are unavailable, never implicit zero, and the owned result uses pointer/omitted counts rather than zero defaults.
- The upstream telemetry and infra collectors ignore `orgID` and query deployment-wide ClickHouse tables. Alert firing/last-fired stats are also explicitly aggregated across all orgs. Exposing those fields through an organization tool would violate SEC-1, so the MCP result excludes them and reports `signals.available=false` / `alerts.runtime.available=false` with a tenant-scope explanation.
- A malformed or changed top-level envelope cannot safely fail open to raw passthrough because the upstream bag contains deployment-wide and non-observability fields. The handler emits a WARN and returns a top-level coded `UPSTREAM_ERROR` instead; compatible new keys within approved org-scoped prefixes fail open through `additionalStats` with a WARN.
- Dashboard panel counters only inspect legacy v1 widget/query shapes and count builder query entries rather than unique v2/Perses panels. The result labels their coverage `legacyV1WidgetsOnly`, and the tool description states the limitation inline.
- Count values are decoded from `json.RawMessage` into integers and re-encoded through the precision-preserving `structuredResult` path; this avoids float64 loss above 2^53.

### 2026-08-02 — Companion agent-skills CMP-3 audit
- No companion change is required: this adds a new tool without modifying any existing contract, and the companion plugin discovers `signoz_*` tools dynamically.
- Optional follow-up: teach `signoz-setting-up-observability` to call the overview first for coarse artifact posture, while retaining list calls for exact names, IDs, duplicate detection, and channel selection.

### 2026-08-02 — Partial-success observability
- Because `/api/v1/stats` returns HTTP 200 even when individual collectors fail, each required organization-scoped group now exposes an `available` flag derived from its sentinel count. Missing sentinel fields are included in the bounded contract-drift WARN, while omitted counts remain absent rather than becoming false zeroes.
- Cloud integrations do not expose a stable total-count sentinel and remain an explicitly grouped map rather than claiming completeness from the presence or absence of provider-specific keys.

### 2026-08-02 — Cloud-provider availability correction
- A source-level follow-up established that the EE collector queries AWS and Azure independently and emits each provider key even for a genuine zero, but omits that provider on query failure; OSS emits neither key. The result therefore exposes `sourceAvailability` (`complete`, `partial`, or `unavailable`) plus per-provider `dataAvailable` and optional `connectedAccounts` instead of an ambiguous flat map.
- The earlier conclusion that provider-key absence could not act as any sentinel is refined: it is not a collector-wide sentinel, but it is a truthful per-provider availability sentinel for the current AWS/Azure collectors. Missing provider fields emit the same bounded partial/drift WARN and are never converted to zero.
- `connectedAccounts` is documented narrowly as non-removed account rows with an account ID and at least one agent report; it does not assert recent check-in or that an integration service is enabled.

### 2026-08-02 — Independent best-practices review findings
- Accepted OUT-3/SUR-1: boolean availability and omitted fields were machine-readable but did not attach recovery to every partial group. The result now sets `metadata.partial` and emits sorted `incompleteGroups` entries with affected owned output paths, a reason, a next action, and exact fallback tools where they exist. Intentionally excluded signal/runtime groups also expose `nextTools`.
- Accepted OUT-5/CMP-3: a malformed value for a known counter was being preserved under `additionalStats`, conflicting with the owned canonical field and the documented meaning of “compatible new stats.” Known malformed fields are now omitted, listed under `metadata.invalidStatFields`, counted as omitted, and included in the relevant recovery entry; `additionalStats` is reserved for unknown compatible fields.
- The independent EVL-1 selection pass otherwise succeeded for direct posture requests, indirect pre-setup inventory requests, exact-resource negative prompts, and live-ingestion negative prompts. A line-accurate re-review will run after these repairs.

### 2026-08-02 — Independent best-practices re-review
- Final EVL-3 result: no remaining findings, high confidence. The reviewer confirmed tenant filtering, auth/error propagation, partial-result recovery, drift observability, annotations/schema fidelity, documentation/metadata synchronization, and the additive CMP-3 conclusion.
- Final EVL-1 result: direct and indirect posture prompts select `signoz_get_org_overview`; exact dashboard inventory routes to `signoz_list_dashboards`; live log-ingestion freshness routes to `signoz_search_logs`; partial responses route through `metadata.incompleteGroups.nextTools`.

### 2026-08-02 — Claude Opus 5 review
- The Claude CLI `opus` alias unexpectedly resolved to `claude-opus-4-8`; that pass was not treated as the requested Opus 5 review. The review was rerun with explicit model `claude-opus-5`, high effort, manual/read-only permissions, in the current worktree. The returned model-usage record confirmed `claude-opus-5`.
- Accepted finding: because OSS emits neither AWS nor Azure cloud key, treating both absent keys as collector failures made every OSS snapshot permanently partial and emitted a noisy WARN with a recursive retry. Zero reported providers now means structurally `unavailable` without degrading the overall snapshot; exactly one reported provider remains a true partial, and malformed provider values remain invalid/partial.
- Accepted finding: log-pipeline group availability now derives from its total-count sentinel, matching the other groups and README. A missing `enabledCount` remains explicit through omitted output plus `incompleteGroups` recovery.
- Accepted finding: the live E2E no longer restates `partial == len(incompleteGroups) > 0`. It cross-checks each live AWS/Azure source key against provider availability/counts, rejects recursive cloud recovery, validates source availability, and logs the observed partial groups.
- Adopted non-blocking hardening: important completeness and coverage fields now carry output-schema descriptions, and an unexpected upstream status string is bounded before entering the coded error. The v0.129.0 floor was independently validated against upstream release history during the initial contract audit.

### 2026-08-02 — Claude Opus 5 re-review
- The same verified `claude-opus-5` session confirmed all three original findings were resolved. It then identified the residual ambiguity that zero reported cloud providers can mean either unsupported/edition-gated behavior or two simultaneous provider-query failures. README and output-schema text now state that ambiguity explicitly and forbid interpreting `unavailable` as zero configured integrations.
- The tool description now promises fallback tools only where available, matching the cloud recovery path, which intentionally provides UI guidance without a recursive MCP call.
- The ordinary unit suite now rejects `signoz_get_org_overview` as a cloud-partial recovery tool; the non-recursion invariant no longer depends on the live E2E build tag.

### 2026-08-02 — Final Opus verification and compatibility provenance
- A final diff-focused continuation of the same verified `claude-opus-5` session reported no remaining findings and confirmed the residual fixes, output-description budget, focused tests, and append-only context history.
- SigNoz commit [`287b60cbe61d06490bc1a4ddda6fe28002daf431`](https://github.com/SigNoz/signoz/commit/287b60cbe61d06490bc1a4ddda6fe28002daf431) introduced `GET /api/v1/stats`. Adjacent-tag verification found it absent from v0.128.0 and present in v0.129.0; `git tag --contains` identifies v0.129.0 as the earliest release tag ([compare](https://github.com/SigNoz/signoz/compare/v0.128.0...v0.129.0)).
- The non-cloud required sentinels were audited at current upstream source: successful dashboard, rule, channel, saved-view, and log-pipeline collectors always emit their fixed total fields, including zero. Cloud integrations are the sole required group-level sentinel ambiguity; the EE-only `public_dashboard.count` remains optional and does not control completeness.

### 2026-08-02 — Live staging E2E
- The delegated read-only `TestE2EOrgOverview` passed against the supplied staging instance. It round-tripped 29 recognized organization-scoped source fields across dashboards/panels, rules/signals, notification channels, saved views, log pipelines, public dashboards, and AWS/Azure cloud integrations.
- The live result reported `cloudSourceAvailability=complete`, `metadata.partial=false`, and no incomplete groups. Deployment-wide signal and alert-runtime stats remained unavailable and no deployment-wide source key leaked into the owned result; metadata counts reconciled.
- The tool created zero resources, so cleanup was not applicable. The bearer credential was passed only through the test process environment and was not logged, printed, persisted, or committed.
