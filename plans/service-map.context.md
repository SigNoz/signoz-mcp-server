# Feature: Service Dependency Map Tool — Context & Discussion

## Original Prompt

> SigNoz's own UI has a Service Map feature — the call graph between services, including
> per-edge error rate and latency. The MCP server currently exposes `signoz_list_services`
> and `signoz_get_service_top_operations`, but nothing surfaces the actual dependency edges
> between services. This is a real gap for diagnostic workflows: answering "is the checkout
> failure caused by something downstream" requires knowing what checkout actually calls,
> which today requires leaving the chat to check the SigNoz UI directly.

Filed as SigNoz/signoz-mcp-server#269.

## Reference Links

- Issue: https://github.com/SigNoz/signoz-mcp-server/issues/269
- Route registration: `SigNoz/signoz` `pkg/query-service/app/http_handler.go:527`
- Request parsing: `pkg/query-service/app/parser.go:216` (`parseGetServicesRequest`), `:366` (`parseTimeStr`)
- Response item: `pkg/query-service/model/response.go:328` (`ServiceMapDependencyResponseItem`)
- ClickHouse query: `pkg/query-service/app/clickhouseReader/reader.go:861` (`BuildServiceMapQuery`)

## Key Decisions & Discussion Log

### 2026-08-03 — endpoint research

- The endpoint is `POST /api/v1/dependency_graph`, **not** `serviceMapDependencies` as
  guessed while writing the issue. Confirmed at `http_handler.go:527`.
- It is still the current path — the Service Map has no query-builder replacement in
  `main`, so this is not a v5-QB migration candidate.
- `start`/`end` are **strings containing nanosecond epoch**: `parseTimeStr` does
  `strconv.ParseInt` then `time.Unix(0, v)`. Both are required; missing values 400.
  This matches what `timeutil.GetTimestampsWithDefaults(args, timeutil.UnitNanos)` already
  produces for `signoz_list_services`, so no new time handling was needed.
- `end` is passed through `parseTimeMinusBufferStr` server-side, which subtracts a small
  recency buffer. Not compensated for client-side — the buffer is the backend's choice.
- The response is a **bare JSON array** (legacy `WriteJSON` path), not the
  `{"status":"success","data":...}` envelope that newer `render.Success` routes use.
- `errorRate` is a **percentage** (`sum(error_count)/sum(total_count)*100`), and quantiles
  are in **milliseconds**. Both documented in the tool description and README so an agent
  does not report a ratio as a percentage or vice versa.
- Source table is the `dependency_graph_minutes` family, so resolution is per-minute and
  narrow windows can legitimately return zero edges. Called out in the tool description
  because "no edges" reads as "no dependencies" to an agent otherwise.

### 2026-08-03 — design decisions

- **No output schema / typed edge struct.** Deliberate. `signoz_list_services` also passes
  upstream records through as `[]any`. `/api/v1/dependency_graph` is an undocumented
  internal endpoint, so mapping it into a fixed Go struct would silently drop any field
  SigNoz adds later. Passthrough preserves forward compatibility; the tradeoff is no
  `outputSchema` advertised to clients.
- **`service` + `direction` filtering is client-side.** The endpoint has no service
  parameter — it returns the whole graph. Filtering locally still helps: it keeps the
  agent's context small when the question is about one service, which is the common case.
- **`direction` without `service` is a validation error, not a silent no-op.** Ignoring it
  would let an agent believe it received a filtered graph and reason from a wrong premise.
  Same reasoning for rejecting an unknown `direction` value instead of defaulting.
- **`tags` is raw-JSON passthrough**, matching the existing
  `signoz_get_service_top_operations` convention rather than inventing a typed filter here.
- **Defensive envelope parsing + WARN.** Per the external-contract rule in CLAUDE.md, the
  parser accepts both the bare array and a `{"status","data"}` envelope, and logs a WARN
  when it sees the wrapped form. A fixture test alone would not catch that drift, and the
  failure mode without this is an empty graph an agent reads as "no dependencies" —
  exactly the silent degradation the rule targets.

## Open Questions

- [ ] Should the tool also emit `webUrl` deep links per edge? Deferred — the SigNoz UI
      route for a service-map edge (as opposed to a service) is not obvious, and guessing a
      URL shape is worse than omitting the field.
- [ ] Not verified against a live SigNoz instance: the bare-array envelope and the
      nanosecond `start`/`end` units are both read from source, not from a real response.
      Flagged in the PR body so a maintainer with an instance can confirm.
- [x] Full graph vs. one named service's dependencies (asked in the issue)? — Resolved:
      support both, via an optional `service` parameter, defaulting to the full graph.
