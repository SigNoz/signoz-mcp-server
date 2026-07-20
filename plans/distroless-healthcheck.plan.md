# Plan: Distroless-safe MCP healthcheck

## Status

Done

## Context

The MCP image has no shell or `wget`, so an in-container `wget` health probe
always reports a healthy server as unhealthy.

## Approach

1. Add and test a native `healthcheck` binary subcommand that probes `/livez`.
2. Use that subcommand in the single- and multi-architecture Dockerfiles.
3. Update the Foundry compose template in a companion contribution.

## Files to Modify

- `cmd/server/healthcheck.go` — health probe implementation.
- `cmd/server/healthcheck_test.go` — status and transport regression tests.
- `cmd/server/main.go` — command dispatch before configuration startup.
- `Dockerfile` and `Dockerfile.multi-arch` — distroless-safe healthcheck.
- `README.md` — document container health behavior.

## Verification

- Run `go test ./cmd/server` and `go test ./...`.
- Build the production Docker image, run it with HTTP transport, and verify both
  Docker's native health status and the external `/livez` endpoint.

## Results

- `go test ./cmd/server` passed.
- `go test ./...` passed.
- The production Docker image reached Docker health status `healthy` with
  `TRANSPORT_MODE=http`; an external request to `/livez` returned `ok`.
