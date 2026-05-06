# Plan: OAuth Auth Form URL Normalization

## Status
Done

## Context
Users can paste either a SigNoz Cloud hostname or a deep link from their SigNoz UI into the hosted MCP OAuth auth screen. The form previously required a strict base URL with a scheme and rejected useful inputs such as `tough-gecko.us.signoz.cloud` or `https://tough-gecko.us.signoz.cloud/home`.

## Approach
Add a form-specific normalization helper that:

- Trims whitespace.
- Assumes `https://` when the pasted value has no explicit scheme.
- Preserves explicit `http://` for self-hosted deployments.
- Rejects malformed `http:` or `https:` prefixes that do not use `//`.
- Strips path, query, and fragment suffixes before delegating to strict origin validation.
- Rejects userinfo through the strict validator.

Keep existing strict normalization for non-form callers.

## Files to Modify
- `pkg/util/url.go` - add form-specific normalization and userinfo rejection.
- `pkg/util/url_test.go` - cover protocol-less input, suffix stripping, malformed scheme input, userinfo rejection, and absolute URLs inside stripped query values.
- `internal/oauth/handlers.go` - use the form-specific normalizer in `HandleAuthorizeSubmit`.
- `internal/oauth/handlers_test.go` - verify a deep-link auth-form URL stores the stripped origin in the authorization code.
- `internal/oauth/static/authorize.html` - update form helper text.
- `README.md` - document hosted auth-form URL input behavior.

## Verification
- `git diff --check`
- `go test ./pkg/util ./internal/oauth ./internal/mcp-server`
- `go test ./...`
- Independent agent review of security, tests/docs/UX, and implementation compatibility.
