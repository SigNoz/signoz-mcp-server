# Feature: Alert Policy Routing and Completed Preflights — Context & Discussion

## Original Prompt
> Fix both confirmed alert workflow gaps and create a pull request:
> 1. Policy-routed alerts currently require a notification channel even when `notificationSettings.usePolicy=true`.
> 2. `signoz_update_alert` can repeat alert-get and notification-channel preflights that the conversation already completed.

## Reference Links
- [nerve-pod #160](https://github.com/SigNoz/nerve-pod/issues/160)
- [signoz-mcp-server #248](https://github.com/SigNoz/signoz-mcp-server/pull/248)
- [SigNoz backend channel validation](https://github.com/SigNoz/signoz/blob/main/pkg/query-service/rules/manager.go)

## Key Decisions & Discussion Log
### 2026-08-01 — Scope and behavior contract
- Keep this change limited to alert create/update validation and model-facing workflow guidance.
- Accept a payload with no referenced notification channels only when `notificationSettings.usePolicy` is `true`. Continue requiring at least one channel for direct routing.
- Continue validating every channel name that is supplied, including when policy routing is enabled, because the SigNoz backend validates referenced names even though policy routing does not use them for delivery.
- Make update prerequisites state-aware: fetch the current rule, read authoring resources, and list channels only when those steps have not already been completed with still-current results. Mutation-time validation remains the stale-state fallback.
- Treat this as a client-visible behavior correction. Synchronize tool metadata, README, manifest, and alert resources, and record the companion `SigNoz/agent-skills` assessment in the PR summary.
- Reference nerve-pod #160 without closing it because that umbrella issue tracks several unrelated remaining gaps.

### 2026-08-01 — Companion agent-skills contract audit
- The `signoz-creating-alerts` skill explicitly teaches that a notification channel is always required, so the policy-routing correction changes a contract the skill teaches and requires a companion `SigNoz/agent-skills` update under CMP-3.
- Update that skill to branch between direct routing (verified channel names required) and confirmed org-policy routing (`usePolicy=true`, no direct channel required), and add a focused policy-routing eval.
- The completed-preflight reuse wording does not require a companion skill change; no current skill instructs clients to repeat already-completed alert-get, resource, or channel-list calls.

### 2026-08-01 — Implementation and review closure
- The shared create/update validator now accepts a channel-less v2 threshold or PromQL payload only when `notificationSettings.usePolicy=true`; it still validates every supplied non-blank channel name. Direct and anomaly routing continue to require an existing channel.
- Recovery guidance is rule-aware: anomaly errors point to `preferredChannels` and never suggest policy routing, while active policy routing tells clients to remove invalid or blank direct references.
- Tool, field, resource, README, manifest, and initialize metadata now reuse reads only within the same still-current prepared operation and require a refresh when state may have changed.
- Independent review identified and verified fixes for unsafe global preflight reuse, blank supplied channel names, anomaly-incompatible recovery guidance, and stale mirrored resource metadata.
- The server passed focused tests, formatting/imports, workflow lint, guardrails, the full Go suite, `go vet`, build, manifest parsing, and diff checks. Agent CI passed the secret-free Inspector and contract workflows; five repository-secret-dependent jobs could not start locally, while their format/test/vet/build equivalents passed directly.
- The companion skill passed strict plugin validation and static checks. A baseline-versus-current policy-routing simulation showed the old skill blocking on direct-channel selection and the updated skill completing a policy-routed create; an independent grader passed the updated behavior.

### 2026-08-01 — Multi-agent review corrections
- A three-pass independent review found that the edited validation path still modeled direct v2 routing too loosely: SigNoz creates one route per threshold, so every direct-routing threshold needs its own valid channel list. Top-level `preferredChannels` is a v1/anomaly field and is not a v2 fallback.
- Current upstream v2alpha1 validation requires `condition.thresholds` even when `alertOnAbsent=true`; remove the local absent-only exception instead of forwarding a payload that upstream rejects.
- Scope every policy-routing description to v2 `threshold_rule`/`promql_rule`; anomaly rules remain v1 and require direct `preferredChannels`.
- Carry the same-operation/still-current qualifier into the alert resources and README summaries so full-replacement guidance cannot encourage reuse of an old rule snapshot.
- Add a negative companion eval proving that an unconfirmed org policy causes clarification rather than silently selecting policy routing. The broader lack of ETag protection and the channel list-to-write race remain out of scope because they are upstream/concurrency design risks, not regressions introduced by this feature.

### 2026-08-01 — Follow-up implementation and review closure
- Implemented the four scoped contract corrections and added focused regressions for v2 per-tier channels, v2 `preferredChannels` rejection, anomaly direct routing, anomaly policy rejection, and absent-only v2 rejection.
- The companion skill now stops before writing an exact absent-only request and includes both confirmed-policy and unconfirmed-policy evals. A final review caught one permissive `preferredChannels` sentence; it was corrected and the re-review was clean.
- Independent final server review found no remaining actionable issue. Formatting/imports, workflow lint, focused tests, guardrails, the full Go suite, `go vet`, build, manifest parsing, and diff checks passed.
- Agent CI passed the two secret-free jobs; five broader jobs could not start without repository secrets, while their local format/test/vet/build equivalents passed. No live tenant resources were mutated.

## Open Questions
- [x] Does `SigNoz/agent-skills` require a companion change for the corrected policy-routing and preflight-reuse contract? — Yes for policy routing; no for preflight reuse. Publish and link a focused companion PR.
