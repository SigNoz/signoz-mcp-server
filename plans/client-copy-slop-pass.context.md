# Feature: Client-Facing Copy Slop Pass — Context & Discussion

## Original Prompt
> Let's ensure we update all descriptions and resources which are read by client with /no-ai-slop

## Reference Links
- `no-ai-slop` skill (local Claude skill) — editing rules applied
- `guardrails/README.md` — wire-catalog oracle is compare-only, no regeneration path
- `docs/mcp-best-practices.md` CMP-3 — agent-skills companion audit

## Key Decisions & Discussion Log
### 2026-08-20 — scan before editing
- Scanned every client-read surface (43 tools / 541 description strings, 22 resources,
  4 prompts, server instructions, manifest.json) for the skill's pattern list.
- Clean already: zero banned words, zero empty phrases, zero puffery, zero weasel
  attribution, zero throat-clearing, zero fake-strong verbs. Colon hits were all
  legitimate list/label colons, not fake-drama reveals.
- Remaining slop was concentrated in two places: em dashes used as prose connectors,
  and one templated block in `pkg/dashboard/widgets.go`.

### 2026-08-20 — scope boundary for em dashes
- Kept em dashes that act as **column separators in fixed-width reference tables**
  (`has_error  bool  — whether the span has an error`) and as **markdown heading
  separators** (`## 3. metric_promql — PromQL rule`). These are formatting
  conventions, not the rhythm crutch the rule targets, and rewriting them would
  churn alignment for no reader benefit.
- Rewrote every em dash acting as a prose connector.

### 2026-08-20 — upstream-owned strings excluded
- `internal/handler/tools/schemas/dashboard_patch.json` is generated verbatim from
  SigNoz's upstream OpenAPI spec (`extract_schemas.py`) and served via
  `WithRawInputSchema`. Its one em dash is upstream-authored; a local edit would be
  reverted on the next regeneration. Left as-is.
- `internal/handler/tools/dashboard_templates.json` titles mirror upstream dashboard
  names (`FluxCD — Controllers`). Left as-is.

### 2026-08-20 — widgets.go was the real find
- Five parallel `Note: This panel is best used ...` templates, trailing `-ing`
  analysis clauses (`revealing shape...`, `making it the primary panel...`,
  `stabilizing interpretation across charts`), abstraction (`temporal evolution`),
  empty closers (`in a compact, navigable format`), and one pure-puffery line
  (`It surfaces high-salience indicators that benefit from immediate readability,
  functioning as a KPI-style snapshot.`) which was deleted as redundant.
- Kept the per-panel parallel lead-in but shortened it to `Best for:`, matching the
  file's own existing `Best for:` idiom in the Query Type section. Parallel structure
  is correct for a scannable spec list; the boilerplate and puffery were not.

### 2026-08-20 — fixture strategy
- `guardrails/README.md` forbids re-recording the wire-catalog oracle. Textual
  fixtures were patched with the *same* string edits applied to source.
- Derived numeric fields (`size`, `serializedLength`, `sha256`) mechanically follow
  content length and cannot be hand-computed. They were read via a temporary,
  uncommitted dump test that reused the existing oracle helpers, then diffed:
  only those numeric lines changed, no structural/description/URI drift. Temp test
  deleted.

### 2026-08-20 — CMP-3 audit
- Wording-only change: no tool/parameter renamed or removed, no payload shape change,
  no documented behavior change. Grepped `SigNoz/agent-skills` for every edited
  string; zero matches. No companion skills PR required.

## Open Questions
- [x] Rewrite reference-table and heading em dashes too? — No. They are formatting
      conventions, not prose crutches; see scope boundary above.
- [x] Full README slop pass? — Out of scope (README is human-read, not client-read).
      Only the lines mirroring changed tool/param descriptions were synced, as
      CLAUDE.md requires. README-original prose still has ~21 em dashes and a few
      unspaced ones (`Irreversible—discover`); offered to the user as a follow-up.

### 2026-08-21 — second pass: writing-craft read (personal:writing skill)
- Re-read all 43 tool descriptions, server instructions, prompts, and guides for
  reader-effort problems (ambiguity, ordering, decoding) rather than slop patterns.
- Verdict: the catalog is structurally sound — consistent shape (purpose → sibling
  routing → caveats), consistent concept handles, sharpest constraint late. Six
  genuine first-pass-misreading fixes applied:
  1. Server instructions rule 7: reuse guidance arrived before the reader knew what
     the reads are. Reordered to context-first. First attempt reworded the
     "same still-current prepared operation" handle and tripped
     TestGuardrail_WireContractBudgets, which pins that phrase — correctly, since
     tool descriptions use the handle verbatim and rule 7 is its anchor. Final fix
     reorders only, keeping all three pinned phrases; guardrail unchanged.
  2. signoz_get_dashboard: dropped "respectively" (forces backward pair-mapping).
  3./4. signoz_delete_dashboard / signoz_delete_view: "views, which use
     signoz_delete_view" misattaches (views don't use the tool) → "delete those with".
  5. signoz_list_alerts: "for its timeline" had a fuzzy antecedent → "one rule's".
  6. signoz_query_metrics: "results use top 100" decoded poorly → "are capped at the
     top 100". Also widgets_examples.go: causal "since" → "because".
- Deliberately not edited: the dense preflight-reuse sentences in create/update_alert
  ("reuse ... only from the same still-current prepared operation; otherwise call it,
  refreshing only if state may have changed"). They are hard reading but encode
  reviewed contract semantics with a now-anchored handle; rewording risks drift.
- CMP-3: grepped agent-skills for every changed string; zero matches. No companion PR.

### 2026-08-21 — README prose pass (user approved the offered follow-up)
- Rewrote the ~21 remaining README em dashes: unspaced ones (`Irreversible—discover`,
  `gaps—they do not`, `config—never`) and prose connectors; converted the
  queryEnvelope definition-list bullets to colon form for consistency with the
  alert-guide doc links.
- Kept the two **Option A/B — ...** heading labels: label separators, not prose
  crutches, same boundary as table columns and markdown headings.
- Restructured the webUrl deep-links note so the condition (request carries an
  instance URL) precedes the definition instead of bracketing it in double em dashes.
- README-only; no client-visible string changed, so no fixture or skills impact.

### 2026-08-21 — codified the style as a CLAUDE.md convention
- Added a **Client-Visible Writing Style** section to CLAUDE.md (mirroring
  agent-skills PR #85's CONTRIBUTING.md section) so the rules from both passes
  outlive this PR: banned patterns, the two allowed em-dash separator uses,
  the concept-handle rule (reword everywhere or not at all; reordering around a
  pinned handle is fine), the upstream-generated exclusions, and the fixture/budget
  mechanics. Added a matching bullet to the Documentation & Metadata Sync Checklist.
- Cleaned CLAUDE.md's own prose em dashes in the same commit so the file follows
  the rule it states (title heading, quoted examples, and the grep literal remain).

### 2026-08-21 — review comments + writing-style extraction
- Extracted the Client-Visible Writing Style section from CLAUDE.md into
  `docs/client-visible-writing-style.md`. CLAUDE.md now points at that file so
  the always-loaded conventions stay short; the full rules load only when
  editing client-facing descriptions, resources, prompts, or instructions.
- Codex PR comment on rule 7: the writing-pass reorder put "resolve and fetch"
  before "reuse / do not repeat", which contradicted rule 1. Restored reuse-first
  wording while keeping the three pinned phrases (`same still-current prepared
  operation`, `do not repeat them`, `Refresh if state may have changed`).
- Codex PR comment on `signoz://dashboard/widgets-examples`: remaining prose
  em dashes on the edited resource (including the top-N limit explanation and
  example titles) rewritten as colons, semicolons, or parentheses. Zero em dashes
  remain in that file.
- Codex PR comment on the Done plan: rewritten to cover later passes (writing
  craft, README, CLAUDE.md, docs extraction, review-comment fixes) and to list
  `alerts.go`, `views.go`, `CLAUDE.md`, and `docs/client-visible-writing-style.md`.
