# Plan: Failure Request Observability

## Status
In Progress

## Context
Schema-mismatch telemetry counts advisory contract drift, but operators cannot currently split it by `mcp.client_source` or recover the request that triggered it. Tool failure logs similarly carry the error and tool context but omit the request arguments needed for a local replay. Several secondary request-scoped metrics also omit caller attribution even though the request context already contains the bounded source value.

## Approach
1. Add a shared JSON-log helper that serializes the handler-visible request, recursively redacts credential-shaped fields, and applies a dedicated 1 MiB request-capture cap. Keep ordinary body and error log values at the established 4 KiB cap.
2. Attach the resulting `mcp.request` field to schema mismatches, missing-structured-output warnings, returned tool errors, and Go tool failures. Rate-limit repeated advisory validation WARNs per bounded mismatch tuple while keeping exact counters; registered handler errors emit the request once, and pre-handler failures retain hook-level capture.
3. Normalize `mcp.client_source` once at ingress to the bounded taxonomy `user-client`, `ai-assistant`, or `other`, then append it to all request-scoped metric paths that currently omit it: validation mismatches, missing structured content, auth failures, docs searches/fetches, and analytics identity cache hit/miss.
4. Preserve and explicitly test populated bounded validation dimensions (`gen_ai.tool.name`, `validation.direction`, `validation.path`, `validation.constraint`).
5. Add focused regressions for request capture, notification-credential and repeated-value redaction, a complete large-dashboard capture below 1 MiB, explicit truncation above 1 MiB, repeated mismatch suppression, registered/pre-handler tool failures, and metric attribution.

## Files to Modify
- `pkg/log/log.go` — safe structured-payload serialization/redaction helper.
- `pkg/log/handler_test.go` — redaction and truncation regressions.
- `pkg/util/context.go` — central bounded client-source normalization.
- `pkg/util/context_test.go` — client-source taxonomy regressions.
- `internal/handler/tools/handler.go` — hold the bounded-tuple validation WARN rate-limit state.
- `internal/handler/tools/schema_compat.go` — pass requests into validation telemetry, log each occurrence, and append caller attribution to request-scoped metrics.
- `internal/handler/tools/output_schema_test.go` — schema mismatch request/label/source regressions.
- `internal/handler/tools/docs.go` — append caller attribution to docs-tool metrics.
- `internal/client/client.go` — append caller attribution to identity-cache metrics.
- `internal/mcp-server/server.go` — attach failure requests to tool logs and append caller attribution to auth-failure metrics.
- `internal/mcp-server/server_test.go` — tool failure request and metric-attribution regressions.

## Verification
- `make fmt goimports`
- Focused: `go test ./pkg/log ./internal/handler/tools ./internal/mcp-server ./internal/client`
- Full: `go test ./...`
- Build: `go build ./cmd/server`
- MCP checklist: no client-visible contract changed; README, `manifest.json`, docs surfaces, and companion agent skills remain unchanged.
