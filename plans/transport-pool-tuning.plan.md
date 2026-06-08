# Plan: Tune the shared HTTP transport connection pool

## Status
In Progress

## Context
All `SigNoz` clients share Go's global `http.DefaultTransport` (via
`otelhttp.NewTransport(http.DefaultTransport)`), so connection reuse to a SigNoz host is
already process-wide — the per-`(apiKey,URL)` client cache does not affect it. But the stdlib
defaults (`MaxIdleConnsPerHost=2`, `MaxIdleConns=100`) throttle keep-alive reuse under the
concurrency a multi-tenant MCP server sees: beyond 2 in-flight requests to the same host, idle
connections are closed rather than pooled, forcing fresh TCP+TLS handshakes. Raising these is
the actual lever for connection efficiency (vs the client cache, which is really a `/me`
identity cache).

## Approach
- Add a package-level `sharedTransport` in `internal/client/client.go`: clone
  `http.DefaultTransport` (preserves dial/TLS timeouts, proxy, HTTP/2), then set
  `MaxIdleConns=200`, `MaxIdleConnsPerHost=20`. Wire `NewClient` to
  `otelhttp.NewTransport(sharedTransport)` so every client shares the one tuned pool
  (single-pool property preserved — do not create per-client transports).

## Files Modified
- `internal/client/client.go` — `sharedTransport` var + use it in `NewClient`.
- `internal/client/client_test.go` — `TestSharedTransportPoolTuning` (guards the tuned values +
  that it's a proper `DefaultTransport` clone; the rest of the suite already drives requests
  through it).
- `plans/transport-pool-tuning.{context,plan}.md`.

## Verification
- `go build ./...`, `go vet ./...`, `go test ./... -count=1`, `go test -race ./internal/client/...` — green.
- Standalone PR off `main`.
- Future (optional): make limits env-configurable; observe connection-reuse / idle-FD metrics
  in prod to confirm the chosen values.
