# Plan: Upstream Authz Error Classification

## Status
In Progress

## Context
MCP tools currently wrap all SigNoz backend client failures with `UPSTREAM_ERROR`. That is too broad for authorization failures such as upstream HTTP 403 `authz_forbidden`, because clients and agents treat the result like a generic backend outage instead of a permission issue.

## Approach
- Add a typed upstream HTTP error in the SigNoz client that preserves status code and raw response body while keeping the existing error string.
- Keep `HTTPStatusError.Body` parseable for long JSON envelopes; truncate only in `Error()` and log output.
- Teach `upstreamError` to classify upstream status classes aligned with SigNoz backend rendering: validation, unauthorized, permission denied, not found, conflict, rate limited, unsupported, license unavailable, canceled, and timeout.
- Include machine-readable structured fields such as `status`, `upstreamCode`, `upstreamType`, and `upstreamMessage`.
- For parseable upstream envelopes, keep raw JSON out of the human text block and show the backend message instead, avoiding downstream scanners that look for exact upstream `code` strings in text.
- Preserve caller wrapper context by replacing only the inner HTTP status text, and bound all returned upstream message/body text.
- Keep assistant auth-expired compatibility for upstream 401s through a nested `upstreamAuth.code` bridge, without exposing that bridge for upstream 403 permission denials.
- Treat parseable message-less upstream envelopes as status-only text rather than raw JSON; classify legacy query-service `503` timeout/canceled envelopes by `errorType`.
- Use `PERMISSION_DENIED` for HTTP 403 to avoid colliding with downstream auth-expiry checks that reserve exact upstream code `forbidden` for auth failures.
- Keep unknown statuses and generic backend failures on `UPSTREAM_ERROR` for backwards compatibility.
- Add focused tests for generic upstream errors and alert-create 403 behavior.

## Files to Modify
- `internal/client/client.go` — preserve status/body details on non-2xx backend responses.
- `internal/handler/tools/errs.go` — classify upstream auth failures and enrich structured content.
- `internal/handler/tools/errs_test.go` — pin helper-level structured content behavior.
- `internal/handler/tools/alerts_test.go` — pin `signoz_create_alert` 403 behavior.

## Verification
- Run focused Go tests for client/error/alert tool handling.
- Run broader relevant package tests if focused checks pass.
- Spin up independent review agents after implementation and address actionable findings before publishing.
- Verify staging alert create/delete through MCP with a real token; if the token has write permission, confirm cleanup and rely on unit coverage for the 403 branch.
