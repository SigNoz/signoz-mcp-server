# Plan: Generated SigNoz API Client

Status: In Progress
Issue: [SigNoz/nerve-pod#26](https://github.com/SigNoz/nerve-pod/issues/26)
PR:

## Context

#26 ("Autogen APIs in MCP") started from a Slack thread about
[SigNoz/signoz#12621](https://github.com/SigNoz/signoz/issues/12621). Self-hosted SigNoz answers
unmatched `/api/*` paths with the SPA's `200 text/html`, and an auth proxy answers with a `200`
login page. `doRequest` accepts any 2xx (`internal/client/client.go:467`), so the agent receives
the HTML page as a successful tool result. The upstream fix,
[SigNoz/signoz#12815](https://github.com/SigNoz/signoz/pull/12815), is open, and older self-hosted
versions won't get it anyway.

The thread's direction (2026-09-21 and 2026-09-30):

- Fix it on the client side (Pandey).
- Use a generated client so endpoint and shape drift stop being hand-maintained (Pandey).
- Use our own generator, [SigNoz/skaff](https://github.com/SigNoz/skaff), rather than an external
  pipeline such as Cloudflare Forge (Vishal).

So #26 means a generated **client** under the curated tools. It does not mean generated **tools**.
Draft [signoz-mcp-server#151](https://github.com/SigNoz/signoz-mcp-server/pull/151) generates 136
tools. It forked 127 commits before current `main`, conflicts, and predates the current tool rules
(`searchContext`, string scalars, the wire catalog, guardrails, UI-renderable features). It
should be closed with a pointer to this plan.

This reverses the 2026-07-14 verdict in `plans/alert-history-v2-migration.context.md`, which
rejected skaff. That verdict assumed a generated client would replace our transport. The
Terraform provider shows otherwise (below).

### Survey: client endpoints against the v0.145.0 spec (2026-10-06)

Every SigNoz call in `internal/client/` was checked against `docs/api/openapi.yml` at
v0.145.0, the latest release. 35 of 37 operations are in the spec.

| Area | Operations (operationId) | In spec |
| --- | --- | --- |
| Identity | `GetMyServiceAccount`, `GetMyUser` | yes |
| Alerts | `GetAlerts`, `ListRules` (deprecated; target `ListRulesV3`), `GetRuleByID`, `CreateRule`, `UpdateRuleByID`, `DeleteRuleByID`, `GetRuleHistoryTimeline` | yes |
| Dashboards | `ListDashboardsV2`, `GetDashboardV2`, `CreateDashboardV2`, `UpdateDashboardV2`, `PatchDashboardV2`, `DeleteDashboardV2` | yes |
| Saved views | `ListSavedViews`, `GetSavedView`, `CreateSavedView`, `UpdateSavedView`, `DeleteSavedView` | yes |
| Notification channels | `ListNotificationChannels`, `GetNotificationChannel`, `CreateNotificationChannel`, `UpdateNotificationChannel`, `DeleteNotificationChannel`, `TestNotificationChannel` | yes |
| Query and fields | `QueryRangeV5`, `GetFieldsKeys`, `GetFieldsValues` | yes |
| Metrics | `ListMetrics`, `GetMetricsTreemap`, `GetMetricAttributes`, `GetMetricDashboardsV2`, `GetMetricAlerts` | yes |
| Org | `GetStats` | yes |
| Services | `POST /api/v1/services`, `POST /api/v1/service/top_operations` | **no** |

`POST /api/v2/services`, the target of `plans/2026-09-25-list-services-v2-endpoint.md`, isn't in
the spec either. The services calls stay hand-written until upstream documents them.

`ListRules` (`GET /api/v2/rules`, used by `signoz_list_alert_rules`) is marked deprecated in favor
of `ListRulesV3` (`GET /api/v3/rules`), which adds filtering, sorting, and offset pagination. Today
the handler fetches every rule and pages them locally (`internal/handler/tools/alerts.go:228`).
v3 takes these query parameters:

- `query`: a filter expression;
- `states`: a repeated state filter;
- `sort`: `updated_at`, `created_at`, `name`, `state`, or `severity`;
- `order`: `asc` or `desc`;
- `limit` and `offset`.

It returns `data.{rules, total, labels, reservedKeywords}`. Each v3 rule item has `alert`,
`alertType`, `createdAt`, `createdBy`, `description`, `disabled`, `id`, `labels`, `ruleType`,
`state`, `updatedAt`, and `updatedBy`. That covers every `AlertRuleSummary` field; severity comes
from labels, as today. The full-definition fields that v2 adds (`condition`, `evaluation`, and
others) aren't used by the summary.

### Spike: skaff v0.0.6 on all 35 operations (2026-10-06)

The scratch module declared the 35 operations in a `skaff.yml` and ran `skaff apitypes` and
`skaff client`.

- **Builds clean.** `go build ./...` and `go vet ./...` pass, with dependencies
  `oapi-codegen/runtime` and `oapi-codegen/nullable`. Every operation got a method, including the
  POST reads (`QueryRangeV5`, `GetMetricsTreemap`) declared as `data_sources`.
- **Size:** 279 type files (about 7.8K lines, 1.2 MB; 97 of them dashboard types) plus a
  6.7K-line client.
- **Time:** `apitypes` took 5.5 minutes; `client` took about 1 second. That's too slow to
  regenerate on every PR.
- **Config shape:** skaff only reads operations from Terraform-style `resources`
  (create/read/update/delete) and `data_sources` (read). Non-CRUD calls, such as PATCH and the
  test send, work only because the client step reads nothing but `path` and `method`.
- **Transport hooks:** `WithHTTPClient(HttpRequestDoer)` and `WithRequestEditorFn` are generated.
  So retries, size caps, tenant context, OTel spans, auth headers, and the User-Agent can live in
  a Doer we own.
- **HTML 200:** `Parse<Op>Response` fills `JSON200` only when `Content-Type` contains `json`.
  An HTML 200 page leaves `JSON200` nil with a nil error, so the caller must turn that into a
  coded error.
- **Strict decode:** for a JSON 200, `Parse<Op>Response` unmarshals into the generated types and
  returns an error if that fails. One upstream type change or unmodeled union would break the
  whole call. That violates our fail-open rule.
- **Unbounded read:** `Parse<Op>Response` calls `io.ReadAll` with no limit, and it decodes the
  full body even when we only pass it through.

### Reference: how the Terraform provider wires it

`terraform-provider-signoz` on `main`, in `internal/apiclients/`:

- `WrappedClient{Gen *ClientWithResponses}` wraps the generated client.
- `authDoer` sets auth, `Accept`, and the User-Agent, with retries from heimdall.
- A `Do` method is the escape hatch for endpoints that aren't in the generated client, such as
  legacy alerts and dashboards.
- `APIError` decodes `RenderErrorResponse`, with a capped raw fallback for proxy HTML.

Regeneration is on demand, not in CI. CI only formats, lints, builds, and tests the committed
`zz_generated_*` code. A `spec-syncer` Claude skill reruns the full pipeline (`run-pipeline.sh`,
through primus `make skaff`) against SigNoz `main` and opens a
`chore(codegen): sync generated code to upstream openapi spec` PR. These land every week or two
(#138 through #164, July to September 2026), mostly from `claude[bot]`. A `.claude/rules` file
forbids hand-editing generated files, but no check enforces it, and nothing gates drift.

### Resolved questions (2026-10-06)

None of these block phase 1.

1. **Terraform-style `resources` / `data_sources` sections:** cosmetic. `apitypes` and `client`
   read only `path` and `method` per entry, and the spike covered all 35 operations, including
   PATCH and the POST reads. A header comment in `skaff.yml` explains the naming. The pinned
   skaff version guards against stricter validation in a future skaff release.
2. **5.5-minute `apitypes` run:** acceptable. It's paid once per spec bump, plus the
   path-filtered `gen-check` job. Ordinary PRs don't run it.
3. **Plain `Client` only:** yes. oapi-codegen emits `Client`, which builds requests and returns
   `*http.Response`, and `ClientWithResponses`, which adds the strict `Parse<Op>Response`
   decode. We use `Client` only. `apitypes` is still required, because `Client` references 43
   of its types (request bodies and enum parameters), and skaff emits all 279 files.
   - A request-side-only option in skaff would be nice to have, not needed. If it lands, we
     regenerate and the unused response types disappear with no code change.
   - Handlers that decode responses keep using our own small structs (for example
     `types.APIAlertRule`), so nothing depends on generated response types.
4. **Spec pin:** v0.145.0, the latest release (CLAUDE.md). Bump it with each SigNoz release.
   Self-hosted users running an older SigNoz still depend on the runtime JSON check.
5. **skaff download token:** needed only for the CI `gen-check` job. Local `make gen` works
   with a `gh` login that can read `SigNoz/skaff`. Add the job once the secret exists.

## Approach

Generate a request-side client from the pinned spec with skaff. Keep the transport and response
handling ours. Move the areas over one at a time without changing any tool's contract.

- **Request side generated.** We would use the generated `Client` methods for paths, path
  escaping, query encoding, and typed parameters. Raw-passthrough writes (dashboards, rules)
  would use the `WithBody` variants, so request bytes stay exactly what the tool built.
- **Response side ours.** One shared response path would replace the strict decode:
  - a size cap;
  - a JSON check on 2xx, which turns HTML or a login page into a coded error and a WARN log;
  - the existing `ParseUpstreamErrorBody` and `upstreamError` mapping for non-2xx, keeping
    401/403 propagation.

  Tools would keep passing raw `json.RawMessage` through. Typed decoding is opt-in per call,
  only where a handler already parses fields, and always fails open with a WARN log.
- **Transport in a Doer.** The current retry, replay-safety, timeout, tenant, OTel, and header
  logic would move from `doRequestWithReplayPolicy` into a Doer behind `WithHTTPClient`.
- **Escape hatch.** Operations missing from the spec (the services calls) would keep a
  hand-written `Do`, as in the Terraform provider.
- **Regenerate per bump, not per PR.** `make gen` would run the pinned skaff against the
  pinned spec, and the output is committed. It runs when the spec pin, the skaff pin, or
  `skaff.yml` changes, which follows SigNoz releases, so the 5.5 minutes are paid once per bump.
  This matches the Terraform provider.
- **Gate drift only where it can happen.** Unlike the Terraform provider, `make gen-check`
  (regenerate, then fail on a diff) would run in a path-filtered job: only on PRs that touch
  `skaff.yml`, the pins, or `zz_generated_*` files, plus a weekly scheduled run. It catches
  hand edits and stale output without slowing ordinary PRs, so it stays out of `make ci`.
- **No contract change.** Tool names, schemas, outputs, and error codes would not change, and
  the wire catalog would not move. If a migrated area changes a wire byte, that's a bug.

### Phases

1. **Scaffolding:** `skaff.yml`, pinned skaff and spec versions, `make gen` / `gen-check`, the
   Doer, and the shared response path. `GetOrgOverview` moves as the first caller (see Key
   Decisions).
2. **Migrate by area, one PR each:** identity and org, then fields and query, metrics, saved
   views, notification channels, alerts, and dashboards last (the largest type surface). The
   alerts PR moves `signoz_list_alert_rules` to `ListRulesV3` (see Key Decisions).
3. **Close-out:** remove dead hand-written request builders and update `docs/architecture.md`.

## Files to Modify

- `skaff.yml` (new): declared operations.
- `Makefile`: `gen`, `gen-check`, and pinned `SKAFF_VERSION` / spec ref (not part of `ci`).
- `internal/apiclient/` (new, generated): `zz_generated_client.go` and `apitypes/`. golangci-lint
  skips them through oapi-codegen's `Code generated ... DO NOT EDIT.` header.
- `internal/client/doer.go` (new): the transport, moved out of `doRequestWithReplayPolicy`.
- `internal/client/api.go` (new): `callAPI` and `ErrNonJSONResponse`.
- `internal/client/client.go`: `doRequestWithReplayPolicy` builds the request and calls the Doer;
  callers move per phase.
- `internal/client/org_overview.go`, `internal/handler/tools/org_overview.go`: first migrated
  caller, and the route-unavailable recovery for an HTML 200.
- `.github/workflows/`: path-filtered and weekly `gen-check` job, added once a skaff download
  secret exists.
- `docs/architecture.md`, `CLAUDE.md`: client layering, `make gen`.

## Key Decisions

### 2026-10-06 — Generate a client, not tools

- Decision: #26 is delivered as a generated client under the curated tools. Close #151.
- Rationale: this is what the Slack thread asked for. Generated tools would bypass the
  tool-contract rules and multiply the tool count, which works against the size budget work.
- Alternatives rejected: rebasing #151.

### 2026-10-06 — Regenerate per bump; gate drift only on generator-input PRs

- Decision: commit generated code, and regenerate when the spec pin, the skaff pin, or
  `skaff.yml` changes. `gen-check` runs in a path-filtered and weekly job, not in `make ci`.
- Rationale: `apitypes` takes 5.5 minutes. The Terraform provider regenerates on demand with no
  CI gate. We add the narrow gate because CLAUDE.md requires drift to fail a test.
- Alternatives rejected: `gen-check` on every PR (too slow); no gate at all (hand edits and
  stale output go unnoticed).

### 2026-10-06 — Target `ListRulesV3`, not the deprecated `ListRules`

- Decision: `skaff.yml` declares `GET /api/v3/rules` and not `GET /api/v2/rules`.
  `signoz_list_alert_rules` moves to v3 with its parameters and output unchanged. `limit` and
  `offset` are passed to the server instead of being applied locally. No separate issue: v2 will
  be removed upstream, so this work is part of the migration.
- Rationale: generating a client for an operation that's already deprecated just bakes in a
  removal. v3 pages on the server, so a large workspace stops downloading every rule for one
  page. Once v2 is removed, self-hosted servers would serve HTML 200 for it.
- Timing: stays bundled with the phase 2 alerts PR rather than shipping now as a hand-written
  change. The user expects v2 to stay upstream for a long time. v3 first appears in v0.144.0.
- Follow-up, not in this plan: exposing v3's `query`, `states`, `sort`, and `order` as tool
  parameters. That's an additive contract change and goes through the CLAUDE.md tool checklist.
- To verify in that PR: a real v3 response round-trips through the summary, and server-side
  `limit`/`offset` and `total` match today's local paging (e2e).

### 2026-10-06 — Phase 1 moves `GetOrgOverview` as the first caller

- Decision: phase 1 also moves `GetOrgOverview` (`GET /api/v1/stats`) to the generated client.
  `signoz_get_org_overview` adds the existing route-unavailable recovery text to an HTML 200,
  as it does for a 404.
- Rationale: without a caller, the generated client and `callAPI` would ship as dead code
  tested only in isolation. Org overview is a single GET with no parameters, and it already
  treats a missing route as an expected condition.
- Effect: an HTML 200 from `/api/v1/stats` now returns `upstream_error` with a message naming
  the likely causes. Before, the handler failed to parse the HTML and reported an unexpected
  response shape. Other tools keep today's behavior until their area moves or the deferred HTML
  guard ships.

### 2026-10-06 — The generated client builds requests and never validates

- Decision: request bodies always go through the generated `WithBody` variants with the exact
  bytes the tool built, never through the typed `JSONRequestBody` structs. Responses aren't
  decoded through generated types. SigNoz stays the only validator.
- Rationale: a typed body drops any field the pinned spec lacks when it's marshalled, so
  requests would lag a newer SigNoz. The generated code itself doesn't validate:
  - the 92 enum `Valid()` methods are never called;
  - enums are plain string types, so newer values pass;
  - query parameters are only encoded.
- Drift handling:
  - SigNoz newer than the pin: new response and body fields pass through. A new query
    parameter waits for a regeneration, which would also expose it in the tool.
  - SigNoz older than the pin: a missing route returns a coded 404 or `ErrNonJSONResponse`.
    Unknown fields or values get SigNoz's own 400, surfaced as `validation_failed`.
  - An operation renamed or removed upstream shows up at the next `SIGNOZ_SPEC_REF` bump as a
    `make gen` diff and compile errors.

### 2026-10-06 — Detect a web page by content type or leading `<`

- Decision: `callAPI` rejects a 2xx whose `Content-Type` contains `html`, or whose body starts
  with `<` after whitespace and a BOM (the existing `isHTMLBody`). It doesn't require
  `application/json`.
- Rationale: a missing `Content-Type` on a JSON body must keep working. A JSON body never starts
  with `<`, so the body check has no false positives on real payloads, and it also catches a
  login page served as `text/plain`.

### 2026-10-26 — Finish phases 2 and 3 in one PR

- Decision: every operation the spec documents moves to the generated client in one PR, and the
  per-endpoint URL builders go with it. `doRequest` stays only as the escape hatch for the two
  services endpoints, and it now shares `callAPI`'s response path, so the HTML 2xx guard covers
  every call. The v1 `ListNotificationChannels` wrapper had no caller and is deleted.
- Rationale: #377 made the generated types the source of the tool schemas, so the client was the
  last hand-maintained copy of the API surface. Moving one area at a time would have left the
  two request paths side by side for several more PRs with nothing to learn in between.
- Alternatives rejected: keeping `doRequest` for areas with a generated method (the plan's
  original per-area phasing).

### 2026-10-26 — Walk v3 rule pages in the handler

- Decision: `signoz_list_alert_rules` keeps its `limit` up to 1000 and its `offset`. The client
  returns one `GET /api/v3/rules` page, and the handler requests pages of 200 until it has the
  window, taking `total` from the server.
- Rationale: SigNoz clamps the v3 `limit` to 200 (`ruletypes.MaxListLimit`) instead of rejecting
  it. Passing the tool's limit straight through would have silently shortened pages and broken
  the tool's contract for workspaces with more than 200 rules.
- Alternatives rejected: clamping the tool's limit to 200 (a breaking contract change for one
  internal reason); keeping the deprecated v2 list, which would have needed a new `skaff.yml`
  entry for an operation upstream is removing.

### 2026-10-26 — Append undocumented query parameters with a request editor

- Decision: `GET /api/v1/alerts` keeps the Alertmanager filter parameters the tool exposes
  (`active`, `filter`, `inhibited`, `receiver`, `silenced`) by appending them to the generated
  request through a `RequestEditorFn`. Credential validation builds its requests with the
  generated constructors but keeps its own non-retrying send.
- Rationale: the spec documents the route but not those parameters. The editor keeps the path
  and method generated and isolates the gap to one line, which disappears when the spec adds
  them. Propose the parameters upstream.

### 2026-10-06 — Use skaff, keep transport and response handling ours

- Decision: skaff generates requests; our Doer and shared response path own the transport and
  response handling.
- Rationale: owner preference for our own generator. The spike shows the Doer hook works. The
  strict, unbounded decode in `Parse<Op>Response` conflicts with fail-open passthrough.
- Supersedes: the 2026-07-14 skaff verdict in `plans/alert-history-v2-migration.context.md`.

## Reference Links

- [nerve-pod#26](https://github.com/SigNoz/nerve-pod/issues/26)
- [signoz-mcp-server#151](https://github.com/SigNoz/signoz-mcp-server/pull/151) (draft, to close)
- [terraform-provider-signoz#85](https://github.com/SigNoz/terraform-provider-signoz/pull/85) (closed; skaff now on TF `main`)
- [SigNoz/signoz#12621](https://github.com/SigNoz/signoz/issues/12621), [SigNoz/signoz#12815](https://github.com/SigNoz/signoz/pull/12815)
- [SigNoz/skaff](https://github.com/SigNoz/skaff) (private)
- `plans/2026-09-25-list-services-v2-endpoint.md`

## Verification

The survey and spike above ran on 2026-10-06 against v0.145.0 and skaff v0.0.6.

Phase 1 (2026-10-06, local):

- `make gen` took 4m48s and wrote 284 type files plus a 6.8K-line client. A second run was
  byte-identical, so `gen-check` won't report false drift.
- `make ci` (with `GOTOOLCHAIN=go1.26.0`):
  - pass: `check-fmt`, `lint` (0 issues; generated files are skipped), `check-deps`,
    `check-build`, `test-race`, `check-guardrails`, `check-e2e-style`;
  - not run: `check-protocol` and `check-conformance`, because GNU `timeout` isn't installed
    locally (CI runs both);
  - failing for unrelated reasons: `check-repo-docs`, only on older untracked plans.
- All existing `doRequest`, retry, and guardrail tests pass on the Doer unchanged.
- New tests:
  - an HTML 200 (by `Content-Type`, and by body with `text/plain`) returns
    `ErrNonJSONResponse`, and the WARN log omits the page;
  - base-URL path prefixes survive, with and without a trailing slash;
  - `signoz_get_org_overview` adds route-unavailable recovery text to an HTML 200.
- The HTML test fails when the check is disabled.

Phases 2 and 3 (2026-10-26, local, branch `refactor/client-generated-requests`):

- `make ci` with `GOTOOLCHAIN=go1.26.0`: every step passes except `check-repo-docs`, which fails
  only on older untracked plans outside the PR.
- The client package's httptest tests pass unchanged apart from two that pinned the old
  transport: the rules list test now expects `GET /api/v3/rules?limit=200&offset=400`, and the
  channel list test compares parsed query values, because the generated encoder keeps
  declaration order rather than sorting keys.
- New `TestListAlertRules_AssemblesWindowFromUpstreamPages` runs the real client against an
  httptest SigNoz that pages at 200: a window of 300 from offset 100 sends
  `limit=200&offset=100` then `limit=100&offset=300`, returns the 300 rules in order, and reports
  the server's total of 450.
- The wire catalog did not change: no tool schema, description, or resource moved.
- e2e: see the PR for the run against the pinned release.

## Outcome

Done in two PRs. Phase 1 (scaffolding, `callAPI`, the `doer`, org overview) merged earlier.
Phases 2 and 3 are in the PR linked above: every operation in `skaff.yml` goes through the
generated client, `doRequest` remains only for the two services endpoints, and the hand-written
URL builders, the v1 channel list wrapper, and `AlertHistoryRequest.QueryParams` are deleted.
`signoz_list_alert_rules` reads `GET /api/v3/rules` through 200-rule pages with the same tool
contract.

The HTML 2xx guard deferred on 2026-10-06 is no longer separate work: `doRequest` now shares
`callAPI`'s response path, so a web page served as 2xx is a coded error on every call.

Deferred todos:

- **Services endpoints missing from the spec:** ask upstream to document `/api/v2/services`.
- **Alert filter parameters missing from the spec:** propose `active`, `filter`, `inhibited`,
  `receiver`, and `silenced` on `GET /api/v1/alerts`, then drop the request editor.
- **v3 list filters as tool parameters** (`query`, `states`, `sort`, `order`) remain a separate
  additive contract change.
