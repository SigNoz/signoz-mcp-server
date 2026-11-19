# Plan: Service Dependency Map Tool

Status: In Progress
Issue: SigNoz/nerve-pod#371
PR:

## Context

Agents can list traced services (`signoz_list_services`) and one service's operations
(`signoz_get_service_top_operations`), but nothing returns the edges between services. To answer
"which services call this one", "what does this service depend on", or "which dependency is slow
or failing", an agent has to infer callers from client-span attributes with
`signoz_aggregate_traces`, which misses uninstrumented peers and merges destinations behind
generic span names. SigNoz already computes the call graph that its Service Map page draws.

This builds on SigNoz/signoz-mcp-server#271, which added the same tool on an older `main`.

Facts checked against SigNoz source at the pinned spec commit `0f598c49`:

- The route is `POST /api/v1/dependency_graph` (`pkg/query-service/app/http_handler.go`), with
  view access. The SigNoz UI's Service Map posts to it. The OpenAPI spec does not document it, at
  the pinned commit or on SigNoz `main` as of 2026-11-18.
- The body is `{"start","end","tags"}` with nanosecond epoch strings. SigNoz trims 30 s from an
  `end` within 30 s of now.
- The response is a bare JSON array of `{parent, child, callCount, callRate, errorRate, p50, p75,
  p90, p95, p99}`. `errorRate` is `sum(error_count)/sum(total_count)*100`, a percentage;
  `callRate` is calls per second over the window; the percentiles come from `duration_nano`, so
  they are nanoseconds.
- Edges come from per-minute rollups, and a window matches the minutes whose start falls inside
  it. Service edges come from a materialized view that pairs a parent span with a child span in
  a different service at ingestion; database and messaging edges use `db.system` and
  `messaging.system` as the child, from every non-server span, so a message system is the child
  of both producers and consumers.
- The tag filter keeps only `deployment.environment`, `k8s.cluster.name`, and
  `k8s.namespace.name`, and SigNoz skips any other key, an unknown operator, or a tag without
  `stringValues` without an error.

Confirmed on a local SigNoz v0.150.0-rc.1 instance (see Verification): the bare array, the units,
the minute matching, and the filter behavior. The live data added one fact the source does not
show plainly: service-to-service edges held 55 to 82% (60% overall) of the matching parent and
child spans, while database and messaging edges matched span counts exactly.

## Approach

Add one read-only tool, `signoz_get_service_map`, in the services group. The client calls the
route on the hand-written request path used for the other undocumented services routes, as a
replay-safe POST. The handler optionally keeps one service's edges in a direction, sorts edges
by parent then child for stable offset pages, wraps them in the shared pagination envelope, and
appends a note labelling units.

## Files to Modify

- `internal/client/client.go`, `interface.go`, `client_test.go`: `GetServiceMap` and its request test
- `internal/handler/tools/services.go`, `service_map_test.go`: registration, handler, validation, tests
- `internal/handler/tools/errs.go`, `feedback.go`: share the optional string argument helper
- `internal/mcp-server/testdata/wire-catalog/tools-list.json`: the new catalog entry only
- `tests/e2e/tests/test_service_map.py`, `tests/fixtures/telemetry.py`, `tests/README.md`: e2e suite
- `README.md`, `manifest.json`, `docs/architecture.md`: tool table, parameters, metadata, request path

## Key Decisions

### 2026-11-19 — Plain environment, cluster, and namespace parameters instead of tags

- Decision: the tool takes `environment`, `cluster`, and `namespace`, one optional string per
  rollup key (`deployment.environment`, `k8s.cluster.name`, `k8s.namespace.name`). Each set value
  becomes an `In` tag with that one value; the tool builds SigNoz's tag array itself. Each
  description points to `signoz_get_field_values` with the same key name for valid values.
- Rationale: SigNoz skips a filter on any other key, an unknown operator, or a tag without values
  without an error and returns the whole graph, which an agent would read as filtered (SCH-2). A
  JSON-encoded `TagQueryParam` string invites exactly those mistakes: agents have sent tags in
  other shapes to the top-operations tool, which drops them without an error. Plain parameters
  leave nothing to get wrong, and an undeclared `tags` argument gets the ignored-argument notice.
- Trade-off: no `NotIn` and one value per key, which keeps each parameter a plain value; either
  can be added later. On the live instance `Equals` failed with HTTP 400 and `Contains` and
  `StartsWith` matched nothing, so `In` and `NotIn` are the only operators that work.

### 2026-11-19 — Keep nanoseconds and label them

- Decision: pass the percentiles through in nanoseconds, state the units in the description, and
  append a units note to every result.
- Rationale: the other trace tools report latency in nanoseconds (`duration_nano`, the operation
  table), so agents compare like with like. #271 described the percentiles as milliseconds; the
  rollup stores `duration_nano`, and the e2e test pins nanoseconds against the pinned release.

### 2026-11-19 — Service and direction filters stay in the handler

- Decision: kept from #271. SigNoz has no service parameter for this route, so the handler filters
  the full graph. `direction` without `service`, or an unknown `direction`, is a validation error.
  When no edge touches the requested service, the result carries a note pointing to
  `signoz_list_services`, without echoing the name.

### 2026-11-19 — Say that service-to-service counts can be low

- Decision: the units note and README say that `callCount` and `callRate` on an edge between two
  services count only the calls SigNoz paired at ingestion, so they can be lower than a span
  count from `signoz_aggregate_traces`.
- Rationale: the tool returns what the Service Map shows, but an agent that compares an edge with
  a span count would otherwise report a discrepancy as missing calls. Error rates and latency
  percentiles agreed with the spans within a few percent.

### 2026-11-19 — Separate note for an empty graph

- Decision: when the window and filters leave no edges at all, the result says so and suggests
  widening the window or dropping the filters; the note about an unknown service name appears only
  when the graph has edges and none touches the service.
- Rationale: the live test filtered to an environment with no data and got the unknown-service
  note, which blamed the name.

### 2026-11-19 — Accept a status/data envelope with a WARN

- Decision: kept from #271. SigNoz returns a bare array from this route; newer routes wrap
  payloads. The handler accepts both, WARNs on the wrapped form, and returns `UPSTREAM_ERROR` for
  any other body rather than an empty graph.

## Reference Links

- [SigNoz/nerve-pod#371](https://github.com/SigNoz/nerve-pod/issues/371)
- [SigNoz/signoz-mcp-server#271](https://github.com/SigNoz/signoz-mcp-server/pull/271)

## Verification

- `GOTOOLCHAIN=go1.26.11 make ci`: every step passes (format, lint, deps, build, race tests,
  guardrails including the wire catalog, protocol and conformance, e2e style, repo docs). The
  protocol step ran with `MCP_PROTOCOL_PORT=18181` because another local service held 18080.
- Handler tests run through the real client against an `httptest` upstream: direction filtering
  and name order, the nanosecond window and the tags built from `environment`, `cluster`, and
  `namespace`, offset pages, the note for a service
  no edge touches, rejected filters that never reach SigNoz, and the envelope fallback. The
  client test pins the request body and the retry on a 503.
- `tests/e2e/tests/test_service_map.py` seeds a caller-to-callee call and reads the edge from
  both ends against the pinned release. Not run locally; it runs in the e2e CI job.
- Live, against a local SigNoz v0.150.0-rc.1 instance with the server built from this branch,
  window 2026-11-12 18:00 to 23:30 UTC, read-only calls:
  - Every edge the tool returned matched SigNoz's own dependency-graph response field for field.
    Callers of `checkout` returned `frontend -> checkout` (729 calls, 6.447% errors); callees
    returned its seven edges, including `kafka` as a message system; `checkout-v2` returned no
    edges and the unknown-service note.
  - Units: the edge p50 for `checkout -> payment` was 3.03 ms against 3.22 ms from the matching
    callee spans; `checkout -> cart` was 0.974 ms against 1.015 ms. A millisecond reading would
    put these at 50 minutes and 16 minutes. `callRate` equals `callCount` over the window in
    seconds.
  - Counts: `checkout -> payment` had 614 calls and 18 errors (2.932%) on the edge, against 1111
    matching parent and child spans with 31 errors (2.79%). `cart -> redis` matched exactly
    (18496).
  - Filters, called directly on SigNoz: `service.name`, `host.name`, an unknown operator, and a
    tag without values returned the unfiltered graph; `In` and `NotIn` on the three keys
    filtered; `Equals` returned HTTP 400; `Contains` and `StartsWith` returned nothing. The tool
    now sends only `In` on the three keys, the shape that filtered correctly here. The live test
    was not rerun after the switch from `tags` to plain parameters.
  - A window with no minute start inside it returned no edges; a 30 s window starting on a minute
    returned that whole minute.

## Outcome

In progress. The route is undocumented in SigNoz's OpenAPI spec; the tool uses the hand-written
request path until SigNoz documents it.
