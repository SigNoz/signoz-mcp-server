# Plan: Upstream spec drives tool schemas, descriptions, and examples

Status: In Progress
Issue: SigNoz/nerve-pod#426
PR: https://github.com/SigNoz/signoz-mcp-server/pull/377

## Context

MCP PR #354 moved the alert and notification-channel tool schemas onto the generated SigNoz
client types, which carried no field descriptions, so the most complex tools shipped with bare
schemas and the teaching moved into hand-written `signoz://` resources and tool descriptions.
SigNoz/signoz#13194 (merged) and SigNoz/signoz#13200 (open, mergeable, 48 commits ahead of
v0.147.0 with nothing behind) describe every API payload field upstream and add operation-level
request examples for rules, saved views, dashboards, notification channels, query_range and the
metrics treemap. SigNoz/skaff#21 (released in skaff v0.0.7, current v0.0.9) emits those
descriptions as `jsonschema` struct tags, which `google/jsonschema-go` (the schema generator
behind `mcp.WithInputSchema[T]`) reads as field descriptions.

Measured on the #13200 head (`09bba8960dfc3e0a43dc54d6d7cf203aae9841f4`):

| Surface | Before | After regeneration |
|---|---|---|
| `signoz_create_alert` input schema | 2,190 bytes, 2 descriptions | 7,990 bytes, 42 descriptions |
| `signoz_create_notification_channel` | 771 bytes, 3 descriptions | 1,300 bytes, 6 descriptions |
| `dashboard_create.json` (raw, extracted) | 39,390 bytes, 11 descriptions (from v0.143.0) | 82,770 bytes, 338 descriptions |
| Upstream request examples | 0 consumed | 10 rules, 4+1 views, 2+1+2 dashboards, 10+2+2 channels |

The hand-written copies this makes redundant: field tables in `signoz://alert/instructions` and
`signoz://view/instructions`, the alert examples (hand-copied from upstream
`ruler_examples.go`), the saved-view and dashboard examples, the manual
`savedViewSchemaProperties()` schema, the notification-channel kind list in the tool
description, and the dashboard schemas extracted by a one-off script from a stale tag.

Open question resolved 2026-10-26: heatmap panels. `extract_schemas.py` stripped heatmap request
types because the UI had no renderer. v0.147.0 ships `DashboardtypesHeatmapPanelSpec` and the
frontend `PANEL_TYPES.HEATMAP`, and the AI builder query plugin is wired through
`persesQueryAdapters.ts`, so both are exposed as upstream publishes them.

## Approach

Construction before detection: one generator, run by `make gen`, derives everything that the
spec can state, and the hand-written surfaces keep only workflow teaching and the union
internals the typed schemas cannot express.

- Pin `SKAFF_VERSION=v0.0.9` and `SIGNOZ_SPEC_REF` to the #13200 head commit until a SigNoz
  release carries it, then bump to that release. The pinned spec is a strict superset of
  v0.147.0.
- `make gen` runs `skaff` for the client and `tools/specgen/specgen.py` for the spec-derived
  files: the three dashboard input schemas (now with descriptions, heatmap and AI builder
  plugins, and the standard `searchContext` handle) and one Markdown examples file per
  resource, rendered from the operation-level `examples` of the spec with provenance (spec ref,
  operation, example key and summary) and embedded with `go:embed`.
- Typed tools keep `mcp.WithInputSchema[T]` over the generated types, so descriptions flow with
  no runtime code. Saved views move onto the generated `PostableSavedView` and
  `UpdatableSavedView` (schemaVersion shadowed as optional because the handler forces v2);
  the `view` wrapper and the `id` parameter keep today's wire shape.
- Dashboards stay on raw extracted schemas because the Perses unions need `oneOf` with
  discriminators that struct reflection cannot express.
- Resources: `signoz://alert/examples`, `signoz://view/examples`, and `signoz://dashboard/examples`
  serve the upstream examples (hand-written preamble with the routing rules, generated body, a
  short hand-written addendum for the Cost Meter alert that upstream lacks). A new
  `signoz://notification-channel/examples` serves the channel examples plus a generated per-kind
  `config.spec` field reference, which is the only place the open `config` object's fields are
  described. Field-level facts the schemas now state are cut from the instructions resources;
  union internals (threshold tiers, evaluation specs, query envelopes) stay.
- Tool descriptions drop the volatile channel-kind catalog in favour of the generated resource
  and drop view facts the schema states.
- The wire catalog is updated entry by entry for the tools and resources this change alters.
- e2e: each examples resource is read over MCP and every example is created against the pinned
  SigNoz and deleted, with the channels the alert examples route to created first, so a
  release whose validator rejects a published example fails a test, not a user.

## Files to Modify

- `Makefile` — pins; `gen` runs the client generator and `specgen`; `gen-check` covers both.
- `tools/specgen/specgen.py` — replaces `internal/handler/tools/schemas/extract_schemas.py`.
- `internal/handler/tools/schemas/dashboard_{create,update,patch}.json` — regenerated.
- `internal/apiclient/**` — regenerated with skaff v0.0.9 from the pinned spec.
- `pkg/alert/examples.generated.md`, `pkg/views/examples.generated.md`,
  `pkg/dashboard/examples.generated.md`, `pkg/notificationchannel/examples.generated.md` — generated.
- `pkg/alert/resources.go`, `pkg/views/instructions.go`, `pkg/views/examples.go`,
  `pkg/dashboard/dashboard_examples.go`, `pkg/dashboard/widgets.go` — trim and embed.
- `pkg/notificationchannel/resources.go` — new resource content.
- `pkg/types/savedview.go` — typed view inputs over the generated types.
- `internal/handler/tools/views.go`, `notification_channels.go`, `alerts.go`, `dashboards.go` —
  registrations and descriptions.
- `internal/mcp-server/testdata/wire-catalog/*.json` — intended entries only.
- `manifest.json`, `README.md`, `docs/architecture.md`.
- `tests/fixtures/mcpclient.py`, `tests/e2e/tests/test_examples.py` — e2e over the examples.

## Key Decisions

### 2026-10-26 — Keep typed schemas for alerts, channels and views; raw schemas for dashboards only

- Decision: do not switch alerts, channels and views to raw spec extraction.
- Rationale: raw extraction renders the full unions with descriptions but costs 52 KB (rule),
  41 KB (view) and 22 KB (channel) per tool on every `tools/list`, against 8 KB, 1 KB and
  1 KB typed. Union internals are taught once in resources read on demand (RES-1).
- Alternatives rejected: raw extraction for every passthrough tool (size); hand-written union
  descriptions in Go (a hand copy of upstream, the construction gap this change closes).

### 2026-10-26 — Expose heatmap and AI builder plugins

- Decision: stop stripping heatmap from the dashboard schemas; keep the AI builder query plugin.
- Rationale: the pinned release renders both (frontend `PANEL_TYPES.HEATMAP`, `builder_ai_query`
  adapter), so the CLAUDE.md rule "only expose what the UI renders" no longer excludes them.

### 2026-10-26 — Pin the spec to a commit, not a release, for this PR

- Decision: `SIGNOZ_SPEC_REF` points at the #13200 head commit with a comment naming the
  follow-up bump.
- Rationale: the descriptions exist nowhere else yet; the commit's spec is a superset of
  v0.147.0, so no type regresses.

### 2026-10-27 — Retire the flat saved-view update body

- Decision: `signoz_update_view` rejects `source`, `spec`, `name`, `schemaVersion`, or
  `generateName` at the top level with a coded `VALIDATION_FAILED` error that names the `view`
  wrapper, instead of silently accepting them.
- Rationale: review finding on PR #377. The schema has required `view` since the wrapper landed,
  so schema-validating clients already rejected the flat shape while direct callers succeeded; the
  handler and the schema now agree, and the error carries the migration (CMP-2).
- Alternatives rejected: keep the silent flat path (two contracts for one tool); advertise
  the flat fields in the schema (a second copy of the generated view type).

## Reference Links

- [SigNoz/signoz#13200](https://github.com/SigNoz/signoz/pull/13200)
- [SigNoz/signoz#13194](https://github.com/SigNoz/signoz/pull/13194)
- [SigNoz/skaff#21](https://github.com/SigNoz/skaff/pull/21)
- [SigNoz/nerve-pod#426](https://github.com/SigNoz/nerve-pod/issues/426)

## Verification

- `make gen` with skaff v0.0.9 against the pinned spec: 290 generated files changed, 14 added
  (telegram channel, heatmap panel, AI builder query), 760 `jsonschema` tags, no description over
  1,024 bytes, `go.mod` unchanged.
- `make gen-spec` run twice produces byte-identical output.
- Rendered `tools/list`: `signoz_create_alert` 7,990 bytes with 42 descriptions,
  `signoz_create_view` 4,225 bytes with every spec field described, dashboards 85 KB each with 348
  descriptions; every tool stays under the guardrail budgets (`TestGuardrail_WireContractBudgets`
  passes, nesting depth unchanged).
- Wire catalog: recorded through the oracle and compared entry by entry; only the ten re-described
  tools and the eight altered or added resources differ from the committed catalog.
- `make ci` (fmt, lint, deps, build, race tests, guardrails, protocol, conformance, e2e style,
  repo docs): see Outcome.
- Live e2e (`make test-e2e`, ephemeral SigNoz cast by foundry): see Outcome.

## Outcome

- `make ci` passes every step on the change; the `check-repo-docs` step fails only on two untracked
  leftover plan files from earlier sessions that are not part of this change.
- Live e2e on a fresh SigNoz cast (2026-10-26): 53 passed, 1 failed. The failure was in the new
  channel test, which sent the upstream PUT body (already wrapped in `config`) as the tool's
  `config` argument; the test and the resource preamble now say to pass the body's `config`
  object; the rerun of that test on a fresh cast passed, so the full suite is green on the
  pinned release.
- Deferred: bump `SIGNOZ_SPEC_REF` to the first SigNoz release carrying #13200; propose the Cost
  Meter alert example and the metrics order-key rule upstream so the last two hand-written example
  blocks can go.
