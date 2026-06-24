# Feature: Reserved Header Log Redaction — Context & Discussion

## Original Prompt
> [$improve](/Users/makeavish/.agents/skills/improve/SKILL.md) deep

## Reference Links
- None

## Key Decisions & Discussion Log
### 2026-06-24 — Deep improvement pass
- The requested local `improve` skill file was not available in the container, so the pass proceeded using repository instructions.
- During client request hardening review, `doRequest` was found to log the value of custom headers that attempt to override reserved headers such as `SIGNOZ-API-KEY` or `Authorization`. Those values can contain credentials and should not be emitted to logs.
- Decision: keep the warning signal for misconfiguration, but log only the header name and remove the header value from structured attributes.

## Open Questions
- [x] Should reserved-header override warnings include the attempted value? — No; the value may be sensitive and the header name is enough to diagnose the misconfiguration.
