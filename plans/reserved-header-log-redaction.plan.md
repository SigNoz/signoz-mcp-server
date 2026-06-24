# Plan: Reserved Header Log Redaction

## Status
Done

## Context
The SigNoz client allows tenant-specific custom headers, but prevents custom headers from overriding reserved headers such as `Content-Type` and the configured authentication header. The warning log for skipped reserved headers currently includes the attempted header value, which can leak credentials if a caller mistakenly supplies `SIGNOZ-API-KEY`, `Authorization`, or another secret-bearing reserved header in `customHeaders`.

## Approach
- Preserve the existing fail-open behavior: skip reserved custom header overrides and emit a warning.
- Remove the header value from the warning attributes so logs do not contain potential credentials.
- Add a regression test that exercises a reserved auth-header override and asserts the secret value is not present in captured logs while the warning still identifies the header name.

## Files to Modify
- `internal/client/client.go` — remove sensitive header value from the reserved-header warning log.
- `internal/client/client_test.go` — add a regression test for reserved custom-header log redaction.
- `plans/reserved-header-log-redaction.*` — record context and implementation plan.

## Verification
- `go test ./internal/client`
- `go test ./...`
