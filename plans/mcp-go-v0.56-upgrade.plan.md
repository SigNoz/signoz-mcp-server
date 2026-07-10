# Plan: mcp-go v0.56.0 Upgrade & Feature Adoption

## Status
In Progress

## Context
We pin mcp-go v0.49.0; latest is v0.56.0. The bump is compile-safe (verified: build clean, 17/17 test packages pass) and pulls in `santhosh-tekuri/jsonschema/v6` + `dlclark/regexp2` for schema validation. Per owner decision, all recommended feature adoptions land in a **single PR** together with the bump. SEP-414 `_meta` trace propagation is deferred to a follow-up (see Deferred).

Findings below were verified directly against the v0.56.0 module source, not just release notes.

## Approach

### A. Version bump (applied in working tree)
- `go.mod`/`go.sum`: v0.49.0 → v0.56.0. No code changes required to compile.
- `santhosh-tekuri/jsonschema/v6` moves from indirect to direct dependency (shadow validator imports it).

### B. Characterization tests first (before enabling any validation)
1. **Normalization audit + replay tests**: audit every internal normalization in the param helpers (`params.go`, aggregate helpers, tool handlers) — string→number, string→bool, aliases, defaults, case handling — then replay every accepted form against the advertised schema using `santhosh-tekuri/jsonschema/v6`. Known cases: string-or-integer numeric params, the silent `query`→`filter` alias (undeclared → accepted, verified: validator ignores undeclared props when `additionalProperties` is unset), omitted `searchContext`. Replay must go through the real JSON decode path (raw wire bytes → decode → handler), not Go-constructed maps, so type behavior matches production. Any accepted-but-rejected-by-schema form is a contract defect: fix the schema (union types), not the acceptance.
2. **Schema-compiles test**: compile every registered tool's input (and output, where declared) schema. Verified in source: `inputSchemaValidator.validate` returns `(false, nil)` on compile error — a malformed schema silently disables validation for that tool. A test must make this loud. Assert an **exact expected inventory** of schema-declaring tools so a tool that accidentally never declared its schema is caught.
3. **DNS-rebinding regression test**: request over loopback listener with non-loopback Host → 403; pod-IP/localhost-Host flows unaffected.

### C. Input schema validation — shadow first, enforce later (v0.50.0; readable errors v0.56.0)
Principle constraint (agent-first): tools accept flexible input types and normalize internally; a call an agent makes "wrong" that we can normalize is a contract defect on our side, not the model's. Replay tests can't enumerate what real agents send, so enforcement cannot be day-one.

- Config: env var **`MCP_INPUT_VALIDATION_MODE`** ∈ `off` | `shadow` | `enforce`; typed constants; unknown value **fails startup** (no silent fallback); **default `shadow`** in this PR; identical behavior on HTTP and stdio; config unit tests + README environment-table entry.
- **Shadow mode — registration-time design**: extend the existing `addTool` wrapper (`schema_compat.go`) — the one place that sees the final normalized schema before SDK registration — to compile the schema once and decorate the tool's handler. Per call, validate decoded args; on mismatch emit WARN + `mcp.tool.validation.mismatches` metric, then **proceed to the handler regardless**. No central registry (no sync/lookup concerns). Mismatches on real traffic drive schema fixes (union types, alias declaration), per "contract as the defect".
- **Dependency plumbing (explicit design)**: validation mode lives on `Handler` (`internal/handler/tools/handler.go`), which already owns the logger and meters — no package-level mutable configuration. `addTool` becomes the method `h.addTool` (update all ~41 registration sites). Schemas are compiled after `normalizeToolSchemas`, and the compiled input/output schemas are closed over in the decorated handler. **Registration-time compile failure**: register the tool fail-open (undecoated) but emit a bounded startup ERROR log + metric; the B2 CI compile/inventory test independently fails the build.
- **Enforce mode** (follow-up default-flip, wiring ships now): the **same decorator** rejects mismatched input before the handler — the SDK validators (`WithInputSchemaValidation` / `WithOutputSchemaValidation`) are **not used**. One validation owner for all three modes means: rejections are in-band `IsError` results on the repo's coded-error contract (`code: VALIDATION_FAILED`, text names the tool, offending path, and violated constraint), they flow through `loggingMiddleware` like any other tool failure (normal `execute_tool` span/log/metric — exactly one terminal event, no hook glue), and there is no coupling to SDK-pinned message prefixes at all. Flip only after ~zero mismatch rate over a soak period on staging + production. (Post-review simplification 2026-07-10; the earlier design used SDK validation for enforce plus an `AfterCallTool` prefix-detection hook — deleted.)
- **Validation input fidelity**: when the SDK preserves `RawArguments` (all wire traffic), the decorator validates the exact raw bytes, skipping the marshal round-trip of the decoded tree; map-constructed requests (tests) fall back to the round-trip path.
- **Telemetry hygiene**: metric attributes use normalized, bounded param paths (strip array indexes and user-controlled property names; cap length). Logs carry bounded path/keyword metadata only — **never raw argument values** (tool inputs can contain webhook passwords, API keys). Mismatch/missing-content WARNs are deduplicated per `(tool, direction, path, constraint)` key per process so a looping client cannot flood logs; counters stay exact per event.
- **Error-quality gate before enforce**: rejection text must name the offending param and constraint (road to recovery); the decorator surfaces the jsonschema leaf error (`at '/limit': got boolean, want string`-style) plus the tool name, pinned by test.
- Do **not** enable `WithStrictInputSchemaDefault()` — `additionalProperties:false` would break the silent `query` alias.
- Alignment note: the decorator validates against the exact normalized schema that is advertised (same `mcp.Tool` value passed to `AddTool`) — validator and published contract share one source, closing the advertised-schema-vs-handler-acceptance drift class.

### D. Output schemas — enumerated read-only allowlist, decorator-enforced
**Enforcement mechanism**: output validation for allowlisted tools happens inside the same registration-time handler decorator, *before* the result returns to `loggingMiddleware` — on mismatch in enforce mode the decorator returns an `IsError` result (`code: OUTPUT_SCHEMA_VALIDATION_FAILED`), so the existing middleware records exactly one failure event. The decorator is the **only** output validator (`server.WithOutputSchemaValidation()` removed post-review — it double-validated every successful result for zero added coverage). Nil-`StructuredContent`-on-success stays a WARN + counter in **every** mode including enforce (fail open, never silent: rejecting would replace a usable text response with nothing actionable). Success shapes keep arrays non-nil (`results`, `available_headings`) — the generated schema types Go slices as `["null","array"]` so `null` is technically schema-valid, but `[]` is the friendlier contract for typed clients, and the allowlist test validates every success against the advertised schema.

**Allowlist (exact, derived from a full 41-tool output-shape audit — only read-only, fully code-controlled success shapes qualify):**

| Tool | Output Go type | Top-level shape | Upstream-owned nested fields | Rationale |
|---|---|---|---|---|
| `signoz_search_docs` | `docs.SearchResponse` | `{results[], query, total_matches}` | none | fully ours, zero upstream coupling |
| `signoz_fetch_doc` | `docs.FetchResult` | `{content, available_headings[], …}` | none | fully ours, stable |
| `signoz_check_metric_usage` | `map[string]client.MetricUsage` | `{<metric>: {dashboards[], alerts[], error?}}` | none | fully ours, small |
| `signoz_list_alerts` | `paginate.Response` w/ `[]types.Alert` | `{data[], pagination}` | none | typed, ours |
| `signoz_list_alert_rules` | `paginate.Response` w/ `[]types.AlertRuleSummary` | `{data[], pagination}` | `Labels` open map → `additionalProperties: true` | typed, ours, one open map |

Schema-generation types (exact — runtime JSON unchanged): `mcp.WithOutputSchema[paginate.Response]` would type `data` as `any[]`, so the two list tools use dedicated mirror structs, defined next to their handlers in `internal/handler/tools/alerts.go`:
- `alertListOutput struct { Data []types.Alert \`json:"data"\`; Pagination paginate.Metadata \`json:"pagination"\` }` → `mcp.WithOutputSchema[alertListOutput]()`
- `alertRuleListOutput struct { Data []types.AlertRuleSummary \`json:"data"\`; Pagination paginate.Metadata \`json:"pagination"\` }` → `mcp.WithOutputSchema[alertRuleListOutput]()`
The other three allowlisted tools use their existing payload types directly (`docs.SearchResponse`, `docs.FetchResult`, `map[string]client.MetricUsage`).

**Excluded, with rationale:**
- All mutating tools (spec says declared output schemas MUST be conformed to; notification-channel create/update deliberately return success after the mutation even when read-back/test-send fails to prevent duplicate retries — SDK/decorator validation would flip that into an error and invite duplicate creation; the trivial `{status,id}` mutation envelopes could qualify later but are excluded on the mutation rule).
- All class-(b) tools (`list_services`, `list_dashboards`, `list_views`, channel create/update): stable outer envelope but upstream-owned, drift-prone `data[]`/`channel` payloads.
- All class-(c) passthrough tools — including the four structured-but-passthrough reads (`get_dashboard`, `get_alert`, `get_view`, `get_notification_channel`, `get_trace_details`): they set `StructuredContent` today but the shape is upstream's, not ours.
- `signoz_list_dashboard_templates` (fully ours and maximally stable, but returned text-only today; migrating it to a structured result is an unrelated output change — follow-up).

**Read→write-back compatibility (independent of output schemas — the paired get tools are upstream passthrough)**: per-pair fixture/contract tests for **dashboards** (get response subobject → update's `{id, dashboard}` wrapper), **views** (get → `{id, view}` + server-owned field stripping), **alerts** (version-dependent get shapes → update payload). Each test documents: response subobject, fields to strip, required wrapper. Notification channels excluded (envelope-get vs flattened-args-update; no adapter defined).

Tests: every allowlisted tool returns non-nil `StructuredContent` on every success path; exact schema inventory (exactly these 5 tools) asserted in B2.

### E. `server.WithStreamableHTTPLogger(m.logger)` (v0.55.0)
- Route SDK transport-level errors (SSE write failures, heartbeat/session goroutines, recovered transport panics) into our slog. No overlap with tool logs.
- Test: assert transport events reach our slog handler (wiring only — exact upstream wording is the SDK's to change and is deliberately not pinned).

### F. Raw-argument precision (v0.56.0) — tests-only unless a raw-wire test proves a bug
- **Contract (owner-confirmed): numeric params intentionally accept BOTH strings and numbers** (`"50"` and `50`); strings are transformed to numbers internally. Any change here must keep that contract.
- First step: a **raw-wire precision test** — JSON bytes with >2^53 integers through the production decode path (map-constructed handler tests cannot reproduce JSON's float64 rounding). **Unless that test demonstrates a real precision bug, F ships tests-only** (characterization of the dual-acceptance contract, no decode changes).
- If a real bug is found, fix per-field only, via a flexible type (custom unmarshaller accepting string and number); no blanket `json.Decoder.UseNumber()` (flips `map[string]any` value types from `float64` to `json.Number`, breaking every existing `.(float64)`/`.(string)` assertion); no `BindArguments` into concrete integer fields (rejects numeric strings). Both-forms (`"50"`/`50`) tests for every touched param.

### Deferred (follow-up PR, not this one)
- **SEP-414 `_meta` propagation (`WithMetaPropagator`)**: extraction replaces the active span context before dispatch; our HTTP middleware starts method spans *before* extraction and hooks end them via `trace.SpanFromContext(ctx)` — with `_meta` present, hooks would end the wrong (remote, non-recording) span and leak ours. Requires consolidating method-span ownership. Too invasive for this PR.
- `server.WithTracer`/`WithPropagator`: duplicate our existing method + tool spans / otelhttp header extraction; no "method spans only" option exists (`WithTracer` force-installs its tool middleware).
- `mcp-go/otel` module: separately versioned (tagged v0.54.0, not v0.56.0); we have no OTel LoggerProvider.
- Output schemas for mutation tools: blocked on post-mutation failure-semantics redesign (see D).
- Enforce-mode default flip: blocked on shadow soak + error-quality gate (see C).
- CORS, OAuth metadata helper, sessions/subscriptions/sampling/elicitation, `SchemaCache`, tool/prompt filters, `WithInteger` retrofits — unchanged from earlier assessment (stateless server, or we own the flow already).

## Principles alignment (internal MCP best-practices doc)
- **Accept-all-and-normalize**: shadow-first validation (C) and the dual string/number guard (F) exist specifically so no currently-normalized agent input starts failing. Enforcement waits for real-traffic evidence.
- **Contract is the defect, not the model**: shadow-mode mismatches are treated as schema bugs to fix (unions, alias declaration), not client errors.
- **Road to recovery in errors**: SEP-1303 message quality is a gate before enforce mode; rejection text must name the param and acceptable values.
- **Contract as executable promise**: validator consumes the exact advertised schema (single source); `query` alias stays accepted; read→write-back compatibility is specified per resource pair with contract tests (D).
- **Fewer, sharper tools / no token tax**: this PR adds zero tools and does not change advertised schemas except to declare already-accepted forms.

## Things that can break (checklist — verify each before merge)
1. **DNS-rebinding 403 (only default behavior change in the range)** — **RESOLVED**: deployment topology verified clean across staging + all three production clusters (single-container pod, no sidecars/mesh, ingress-nginx proxies to pod IPs, kubelet probes hit pod IP, AI assistant connects via public ingress URL). No loopback listener carries a public Host. Protection stays enabled, no opt-out flag. Local dev (localhost Host over loopback) remains allowed. Regression test in B3 guards future topology changes.
2. **Input validation (enforce mode) enforces declared types/enums**: params advertised as a single type but accepting alternate forms in handlers get rejected. Union types `{"type":["string","integer"]}` verified working in jsonschema/v6; every such param must declare the union. B1 catches drift; shadow mode catches real-traffic unknowns.
3. **Undeclared params stay accepted** (validated: `additionalProperties` unset → validator ignores extras) — but only while strict default stays OFF. Never add `WithStrictInputSchemaDefault()` without deprecating the `query` alias first.
4. **Malformed schema = silently disabled validation** for that tool (verified fail-open, no signal). B2 test + exact inventory makes it a CI failure.
5. **Validation bypasses our middleware in both directions**: input rejections produce no `execute_tool` span/log/analytics event; output validation can flip a middleware-logged success into a client-visible error. Mitigation: dedicated authoritative validation telemetry (C) + read-only output allowlist (D) + exactly-one-terminal-event tests.
6. **Output validation no-ops silently** on nil `StructuredContent`, error results, and uncompilable schemas — mitigated by runtime nil-StructuredContent signal (D), B2 compile test, exact inventory.
7. **Typed binding / decode hazards (F) — protects the intentional dual string/number input contract**: `UseNumber` flips map value types breaking type assertions; struct binding silently drops unknown/alias fields and rejects numeric strings into concrete int fields; input validation sees decoded `Params.Arguments` (float64-rounded) while `BindArguments` sees raw bytes. Raw-wire test first; tests-only unless bug proven.
8. **SDK transport logger volume**: heartbeat/session log noise at wrong levels could flood; E's level/fields test verifies mapping.
9. **New transitive deps** (`jsonschema/v6` → direct, `regexp2`): license check + binary/image-size delta vs current baseline (commands and acceptable threshold recorded in PR).
10. **Client compat (enforce mode, follow-up)**: SEP-1303 rejection message shape differs from current handler-specific error text; error-quality gate in C covers this before any default flip.
11. **Secrets in telemetry**: validation logs/metrics must never carry raw argument values or full validation payloads (webhook passwords, API keys); bounded normalized paths only.

## Files to Modify
- `go.mod`, `go.sum` — bump (done); `jsonschema/v6` becomes direct.
- `internal/config/config.go` — `MCP_INPUT_VALIDATION_MODE` (typed, startup-fail on unknown, default shadow) + tests.
- `internal/handler/tools/handler.go` — validation mode field on `Handler` (owns logger/meters already).
- `internal/handler/tools/schema_compat.go` — `addTool` → `h.addTool` method (update ~41 registration sites); registration-time schema compile + shadow/output-validation handler decoration.
- `internal/mcp-server/server.go` — `WithStreamableHTTPLogger`; no SDK validation options (validation is decorator-owned in `internal/handler/tools/schema_compat.go`).
- `pkg/otel/metrics.go` — `mcp.tool.validation.mismatches` / rejection metrics (bounded attrs).
- `internal/handler/tools/*` — output schemas for the read-only allowlist; nil-StructuredContent signal in `structuredResult` path (`errs.go`).
- New test files — normalization replay (raw-wire), schema-compile + inventory, DNS-rebinding regression, output success-shape, exactly-one-terminal-event, transport-logger fields, raw-wire precision.
- Docs sync per CLAUDE.md checklist: README (env table + any behavior notes), `manifest.json`, `docs/` — checked and outcome stated in PR summary. agent-skills: shadow mode changes no taught contract → expected no companion PR; state outcome.

## Verification (executable gates)
- **Build/tests**: `go build ./...` && `go test ./...` — zero failures.
- **Vet**: `go vet ./...` — empty output.
- **Format** (non-mutating, tracked/non-ignored files only — plain `-l .` recurses into ignored worktrees):
  - `git ls-files -co --exclude-standard -z -- '*.go' | xargs -0 gofmt -l`
  - `git ls-files -co --exclude-standard -z -- '*.go' | xargs -0 goimports -l`
  - both must print nothing (CI equivalent: primus `fmt` job).
- **Lint**: no pinned local command in this repo — the primus **`lint`** GitHub job (`signoz/primus.workflows/go-lint.yaml`) must pass; likewise **`fmt`**, **`test`**, **`deps`**, **`build`** jobs in `.github/workflows/ci.yaml`.
- **Tidy**: `go mod tidy && git diff --exit-code go.mod go.sum`.
- **Race**: `go test -race ./internal/handler/tools/... ./internal/mcp-server/...` (covers registration-time schema compile cache + per-call reads).
- **License** (pinned, no install required): `go run github.com/google/go-licenses@v1.6.0 report ./... | grep -E 'jsonschema|regexp2'` — expect Apache-2.0 (`santhosh-tekuri/jsonschema/v6`) and MIT (`dlclark/regexp2`); both acceptable.
- **Binary size** (two builds, then compare):
  - on `main`: `go build -o /tmp/mcp-main ./cmd/server`
  - on the branch: `go build -o /tmp/mcp-branch ./cmd/server`
  - `stat -f%z /tmp/mcp-main /tmp/mcp-branch`; record both in PR; acceptable delta ≤ +5 MiB.
- **Ordering**: B-tests land before C/D options are enabled in the same PR (commit ordering: tests first, then enablement).
- **Live smoke via subagent per CLAUDE.md**: copy an existing resource's shape rather than hand-crafting; never print or persist credentials; delete every created resource and confirm deletion; report which fields round-tripped. Exercise valid + deliberately invalid args on a representative tool set; confirm shadow mode never rejects and the mismatch metric fires; confirm SEP-1303 error text (enforce mode, staging only) is LLM-actionable.
- **Observability**: `mcp.tool.validation.mismatches` and nil-StructuredContent counters visible in SigNoz.
