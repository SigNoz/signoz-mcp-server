# Plan: OAuth expired-workspace errors

## Status
In Progress

## Context
`/oauth/authorize` currently maps every non-401 credential-validation failure
to 502 `temporarily_unavailable` with "We couldn't reach that SigNoz instance.
Check the URL and try again." Live-telemetry investigation (nerve-pod#155)
shows 22 of 23 such failures in the last 7 days were expired/deactivated
SigNoz Cloud workspaces whose ingress returns an HTML 404 "workspace does not
exist" page — a permanent condition. The current message sends users down the
wrong recovery path, and the failures are not attributable per-tenant because
`mcp.tenant_url` is empty on the metric for this path.

## Approach
1. **Classify the dead-workspace signature in the client**
   (`internal/client/client.go`):
   - New sentinel `ErrInstanceNotFound`.
   - `evaluateValidationResponse`: status 404 with a non-JSON (HTML) body ⇒
     `ErrInstanceNotFound`. JSON 404s keep the generic "unexpected status"
     error (a real API 404 means service-account retry logic elsewhere).
   - `ValidateCredentials`: skip the `/api/v1/service_accounts/me` retry when
     `/api/v1/user/me` returned an HTML 404 — it is the ingress page, not a
     "key is a service account" signal.
2. **Branch in the OAuth handler** (`internal/oauth/handlers.go`):
   - `errors.Is(err, client.ErrInstanceNotFound)` ⇒ HTTP 404, error code
     `invalid_request`, message: "We couldn't find a SigNoz workspace at that
     URL. If your trial or subscription has ended, the workspace may have been
     deactivated — check your workspace status or contact SigNoz support."
     `FailureReason: instance_not_found`.
   - Remaining default case keeps 502 `temporarily_unavailable` but gains
     `FailureReason: instance_unreachable`.
3. **Fix tenant attribution**: seed `util.SetSigNozURL(r.Context(), normalizedURL)`
   into the request context immediately after the allowlist check, so the
   401, not-found, and unreachable failure metrics/logs all carry
   `mcp.tenant_url` (today only the allowlist rejection seeds it).

## Files to Modify
- `internal/client/client.go` — `ErrInstanceNotFound`, HTML-404 classification,
  skip pointless service-account retry on ingress 404.
- `internal/client/client_test.go` — cases: HTML 404 on user/me (no retry,
  `ErrInstanceNotFound`), JSON 404 → service-account retry preserved, HTML 404
  on the retry ⇒ `ErrInstanceNotFound`.
- `internal/oauth/handlers.go` — new branch + failure reasons + context
  seeding.
- `internal/oauth/handlers_test.go` — authorize-submit renders the permanent
  error for `ErrInstanceNotFound`; transient path keeps `temporarily_unavailable`.
- `plans/oauth-expired-workspace-errors.{context,plan}.md` — this pair.

## Verification
- `go test ./...`, `go vet`, lint.
- Fixture for the classifier is the real ingress 404 HTML captured from
  production logs (title "404 : This page does not exist :/").
- Docs/metadata sync: no MCP tool contract change ⇒ README/manifest/agent-skills
  unaffected; verified nothing under `docs/` references the old message.
