# Feature: mcp-go v0.56.0 Upgrade & Feature Adoption — Context & Discussion

## Original Prompt
> I want to update mcp-go to latest version and incorporate new features we get with it and are relevant for us. Use /codex agents to get changelog and then suggest me what can be incorporated

## Reference Links
- [mcp-go releases](https://github.com/mark3labs/mcp-go/releases) (v0.50.0 … v0.56.0)
- [v0.49.0…v0.56.0 compare](https://github.com/mark3labs/mcp-go/compare/v0.49.0...v0.56.0)

## Key Decisions & Discussion Log
### 2026-07-10 — Changelog research & version bump
- Codex (gpt-5.6-sol, xhigh) researched all 9 releases after v0.49.0 up to v0.56.0 and cross-checked against this repo's usage. Key API names verified against the v0.56.0 module source.
- No source-breaking changes affect this repo: the two compile breaks (`ListResources`/`ListPrompts` pointer values in v0.53.0, `SamplingCapability` type in v0.54.0) touch APIs we don't call.
- Bumped `go.mod` to v0.56.0; `go build ./...` clean, `go test ./...` all 17 packages pass. New transitive dep: `dlclark/regexp2` (schema validation).
- Only default behavior change in the range: v0.56.0 DNS-rebinding protection. Verified in source (`server/http_localhost.go`): rejects only when the *listener local address* is loopback AND the Host header is non-loopback. Kubernetes pod-IP traffic is unaffected; a same-host sidecar/proxy forwarding via 127.0.0.1 with a preserved public Host header would get 403.
- Feature adoption deliberately decoupled from the version bump: validation options change runtime behavior and need contract tests first.
- Known hazard for input validation adoption: handlers accept argument forms the advertised schemas don't declare (silent `query`→`filter` alias; string-or-integer unions for numeric params). Plain `WithInputSchemaValidation()` is safe for undeclared params (JSON Schema allows additional properties by default) but enforces declared types/enums; `WithStrictInputSchemaDefault()` would break the silent `query` alias and is excluded from the initial adoption.

### 2026-07-10 — Workflow decided: single PR, Codex-driven
- Owner directive: get a Codex second pass for missed wins (explicitly re-evaluate OTel tracing hooks from v0.54.0), consolidate ALL recommended items into a single-PR plan, run codex-plan-review until approval, have Codex implement, then review with Codex + Claude agents at appropriate effort.
- Owner directive: the plan must include an explicit checklist of things that can break.

### 2026-07-10 — Codex addendum + source verification; plan rewritten for single PR
- Codex addendum (same session, resumed) re-evaluated OTel hooks and validation semantics; every claim verified against v0.56.0 module source before adoption:
  - Input validation short-circuits BEFORE tool middleware; output validation replaces results AFTER it → both invisible to our `execute_tool` span/logs/analytics. Mitigation: `AfterCallTool` validation observability (WARN + metric).
  - Malformed schemas fail open silently (`validate` returns `(false,nil)` on compile error) → schema-compile CI test required.
  - Undeclared props accepted when `additionalProperties` unset (jsonschema/v6) → silent `query` alias survives plain validation; strict default stays off.
  - Union types `{"type":["string","integer"]}` validate correctly.
  - `WithTracer` duplicates our method+tool spans and force-installs its tool middleware (no method-only option) → not adopted.
  - `WithMetaPropagator` unchanged would break our hook-ended method spans (extraction replaces active span context; hooks end `trace.SpanFromContext`) → SEP-414 DEFERRED to follow-up with method-span ownership consolidation.
  - `mcp-go/otel` is separately versioned (v0.54.0 tag) — not assumed at v0.56.0.
- Plan rewritten: single PR = bump + characterization tests + input validation + output schemas (code-controlled only) + StreamableHTTP logger + narrow raw-decode adoption; explicit 10-item "things that can break" checklist added per owner directive.

### 2026-07-10 — Owner directive: dual string/number inputs are intentional
- Numeric tool params intentionally accept both strings and numbers ("50" and 50), transformed internally. Plan item F rewritten: no blanket `UseNumber` (would flip map types to `json.Number` and break `.(float64)`/`.(string)` assertions), no concrete-int `BindArguments` (rejects numeric strings); only per-field flexible unmarshallers where a real >2^53 need exists, with both-forms tests; otherwise F ships tests-only. Breakage checklist #7 expanded accordingly.
- Owner also directed: plan requires explicit owner approval before each codex-plan-review submission.

### 2026-07-10 — Principles audit against internal MCP best-practices doc
- Owner directed the plan be checked against the internal agent-first MCP best-practices doc. Direct consequence: day-one hard enforcement of input validation contradicts "tools should accept all and normalize internally" + "if the agent calls it wrong, the contract is the defect" — replay tests can't enumerate real agent traffic.
- Plan restructured: input validation rolls out **shadow-first** (`inputValidation.mode` config: off|shadow|enforce, default shadow this PR; observe-only WARN+metric on mismatch, never reject). Enforce default flips in a follow-up after ~zero mismatch soak on staging+prod, gated additionally on SEP-1303 error text naming the param and constraint (road-to-recovery quality).
- B1 broadened from numerics/`query`-alias to a full normalization audit of param helpers; any accepted-but-schema-rejected form = schema bug (add unions/aliases), not acceptance bug.
- D gains the read→write-back constraint: paired get/update tools' output schemas must stay write-back compatible.
- Added "Principles alignment" section to the plan (doc referenced without URL — private link, public repo).

### 2026-07-10 — Codex plan review round 1 → REVISE; plan revised
- Codex (sol/xhigh, read-only, session 019f4a1e) returned 7 findings; all verified sound and adopted:
  1. AfterCallTool doesn't repair telemetry → dedicated authoritative validation-rejection event + exactly-one-terminal-event tests (input rejection / output rejection / success / handler error).
  2. Output validation after side effects can flip successful mutations (notification channels deliberately return success when read-back fails, to prevent duplicate retries) → runtime output enforcement limited to an exact read-only/idempotent allowlist; mutation tools get no declared output schema this PR.
  3. "Read valid to write straight back" overstated (update wrappers `{id, dashboard}`, field stripping, flattened channel args) → per-resource compatibility rules + contract test per pair; channels excluded.
  4. Shadow validator integration point = `addTool` in schema_compat.go (registration-time compile + handler decoration); jsonschema/v6 becomes a direct dep.
  5. `structuredResult` text-only fallback → nil StructuredContent silently skips SDK validation → runtime WARN+counter; exact schema-inventory test.
  6. Config = env var `MCP_INPUT_VALIDATION_MODE` (typed, startup-fail on unknown); files list corrected `internal/otel` → `pkg/otel/metrics.go`; bounded metric attrs; never log raw args (secrets).
  7. Concrete merge gates: vet/lint/tidy/race, license+size baseline, transport-logger fields test, raw-wire precision test (F tests-only unless bug proven), live-verification constraints restated, loopback topology = STOP condition, no DNS opt-out by default.

### 2026-07-10 — Deployment topology verified: DNS-rebinding merge blocker RESOLVED
- Owner provided deployment configs (internal GCP deployments repo). Verified across staging + all three production clusters: signoz-mcp-server pod is single-container (no sidecars/mesh), Service is ClusterIP, ingress-nginx (`nginx-ui` class) proxies to pod IPs, kubelet probes hit pod IP :8000/healthz, and the AI assistant connects via the public ingress URL. No path reaches the server over a loopback listener with a public Host header. v0.56.0 DNS-rebinding protection is safe to keep enabled with no opt-out; local dev (localhost Host on loopback) remains allowed.

### 2026-07-10 — Codex plan review round 2 → REVISE; four blockers fixed
- (1) Output-rejection double-telemetry → output validation moved into our registration-time handler decorator (returns `IsError` before `loggingMiddleware`, so middleware records exactly one failure); SDK `WithOutputSchemaValidation` retained as defense-in-depth (skips already-error results); nil-StructuredContent detection in the same decorator.
- (2) Plumbing → validation mode on `Handler`; `addTool` becomes `h.addTool` (~41 sites); compiled schemas closed over in decorator; compile failure = fail-open + bounded ERROR signal + CI test failure.
- (3) Allowlist enumerated NOW via a full 41-tool output-shape audit (subagent): only 5 read-only class-(a) tools qualify — search_docs, fetch_doc, check_metric_usage, list_alerts, list_alert_rules (Labels as open map). Notable: the get_* read tools are upstream passthrough despite setting StructuredContent → write-back pairs get fixture/contract transformation tests, independent of schemas; list_dashboard_templates excluded (text-only today, structured migration = follow-up).
- (4) Executable gates pinned: make fmt/goimports + git diff --exit-code, primus CI jobs (fmt/lint/test/deps/build), go vet, tidy diff check, -race on tools+mcp-server packages, go-licenses (jsonschema Apache-2.0, regexp2 MIT), binary-size delta ≤ +5 MiB, enforce-hook detection via SDK-pinned prefix `input schema validation failed: ` with contract test against SDK drift.

### 2026-07-10 — Codex plan review round 3 → REVISE (2 minor); fixed
- Pinned exact output-schema mirror structs for the two list tools (`alertListOutput`/`alertRuleListOutput` with `Data []types.Alert`/`[]types.AlertRuleSummary` + `paginate.Metadata`, defined in alerts.go) — `paginate.Response.Data` is `[]any` so the generic type would not validate element shapes.
- Made remaining verification commands executable: pinned `go run github.com/google/go-licenses@v1.6.0 report`, split binary-size builds into separate main/branch commands + `stat -f%z`, switched format gate to non-mutating `gofmt -l` / `goimports -l`.

### 2026-07-10 — Codex plan review rounds 4–5 → APPROVED
- Round 4 fix: format gates scoped to tracked/non-ignored Go files via `git ls-files -co --exclude-standard` (plain `-l .` recursed into ignored worktrees); Codex verified the commands run clean in this checkout.
- Round 5: **VERDICT: APPROVED** — plan implementation-ready after 5 rounds (session 019f4a1e, gpt-5.6-sol/xhigh, read-only).

### 2026-07-10 — Implementation by Codex (session 019f4a38); gates verified green
- Codex (sol/xhigh, workspace-write) implemented the full plan scope: 24 files modified + 5 new test files (+668/−150). Run was killed at the very end (background timeout) before emitting its report; report recovered via session resume.
- Coordinator independently verified all executable gates on the resulting tree: build ✓, vet ✓, go test 17/17 packages ✓, -race on tools+mcp-server ✓, ls-files-scoped gofmt/goimports clean ✓, tidy idempotent ✓.
- Note: `git diff --exit-code go.mod go.sum` as written in the plan only works post-commit; on an uncommitted tree the tidiness gate is "tidy is a no-op" (checked via checksums).
- Codex prematurely set plan Status to Done; coordinator reset to In Progress (multi-reviewer pass + live smoke + PR still pending).

### 2026-07-10 — Multi-reviewer pass: 3 Codex findings + 10 workflow-verified findings; fix round launched
- Codex adversarial review (read-only, sol/xhigh): duplicate terminal WARN on output rejection; >2^53 precision test drives unused BindArguments path (production uses GetArguments/float64); DNS-rebinding test bypasses production wiring. Confirmed sound: allowlist, raw-wire replay, mode semantics, no secret leaks.
- Claude workflow review (26 agents, 20 verified, top 10 reported): **critical — `mcp.WithInputSchema[T]` emits `additionalProperties:false`**, so update_alert/update_dashboard advertised schemas reject the write-back payloads the handlers accept (shadow noise blocks enforce gate; enforce breaks write-back); generated schema spuriously requires `recoveryTarget` (non-omitempty pointer); output enforcement not mode-gated (active in off; mirror-struct drift would discard fetched data; no kill-switch); decorator output rejections emit no metric; enforce rejections invisible to mcp.tool.calls dashboards (plan-accepted, but new metric must be dashboarded before flip); SDK output validation double-validates hot path; duplicated load-bearing prefix constants across packages; formulaQueries description lost field vocabulary; README stale (groupBy/formulaQueries).
- **Plan deviation (risk-reducing)**: output validation becomes mode-aware like input (off/shadow/enforce; default shadow = observe-only, pass result through); `server.WithOutputSchemaValidation()` enabled only in enforce mode (in shadow it would reject results the decorator deliberately passes, and it double-validates the hot path). Deviates from the Codex-approved "decorator rejects immediately" design — justified by the mirror-struct-drift finding; owner may revert to hard output enforcement if preferred.
- Fix round delegated to the Codex implementation session (resume) with the consolidated 9-item spec; gates + focused re-review to follow.

### 2026-07-10 — Fix round complete; all gates independently verified green
- Codex fixed all 9 consolidated findings (F1–F9), disagreed with none. Coordinator re-ran every gate locally (Codex sandbox blocked loopback binds): build ✓, vet ✓, scoped gofmt/goimports ✓, go test ./... 17/17 ✓, -race both trees ✓, tidy idempotent ✓. Spot-verified F1 (additionalProperties:false stripped in normalize; RecoveryTarget omitempty) and F2 (output validation mode-gated; SDK output validator enforce-only).
- Docs updated in fix round: README (groupBy union, formulaQueries dual form, validation modes, env defaults), docs/architecture.md (validation env var). manifest.json + agent-skills re-checked: no changes needed.
- Remaining: live smoke (staging), commit + PR (owner approval required).

### 2026-07-10 — Live smoke on local binary (opus subagent): all reachable paths PASS
- Credential-free verifications on the local build (HTTP :18321): shadow mode never rejects and emits bounded mismatch WARN (`validation.path:/limit`, no raw values); enforce mode rejects in-band with actionable `input schema validation failed: /limit …Want:[integer string]`; dual `limit:5`/`limit:"5"` accepted identically; **write-back payload with audit fields + recoveryTarget-less thresholds NOT rejected in enforce mode** (F1 fix confirmed at the wire); DNS-rebinding 403 via production wiring (`Host: mcp.us2.signoz.cloud` on loopback → 403, localhost → 200); docs tools return StructuredContent; no panics; full cleanup confirmed.
- Blocked without local SigNoz credentials (plugin is OAuth-only; no PAT found anywhere — env, .env, claude/codex configs, keychain): live view lifecycle and live alert write-back round-trip; covered by passing repo contract tests. Can be run live later with SIGNOZ_API_KEY/SIGNOZ_URL.

## Open Questions
- [x] Which adoption candidates to implement first — RESOLVED: single PR = bump + characterization tests + shadow-first input validation + read-only-allowlist output schemas + transport logger + F tests-only; SEP-414/tracer/enforce-flip/mutation-output-schemas deferred.
- [x] **MERGE BLOCKER**: loopback listener with public Host topology — RESOLVED clean (see 2026-07-10 topology entry): no sidecars/mesh, ingress-nginx → pod IP, probes → pod IP, assistant → public URL. Protection stays on, no opt-out.
