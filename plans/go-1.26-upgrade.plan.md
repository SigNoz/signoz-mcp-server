# Plan: Go 1.26 Toolchain Upgrade

## Status
Done

## Context

The repository declares Go 1.25.5 while the active local toolchain is Go
1.26. The builder image and all active CI lanes must agree with `go.mod`; a
partial update could test one language version and release another.

## Approach

1. Set the module minimum to `go 1.26.0` without adding a `toolchain`
   directive or changing dependencies.
2. Update the Docker builder and every active workflow Go selector from `1.25`
   to `1.26`, including reusable Primus workflow inputs and secret-free
   guardrail/protocol/manual-refresh jobs.
3. Update the README badge. Preserve historical references in completed or
   baseline planning documents.
4. Verify module tidiness without dependency upgrades, then run formatting,
   full tests, focused race coverage, vet, server build, Docker build, and the
   repository's local workflow runner where its credentials permit.

## Files to Modify

- `go.mod` — declare Go 1.26.0 as the minimum supported toolchain.
- `Dockerfile` — build with `golang:1.26-alpine`.
- `.github/workflows/ci.yaml` — use Go 1.26 in all reusable Go jobs.
- `.github/workflows/dockerbuildci.yaml` — use Go 1.26 for release image builds.
- `.github/workflows/guardrails.yaml` — use Go 1.26 in the contract lane.
- `.github/workflows/mcp-protocol.yaml` — use Go 1.26 in Inspector and conformance lanes.
- `.github/workflows/docs-index-refresh.yml` — use Go 1.26 in the manual corpus refresh.
- `README.md` — advertise the Go 1.26 minimum.

## Verification

- `go mod tidy -go=1.26.0 -diff` reports no unplanned dependency changes.
- `make fmt goimports`, `go test ./...`, focused `-race`, `go vet ./...`, and
  `go build ./cmd/server` pass under Go 1.26.
- Build the production Dockerfile with the new builder image.
- Run the local workflow runner; distinguish unavailable repository secrets
  from source failures. Confirm remote CI after push.
