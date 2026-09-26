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
- [x] Should the MCP result be a grouped owned envelope, a faithful raw passthrough, or both grouped known fields plus bounded unmapped stats? Resolved: return both. `data.sourceStats` is the authoritative complete flat bag containing every reported upstream `data` entry, while typed groups are convenience projections. Invalid known projection values remain in `sourceStats` and are named by `invalidProjectionFields`; future fields remain in `sourceStats` without an allowlist.
- [x] Which upstream fields are stable enough to group without silently discarding future keys? Resolved: provide typed projections for every currently observed family: signals/infrastructure, dashboards, alert rules/runtime/channels, saved views, log pipelines, cloud integrations, users, authentication, service accounts, roles, license, and configuration. Every current and future key is independently preserved in `sourceStats`, so projection evolution cannot discard data.
- [x] Is server-side caching warranted, or should the backend remain the single owner of freshness/caching? Resolved: no MCP-side cache in this PR; the upstream request is already bounded to 10 seconds, and a shared result cache would complicate deployment routing and freshness semantics.
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

### 2026-08-02 — Deployment-boundary correction from maintainer
- The maintainer clarified that current SigNoz does not provide multiple org-scoped tenants inside one deployment; each cloud user has a separate deployment. The earlier filtering decision incorrectly applied a future multi-org boundary to the current product architecture.
- Revised requirement: represent every field returned by `/api/v1/stats`, including telemetry/infra, alert runtime, and non-observability workspace/configuration stats. Org-scoping can be introduced later if the backend contract gains that boundary.
- The draft PR contract is not shipped, so its owned output can be revised directly. The next implementation pass will preserve every current and future source key while retaining grouped posture fields for agent ergonomics.

### 2026-08-02 — Superseding complete-source contract
- The current security boundary is the SigNoz deployment selected by the request URL and credential. Within that deployment, every entry reported in the `/api/v1/stats` `data` object is in scope; the earlier field-filtering design is superseded.
- `data.sourceStats` is the authoritative complete flat bag. It retains the backend's dotted key names and every successfully decoded value, including telemetry and infrastructure, alert runtime, users, authentication domains and tokens, service accounts, roles, license, configuration providers, and unknown future keys.
- Typed groups remain convenience projections for agent ergonomics and cover every current field family: `signals`, `dashboards`, `alerts`, `views`, `logPipelines`, `cloudIntegrations`, `users`, `authentication`, `serviceAccounts`, `authorization`, `license`, and `configuration`. They never narrow or override `sourceStats`.
- Missing remains distinct from zero: absence from `sourceStats` means the upstream collector did not report the key. A malformed value for a known projection stays authoritative in `sourceStats` but is omitted from the incompatible typed field and named in `metadata.invalidProjectionFields`.
- Metadata now describes projection completeness only. `reportedStatCount`, `projectedStatCount`, and `unprojectedStatCount` reconcile against `sourceStats`; `projectionPartial` and `incompleteGroups` identify expected typed fields that were unreported or invalid. They do not claim that the upstream endpoint reported every field it could have produced.
- Unknown future keys are preserved immediately in `sourceStats` and counted as unprojected until a typed convenience field is added. A malformed top-level envelope or non-object `data` remains a coded upstream error because no authoritative source bag can be constructed.

### 2026-08-02 — Complete upstream collector audit
- A fresh source audit against SigNoz main commit `ab91995ee55eee2e1c4845b2eb0af2272ca43255` found 15 concurrently aggregated collectors. Collector failures are swallowed into a partial HTTP 200, so missing expected keys require a detectable projection warning while every reported value remains authoritative in `sourceStats`.
- The audit covered all current fixed and dynamic families: telemetry counts/timestamps, infrastructure booleans, rules and firing runtime, channels, dashboards, views, log pipelines, cloud integrations, users, authentication tokens/domains, service accounts, roles, license, and storage/tokenizer/cache configuration. Typed projections now cover each family; a key-conservation test covers arbitrary future values and integers above 2^53.
- Optional normal absence is not marked incomplete for telemetry/alert/auth-token timestamps, dynamic breakdowns, `auth_token.count`, public dashboards, license fields, or zero cloud providers. Counts, infrastructure booleans, user fields, core auth-domain/role/service-account fields, and configuration providers are emitted on collector success and remain expected projection sentinels; their absence can otherwise hide a swallowed collector failure.

### 2026-08-02 — Corrected Opus 5 review
- The post-correction review ran with explicit `claude-opus-5`, high effort, manual/read-only permissions, and verified `modelUsage.canonicalModel=claude-opus-5`.
- Accepted SUR-1/SUR-6/DSC-2: the new telemetry-capable description named only generic alternatives. It now names exact inventory neighbors and the log/trace/metric tools for time-windowed or per-service questions while retaining the intended coarse freshness use case from issue #50.
- Accepted OUT-3/ERR-3: recovery for several newly projected groups fell through to a recursive `signoz_get_org_overview` recommendation. Every current group now has actionable guidance; groups without a dedicated MCP inspector point to the relevant SigNoz area without a recursive tool call, and a unit inventory enforces that invariant.
- Accepted E2E finding: the staging run reached a healthy zero-invalid-fields result, but the test compared a nil slice with a non-nil empty slice. The assertion now uses content equality that treats both as empty; all source-conservation and typed-projection checks before that assertion had passed.
- Rejected the suggestion to suppress missing infrastructure/user/auth-domain/service-account/role/configuration diagnostics. The upstream audit shows these keys are emitted on successful collectors and omitted on failure; warning on their absence implements the repository's fail-open-but-never-silent rule. Edition/tokenizer-dependent and optional timestamp keys remain non-sentinels.

### 2026-08-02 — Final forward-compatibility and conditional-sentinel review
- The source audit refined the earlier shorthand that `auth_token.count` is tokenizer-dependent: its absence is normal for JWT, but the opaque tokenizer emits it on a successful collection. The typed projection therefore conditionally treats it as expected only when `config.tokenizer.provider=opaque`; JWT absence remains complete. An unknown future tokenizer value emits a bounded drift WARN without claiming projection incompleteness.
- All typed dynamic families now accept only the current single-segment key shape. Deeper additive keys under a known prefix remain authoritative in `sourceStats`, count as unprojected, and do not create a phantom typed label, false invalid field, or WARN. Unit fixtures cover both boolean and numeric deeper keys, and the live E2E oracle applies the same rule.
- `serviceAccounts.available` and `authentication.tokens.available` now expose whether their total count was reported. Infrastructure schema text explicitly distinguishes collector failure from a reported `false` value. Recovery guidance for every current group is non-recursive.
- The independent rubric pass added the final negative-selection boundaries: `signoz_list_metrics` for the metric-name catalog and `signoz_list_alerts` for current alert instances. Together with named inventory and time-windowed neighbors, direct and indirect posture routing passes while exact-inventory and time-windowed negative prompts route away from the overview.

### 2026-08-02 — Final independent and Opus 5 re-reviews
- The independent clean-context review against every applicable section 11 item finished with no findings after verifying source conservation, deployment/request scoping, conditional JWT/opaque semantics, single-segment dynamic projections, non-recursive recovery, negative-selection boundaries, output-schema openness, and synchronized docs/metadata/tests. Direct and indirect posture prompts select the overview; metric-name, current-alert-instance, exact-inventory, and time-windowed/per-service prompts select their named neighbors.
- The same explicitly verified `claude-opus-5` high-effort read-only session re-read the final worktree and returned no findings. Its model usage again reported canonical model `claude-opus-5`; no permission denials occurred on the final pass.

### 2026-08-02 — Final complete-source staging E2E
- The delegated live test passed against staging with every one of 63 reported source keys and semantic values preserved unchanged in `data.sourceStats`; all 63 were also validated in typed projections. Metadata reconciled as reported=63, projected=63, unprojected=0, invalid=0, `projectionPartial=false`, and zero incomplete groups; cloud source availability was complete.
- The live deployment used the JWT tokenizer, so `auth_token.count` was legitimately absent and `authentication.tokens.available=false` without making the projection partial. The opaque-token behavior is covered separately by a unit test that requires the count and emits recovery metadata when it is missing.
- The read-only E2E created zero resources, so cleanup was not applicable. The supplied credential remained process-ephemeral and was not logged, printed, persisted, or committed.

### 2026-08-02 — Minimum-version recovery follow-up
- The issue #49 follow-up review was combined with this PR and a fresh Opus pass found that an older deployment's HTTP 404 still lacked an inline correction, even though the v0.129.0 floor was documented externally.
- The tool description and synchronized README/manifest metadata now advertise SigNoz v0.129.0 or newer. A 404 keeps the shared `NOT_FOUND` classification and adds an immediate upgrade instruction plus a fallback to the narrower inventory and signal-query tools already named by the tool surface.
- An adversarial E2E review caught that inactive or nonexistent cloud workspaces can also return 404. The final recovery is conditional: first verify that the configured URL points to an active deployment, then upgrade only when a reachable deployment lacks the stats route.

### 2026-08-03 — Maintainer PR description cleanup
- The maintainer requested that the tool-facing description stay simple, explicitly support questions about the current status of SigNoz, and omit the SigNoz version number.
- Decision: lead the tool, README table, and manifest descriptions with current deployment status and overall posture; remove the overview-specific version prerequisite and version-based 404 wording while retaining URL verification plus narrower-tool recovery.
- The maintainer also classified the legacy dashboard-panel limitation as a temporary bug that should not be advertised. Remove the `legacyV1WidgetsOnly` output marker and related README/tool/schema wording rather than making it part of the client-visible contract.

### 2026-08-03 — Actionable 404 recovery review
- Automated review correctly found that the simplified tool description no longer named the fallback tools referenced by the 404 error.
- Decision: keep the general tool description concise and make the exceptional 404 result self-contained by naming the exact inventory and signal-query tools inline. The focused test pins representative fallback names so future description cleanup cannot break recovery again.
