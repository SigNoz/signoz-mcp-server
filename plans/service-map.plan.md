# Plan: Service Dependency Map Tool

## Status

Done

## Context

`signoz_list_services` and `signoz_get_service_top_operations` cover services individually,
but nothing exposes the edges between them. An agent asked "is the checkout failure caused
by something downstream" cannot answer without knowing what checkout calls, forcing the
user out of the conversation into the SigNoz UI's Service Map. Closes #269.

## Approach

Add one read-only tool, `signoz_get_service_map`, over
`POST /api/v1/dependency_graph`.

- **Client** (`internal/client/client.go`): `GetServiceMap(ctx, start, end string, tags
  json.RawMessage)`. POST carries only a body, so it is read-only and goes through
  `doReplaySafePost` — this makes it retryable, unlike a mutating POST. `start`/`end` are
  nanosecond epoch strings. A nil `tags` is normalized to `[]` because the backend rejects
  a null tag filter.
- **Handler** (`internal/handler/tools/services.go`, the service tool family): validate
  explicit timestamps, validate `direction`, resolve the window with
  `timeutil.UnitNanos`, call the client, parse defensively, optionally filter by
  `service`/`direction`, then paginate with `paginate.Array`/`paginate.Wrap` and return
  `listResult`.
- **Edges pass through as `[]any`**, not a typed struct, so fields SigNoz adds later are
  not silently dropped. Consequence: no `outputSchema`, matching `signoz_list_services`.
- **`parseServiceMapEdges`** accepts both the bare array the endpoint returns today and a
  `{"status","data"}` envelope, WARNing on the latter. The endpoint is undocumented, and
  the silent-failure mode — an empty graph an agent reads as "no dependencies" — is what
  CLAUDE.md's external-contract rule exists to prevent.
- **`direction` is rejected without `service`**, and unknown values are rejected, rather
  than degrading to an unfiltered graph an agent would misread as filtered.

## Files to Modify

- `internal/client/client.go` — add `GetServiceMap`
- `internal/client/interface.go` — add `GetServiceMap` to `Client`
- `internal/client/mock.go` — add `GetServiceMapFn` + method (defaults to `[]`)
- `internal/handler/tools/services.go` — register `signoz_get_service_map`, add
  `handleGetServiceMap`, `parseServiceMapEdges`, `filterServiceMapEdges`
- `internal/handler/tools/annotations_inventory_test.go` — pin `readTriple`
- `internal/handler/tools/services_test.go` — handler tests
- `internal/client/client_test.go` — `TestGetServiceMap` pinning method, path, and the
  nanosecond-string body contract
- `manifest.json` — tool entry
- `README.md` — summary-table row and per-tool detail section

Not modified: `register.go` (the tool joins the existing `RegisterServiceHandlers` group),
`schema_inventory_test.go` (no output schema), `nil_arguments_test.go` (every parameter is
optional, so nil arguments are a valid full-graph call).

## Verification

- `go build ./...`
- `go test ./...`
- `go test -count=1 -run '^TestGuardrail_' ./...`
- `make fmt goimports`
- Handler tests cover: unfiltered graph, all three `direction` values, `direction` without
  `service`, unknown `direction`, wrapped envelope, unparseable body, malformed timestamps.
- Client test pins method `POST`, path `/api/v1/dependency_graph`, nanosecond-string
  `start`/`end`, `tags` defaulting to `[]`, and 500/401 error propagation.
- **Not verified live.** The bare-array envelope and nanosecond units are read from the
  SigNoz source, not from a real instance response. Called out in the PR for maintainer
  confirmation.
