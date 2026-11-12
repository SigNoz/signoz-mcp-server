# Plan: Agent Feedback Tool

Status: Done
Issue: https://github.com/SigNoz/nerve-pod/issues/636
PR: https://github.com/SigNoz/signoz-mcp-server/pull/442

## Context

Agents have no way to tell us when a SigNoz MCP tool misled them. The server records failed calls
with error codes and `searchContext`, so we see that a call failed, but not what the agent expected,
what confused it, or which capability was missing. PlanetScale, Sanity, Executor, and Braintrust ship
a feedback tool for this; Sanity and Executor report that the reports catch gaps error telemetry
does not.

Two constraints came with the request:

- Self-hosted users get it on by default, with an opt-out, and nothing that identifies them. Self-hosted
  Grafana MCP, ClickStack, Executor, and SigNoz itself all report by default with an opt-out.
- SigNoz Cloud sets `ANALYTICS_ENABLED=true` and `SEGMENT_KEY`. That flow must keep its events, key,
  and identity unchanged.

Before this change the server sent nothing from a self-hosted install: `ANALYTICS_ENABLED` defaulted to
`false` and no release carried a Segment key.

## Approach

- One tool, `signoz_submit_feedback`, with a required `message` and optional `category` and
  `toolName`. Its description asks the agent to mask personal and sensitive data before sending and
  to tell the user what it sent.
- Analytics resolves to one of three states at startup (`analytics.ConfigFromEnv`):
  - the operator's own pipeline (`ANALYTICS_ENABLED=true` with `SEGMENT_KEY`): every event as today,
    plus the feedback event, with org and user identity;
  - anonymous (a release build with no opt-out): the feedback event only, attributed to an
    HMAC-SHA256 of the SigNoz org ID, with no instance URL, user, name, or email;
  - off (a build from source, `ANALYTICS_ENABLED=false`, or `DO_NOT_TRACK=1`): the tool returns a
    coded `UNSUPPORTED` error and sends nothing.
- Release builds carry the Segment write key through ldflags (`pkg/analytics.key`), as SigNoz and
  foundry do. A build from source keeps `<unset>`.
- The handler resolves the caller's org itself and hands the report to a sink the server injects, so
  the hand-off is synchronous: a call that cannot resolve the org fails with a coded error. Segment
  uploads in batches after the call returns, so the result says the report was queued, not delivered.

## Files to Modify

- `pkg/analytics/config.go`: build-time key, `ConfigFromEnv`, `AnonymousID`.
- `pkg/analytics/events.go`: the feedback event and its properties.
- `internal/config/config.go`, `cmd/server/main.go`: carry the resolved analytics config; log one
  line at startup when anonymous feedback is on.
- `internal/handler/tools/feedback.go`, `internal/handler/tools/register.go`,
  `internal/handler/tools/handler.go`: the tool and its sink.
- `internal/mcp-server/server.go`: deliver feedback in both modes; keep usage events off in
  anonymous mode.
- `.goreleaser.yaml`, `.github/workflows/binarybuildci.yaml`, `.github/workflows/dockerbuildci.yaml`:
  pass the `RELEASE_SEGMENT_KEY` repository variable into release builds.
- `manifest.json`, `README.md`, `internal/mcp-server/testdata/wire-catalog/tools-list.json`: the new
  tool entry and the analytics settings.
- `pkg/analytics/config_test.go`, `internal/handler/tools/feedback_test.go`,
  `internal/mcp-server/feedback_test.go`.

## Key Decisions

### 2026-11-13 — anonymous mode sends feedback only, not usage events

- Decision: in anonymous mode the only event is `MCP Feedback: Submitted`. Tool-call, prompt,
  resource, and OAuth events stay off.
- Rationale: data leaves a self-hosted install only on an explicit tool call the user can see.
  Background usage reporting from every self-hosted install is a separate policy decision.
- Alternatives rejected: sending every event with identity stripped. It is a small follow-up if we
  want it: `analyticsEnabled` is the one gate.

### 2026-11-13 — the operator's pipeline wins over the opt-out switches

- Decision: `ANALYTICS_ENABLED=true` with `SEGMENT_KEY` is resolved first. `DO_NOT_TRACK` and the
  build-time key do not affect it.
- Rationale: that is the SigNoz Cloud deployment, and its flow must not change. `DO_NOT_TRACK` is a
  switch against a vendor's default collection, not against a pipeline the operator configured.

### 2026-11-13 — the tool is always registered and reports when it is off

- Decision: register the tool in every build. Without a sink it returns `UNSUPPORTED` and tells the
  agent not to retry.
- Rationale: one catalog for every build keeps `manifest.json`, the wire catalog, and the inventory
  tests on the single registration path. Reusing `UNSUPPORTED` adds no error code (ERR-1).
- Alternatives rejected: registering the tool only when delivery is on.

### 2026-11-13 — annotations: not read-only, not destructive, not idempotent

- Decision: `withCreateToolAnnotations`.
- Rationale: each call sends one report outside the server, and a repeat sends a duplicate (SUR-7).

### 2026-11-13 — no server-instruction rule and no error-text hint yet

- Decision: the tool description carries all the guidance on when to report.
- Rationale: EVL-2 requires evidence for a change to server instructions, and a hint on upstream
  errors changes the text of every tool's failures. SUR-3 says to measure use first. The
  `MCP Feedback: Submitted` events and the existing tool-call metric give that measurement.

### 2026-11-13 — the anonymous ID is HMAC-SHA256 of the org ID with a fixed salt

- Decision: salt `signoz-mcp-server`, hex output, used as both the Segment user and group.
- Rationale: stable per org, not reversible, and not comparable with a hash of the same org ID made
  by another application. A machine ID, which foundry uses, is not stable in a container.

## Reference Links

- [PlanetScale: Send feedback and questions](https://planetscale.com/changelog/mcp-feedback-and-questions)
- [Sanity: How to get product feedback from agents](https://www.sanity.io/engineering/how-to-get-product-feedback-from-agents)
- [Braintrust: tracing your MCP server](https://docs.braintrust.dev/observe/mcp-servers)
- [Executor usage analytics](https://github.com/UsefulSoftwareCo/executor/blob/main/apps/docs/content/run/usage-analytics.md)
- [Grafana MCP anonymous usage statistics](https://github.com/grafana/mcp-grafana#anonymous-usage-statistics)
- [foundry ledger](https://github.com/SigNoz/foundry/blob/main/docs/ledger.md)

## Verification

- `go test ./pkg/analytics/ ./internal/handler/tools/ ./internal/mcp-server/`: the mode table, the
  tool contract (success, off, validation, rejected credentials), and the event each mode sends
  through the production HTTP handler. `AnonymousID` was checked once by hand against
  `openssl dgst -sha256 -hmac`; it has no pinned test, because the repo allows pins only for the wire
  catalog, guardrail budgets, and the docs baseline. The anonymous-mode test fails when identity attributes or usage events are added back.
- Binaries built three ways (from source, with an empty key, and with a key through the same
  single-quoted ldflags string the Docker workflow passes) and run in HTTP mode against a fake
  SigNoz. From source and empty key: `UNSUPPORTED`. With a key: the report is accepted, the startup
  line, and a Segment upload attempt. With a key and `DO_NOT_TRACK=1` or `ANALYTICS_ENABLED=false`:
  `UNSUPPORTED`. With `ANALYTICS_ENABLED=true` and `SEGMENT_KEY`: accepted, no anonymous startup line.
- `make ci`.
- Not run: goreleaser (the Pro distribution is not installed locally) and the primus Docker build.
  The ldflags template and the quoted flag string were checked by hand against goreleaser's `.Env`
  map and the primus `go-build` recipe.
- No e2e test: the tool's only SigNoz call is the existing identity lookup, and the e2e server is a
  build from source, where feedback is off.

## Outcome

Shipped as planned. Release builds stay silent until the `RELEASE_SEGMENT_KEY` repository variable is
set, because an empty key resolves to off. Deferred: nudges in server instructions and upstream error
text, a triage loop for the reports, and whether `SigNoz/agent-skills` should teach when to call the
tool; all tracked in the issue above.
