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

Three constraints came with the request:

- Self-hosted users get it on by default, with an opt-out, and nothing that identifies them. Self-hosted
  Grafana MCP, ClickStack, Executor, and SigNoz itself all report by default with an opt-out.
- SigNoz Cloud sets `ANALYTICS_ENABLED=true` and `SEGMENT_KEY`. That flow must keep its events, key,
  and identity unchanged.
- Feedback is not analytics. It gets its own key and its own switch, and the analytics settings keep
  the single meaning they have today.

Before this change the server sent nothing from a self-hosted install: `ANALYTICS_ENABLED` defaulted to
`false` and no release carried a Segment key.

## Approach

- One tool, `signoz_submit_feedback`, with a required `message` and optional `category`, `toolName`,
  and `model`. Its description asks the agent to mask personal and sensitive data before sending and
  to tell the user what it sent.
- Feedback has its own Segment source. Release builds carry that source's write key through ldflags
  (`pkg/analytics.feedbackKey`), as SigNoz and foundry do. A build from source keeps `<unset>` and
  sends nothing.
- One switch, `FEEDBACK_ENABLED` (default `true`). With it off, or without a key, the tool returns a
  coded `UNSUPPORTED` error and sends nothing.
- `ANALYTICS_ENABLED` and `SEGMENT_KEY` are untouched: `pkg/analytics/config.go`, the usage events,
  and their gate are the same as before this change. Feedback never goes to the usage analytics
  source, and usage events never go to the feedback source.
- Every report carries a `deployment` property. The MCP server SigNoz hosts sets
  `MCP_DEPLOYMENT=cloud`; its reports carry the org, user, and instance URL its usage events carry.
  Every other deployment is `self_hosted`, and its reports are attributed to an HMAC-SHA256 of the
  SigNoz org ID, with no instance URL, user, name, or email.
- The handler resolves the caller's org itself and hands the report to a sink the server injects, so
  the hand-off is synchronous: a call that cannot resolve the org fails with a coded error. Segment
  uploads in batches after the call returns, so the result says the report was queued, not delivered.

## Files to Modify

- `pkg/analytics/feedback.go`: build-time key, `FeedbackConfigFromEnv`, `AnonymousID`.
- `pkg/analytics/events.go`: the feedback event and its properties.
- `internal/config/config.go`, `cmd/server/main.go`: carry the feedback config; build the feedback
  Segment client and log one line at startup when feedback is on.
- `internal/handler/tools/feedback.go`, `internal/handler/tools/register.go`,
  `internal/handler/tools/handler.go`: the tool and its sink.
- `internal/mcp-server/server.go`: `EnableFeedback` and delivery for both deployments.
- `internal/client/client.go`: the identity lookup returns a typed status error, so the tool can code
  a 401 and a 403 apart.
- `.goreleaser.yaml`, `.github/workflows/binarybuildci.yaml`, `.github/workflows/dockerbuildci.yaml`:
  pass the `FEEDBACK_SEGMENT_KEY` repository variable into release builds.
- `manifest.json`, `README.md`, `internal/mcp-server/testdata/wire-catalog/tools-list.json`: the new
  tool entry and the feedback settings.
- `pkg/analytics/feedback_test.go`, `internal/handler/tools/feedback_test.go`,
  `internal/mcp-server/feedback_test.go`.

## Key Decisions

### 2026-11-13 — feedback is separate from analytics

- Decision: feedback has its own Segment source, build-time key, and switch (`FEEDBACK_ENABLED`). It
  does not read `ANALYTICS_ENABLED`, `SEGMENT_KEY`, or `DO_NOT_TRACK`.
- Rationale: the first version resolved the three variables into operator, anonymous, and off modes.
  Each variable then picked both the destination and how much identity was attached, and
  `DO_NOT_TRACK` turned off one mode but not the other. With a separate path each setting has one
  job, and the Cloud analytics code is the same as before the change.
- Alternatives rejected: the three-mode resolver; `DO_NOT_TRACK`, which is a switch against
  background tracking, while a report leaves only on an explicit tool call the user can see.

### 2026-11-13 — a self-hosted install sends feedback only, not usage events

- Decision: the only event a default install sends is `MCP Feedback: Submitted`. Tool-call, prompt,
  resource, and OAuth events stay behind `ANALYTICS_ENABLED` and `SEGMENT_KEY`.
- Rationale: data leaves a self-hosted install only on an explicit tool call the user can see.
  Background usage reporting from every self-hosted install is a separate policy decision.

### 2026-11-13 — the hosted deployment is identified, and says so with a label

- Decision: `MCP_DEPLOYMENT=cloud` labels reports from the MCP server SigNoz hosts and attaches the
  org, user, and instance URL. Unset or any other value means `self_hosted` and a hashed org.
- Rationale: both deployments share one feedback source, so the event must say where it came from,
  and no existing setting names the hosted deployment. Cloud reports are identified so the team can
  follow up with the customer; that was the product owner's call. The safe side is the default: a
  deployment that sets nothing is never identified.
- Alternatives rejected: identity whenever usage analytics is on, which ties feedback back to the
  analytics settings; a hashed org everywhere.

### 2026-11-13 — an unreadable `FEEDBACK_ENABLED` turns feedback off

- Decision: unset means on; any value that is not a true boolean, such as `off` or `no`, means off.
- Rationale: the variable exists to opt out, so a mistyped opt-out must still opt out.

### 2026-11-13 — the tool is always registered and reports when it is off

- Decision: register the tool in every build. Without a sink it returns `UNSUPPORTED` and tells the
  agent not to retry.
- Rationale: one catalog for every build keeps `manifest.json`, the wire catalog, and the inventory
  tests on the single registration path. Reusing `UNSUPPORTED` adds no error code (ERR-1).
- Alternatives rejected: registering the tool only when delivery is on.

### 2026-11-13 — annotations: not read-only, not destructive, not idempotent

- Decision: `withCreateToolAnnotations`.
- Rationale: each call sends one report outside the server, and a repeat sends a duplicate (SUR-7).

### 2026-11-13 — the agent reports its model; nothing about reasoning effort

- Decision: an optional `model` string the agent fills in only when it knows it.
- Rationale: MCP gives the server the client's name and version, not the model, so the agent is the
  only source. The model separates "this model misread the schema" from "the schema is wrong". It is
  self-reported and unchecked, and the README says so. Reasoning effort is left out: most agents
  cannot see their own setting, so the field would be mostly empty or guessed.

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

- `go test ./pkg/analytics/ ./internal/handler/tools/ ./internal/mcp-server/`: the settings table, the
  tool contract (success, off, validation, rejected credentials), and what each deployment sends
  through the production HTTP handler, with usage analytics on beside it. The server tests fail when
  the report reaches the usage sink, when a usage event reaches the feedback sink, or when a
  self-hosted report carries the org, user, name, email, or API key. `AnonymousID` was checked once
  by hand against `openssl dgst -sha256 -hmac`; it has no pinned test, because the repo allows pins
  only for the wire catalog, guardrail budgets, and the docs baseline.
- Binaries built three ways (from source, with an empty key, and with a key through the same
  single-quoted ldflags string the Docker workflow passes) and run in HTTP mode against a fake
  SigNoz:
  - from source, and with an empty key: `UNSUPPORTED`;
  - with a key and no settings: the report is queued, the startup line says `self_hosted`, and the
    server attempts a Segment upload;
  - with a key and `FEEDBACK_ENABLED=false`: `UNSUPPORTED`, no startup line, no upload attempt;
  - with a key and `ANALYTICS_ENABLED=false`: queued, because feedback does not read that setting;
  - with a key, `MCP_DEPLOYMENT=cloud`, `ANALYTICS_ENABLED=true`, and `SEGMENT_KEY`: queued, the
    startup line says `cloud`, and both Segment clients attempt an upload;
  - the same with `FEEDBACK_ENABLED=false`: `UNSUPPORTED`, and only the usage client uploads.
- `make ci`.
- Not run: goreleaser (the Pro distribution is not installed locally) and the primus Docker build.
  The ldflags template and the quoted flag string were checked by hand against goreleaser's `.Env`
  map and the primus `go-build` recipe.
- No e2e test: the tool's only SigNoz call is the existing identity lookup, and the e2e server is a
  build from source, where feedback is off.

## Outcome

Shipped with feedback separated from analytics, after a first version that resolved both from the
analytics settings proved confusing in review. Two things must be set outside this repo before
feedback flows: the `FEEDBACK_SEGMENT_KEY` repository variable, because an empty key resolves to off,
and `MCP_DEPLOYMENT=cloud` on the hosted deployment, because without it hosted reports are labeled
`self_hosted` and carry a hashed org. Deferred: nudges in server instructions and upstream error
text, a triage loop for the reports, and whether `SigNoz/agent-skills` should teach when to call the
tool; all tracked in the issue above.
