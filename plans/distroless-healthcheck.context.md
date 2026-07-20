# Feature: Distroless-safe MCP healthcheck — Context & Discussion

## Original Prompt

> Fix GitHub issue #246: the documented/Foundry Docker healthcheck runs `wget`
> inside the distroless SigNoz MCP image, where neither `wget` nor a shell exists.

## Reference Links

- [SigNoz MCP issue #246](https://github.com/SigNoz/signoz-mcp-server/issues/246)
- [Foundry compose template](https://github.com/SigNoz/foundry/blob/main/internal/casting/dockercomposecasting/templates/compose.yaml.gotmpl)

## Key Decisions & Discussion Log

### 2026-07-20 — Use the server binary as the probe

- Add a `healthcheck` subcommand that performs a short HTTP request to the
  existing local `/livez` endpoint and returns a process exit code.
- This keeps the production image minimal and works in a distroless runtime.
- Add the Docker healthcheck in both Dockerfiles. A companion Foundry template
  update will replace its external `wget` probe with the binary subcommand.

## Open Questions

- [ ] Does Foundry accept the companion template update while the MCP release
  containing this subcommand is pending?

### 2026-07-20 — Validation completed

- Added an integration check using the production `Dockerfile` with
  `TRANSPORT_MODE=http`.
- The container reported Docker health status `healthy`; the host-visible
  `/livez` endpoint returned `ok`.
- `go test ./cmd/server` and `go test ./...` both passed.
