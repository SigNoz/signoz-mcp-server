# Feature: Retire Live Resource Templates — Context & Discussion

## Original Prompt
> I'm still not clear when this resource_templates.go is actually useful compared to our existing tools and resources.
> Check using signoz-mcp-server tool if users are actually using them; if not, let's deprecate it altogether.

## Reference Links
- MCP surfaces: `internal/handler/tools/resource_templates.go`
- Runtime registration: `internal/mcp-server/server.go`
- Companion contract: `../signoz-ai-assistant/docs/agent-orchestration.md`

## Key Decisions & Discussion Log
### 2026-08-25 — production usage audit and retirement decision
- Queried the internal SigNoz workspace through the SigNoz MCP tools before changing the contract.
- Over 90 days, the alert summary handler ran 8 times and the dashboard summary handler ran once. The 8 alert reads were confined to one tenant and two short clusters on 2026-08-07; the dashboard read was a single call from another tenant on 2026-07-31. All were marked `mcp.client_source=user-client`, with no sustained use afterward.
- Equivalent tools were used routinely in the same 90-day window: 7,670 `signoz_get_alert`, 5,032 `signoz_get_alert_history`, and 3,577 `signoz_get_dashboard` calls.
- `resources/templates/list` volume is not adoption evidence: clients made 92,278 catalog-list calls in 30 days, while all resource reads totaled 4,200. Template listing is commonly automatic client discovery.
- Treat the nine clustered template reads as manual or compatibility probing rather than product adoption. Retire both dynamic templates and keep the static instructional resources unchanged.
- Removal is intentional despite the README's prior backward-compatibility note. The replacement tools remain available and provide richer inputs, structured results, coded errors, `webUrl`, and tool-call observability.

### 2026-08-25 — CMP-3 agent-skills audit
- Audited `SigNoz/agent-skills`; no skill, doc, fixture, or eval references either dynamic summary URI, including in repository history.
- Maintained dashboard workflows use `signoz_get_dashboard`; maintained alert workflows use `signoz_get_alert` and `signoz_get_alert_history`. Existing `signoz://dashboard/*` and `signoz://alert/*` references point only to static authoring resources that remain available.
- No `agent-skills` companion change is required.

### 2026-08-25 — signoz-ai-assistant compatibility audit
- Deployed `origin/main` has no summary-template dependency and does not call `resources/templates/list`.
- Resource support exists only in draft PR #437. Its execution policy requires an exact URI from `resources/list`, whose catalog does not contain the dynamic summary URIs, so the templates are already unreachable there.
- Local corrective work for PR #437 removes the dormant summary allowlist/parser and documents that any future template support needs a typed contract. No separate companion PR is required; PR #438 only needs its normal restack after #437 changes.

### 2026-08-25 — breaking-contract migration path
- The README records the intentional URI removal required by CMP-1/CMP-2. Dashboard reads move to `signoz_get_dashboard`; alert snapshots move to `signoz_get_alert` plus `signoz_get_alert_history` with its default six-hour window and `limit: 10`, `order: "desc"` for the closest prior behavior.
- `manifest.json` has no resource-template inventory, so no manifest edit is required. The release-generated changelog remains untouched.

### 2026-08-25 — implementation and verification complete
- Removed both live template handlers and production registration. Kept generic resource-template adapters and duplicate-registration guards so a future differentiated template remains possible without reviving these contracts.
- Runtime, HTTP/stdio matrices, the SDK-free wire oracle, integration tests, and the Inspector harness now pin an empty `resourceTemplates` array; direct reads of both retired URI shapes fail.
- Formatting, focused tests, all guardrails, the full Go suite, vet, build, module verification, Actionlint, ShellCheck, the Inspector lane, and all 12 selected conformance scenarios passed.

### 2026-08-25 — Opus review cleanup and shipped-plan status correction
- User confirmed that every plan still marked `In Progress` had already shipped. Corrected all 23 statuses to `Done` and appended one administrative context entry per pair.
- Claude Opus 5 high confirmed that completed plan bodies should remain as shipped history. Restored the four substantive older-plan hunks while retaining their append-only supersession notes.
- Kept generic template descriptor validation for future registrations and clarified the best-practices placement table so templates remain possible only under the stricter RES-1 rule.

## Open Questions
- [x] Are the live resource templates used enough to justify their public contract? — No; 9 clustered reads in 90 days versus 16,279 equivalent tool calls.
- [x] Should static alert/dashboard guidance resources also be removed? — No; they are actively referenced by tool descriptions and companion skills and serve a different progressive-disclosure purpose.
- [x] Does `SigNoz/agent-skills` need a companion change under CMP-3? — No; maintained skills do not reference either retired template and already use the replacement tools.
- [x] Does `SigNoz/signoz-ai-assistant` need a companion contract cleanup? — No separate change is required; deployed code has no dependency, and the existing draft resource PR already contains the relevant cleanup.
