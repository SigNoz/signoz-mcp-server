# Plan: Feedback Rule In Server Instructions

Status: In Progress
Issue: https://github.com/SigNoz/nerve-pod/issues/636
PR:

## Context

`signoz_submit_feedback` tells agents to report a problem without waiting for the user, but only in
its own description. Clients that defer tool loading show the model tool names and the server
instructions until it loads a tool, so an agent that never loads the feedback tool never reads that
sentence. The server instructions did not mention feedback.

Sanity and PostHog both put feedback guidance in their server instructions, and Sanity reports that
the tool alone was not enough. That is a reason to try it, not evidence for this server; the check
under Verification is the evidence.

## Approach

Add one numbered rule to `## Rules` in the server instructions. It names the tool, the three
problems the tool description already lists, and the user's own feedback about SigNoz, and tells the
agent to send once and continue. Masking and the report format stay in the tool description, which
the agent loads before it can call the tool (RES-5).

The rule is static: it is also sent when `FEEDBACK_ENABLED=false`.

## Files to Modify

- `pkg/instructions/instructions.go` — rule 10
- `internal/mcp-server/testdata/wire-catalog/initialize.json` — the same text in the recorded
  `initialize` response

## Key Decisions

### 2026-11-25 — one rule under Rules, not routing in the preamble

- Decision: the rule is number 10 under `## Rules`.
- Rationale: the preamble routes user questions to tools and has a 512-byte budget. Feedback is a
  policy that follows a failure in any tool, which is what the rules hold.

### 2026-11-25 — fit the existing byte budget

- Decision: the rule is 179 bytes, which brings the instructions to 4,091 of 4,096 bytes.
  `MaxServerInstructionsBytes` is unchanged.
- Rationale: Claude Code and Noz keep 4,096 characters of server instructions, so a longer text
  would be cut off for them.
- Consequence: 5 bytes are left. The next rule has to shorten an existing one.

### 2026-11-25 — the rule does not mention user frustration

- Decision: the rule covers what the user states as feedback. It says nothing about inferring the
  user's mood.
- Rationale: the `user_frustration` category is accepted but not described, so that other agents are
  not taught to send it. Noz's prompt is where its meaning lives.

### 2026-11-25 — the rule stays when feedback is turned off

- Decision: no conditional instructions.
- Rationale: the tool is listed either way and already says to report without waiting. A server
  with feedback off answers a call with `UNSUPPORTED` and "Do not retry", so the cost is one wasted
  call per problem.
- Alternatives rejected: building the instructions at startup from the feedback setting, which adds
  a second variant of a text the wire catalog pins once.

## Reference Links

- [How to get product feedback from agents (Sanity)](https://www.sanity.io/engineering/how-to-get-product-feedback-from-agents)
- [PostHog MCP agent feedback instructions](https://github.com/PostHog/posthog/blob/master/services/mcp/src/templates/sections/agent-feedback.md)
- [Using server instructions (MCP blog)](https://blog.modelcontextprotocol.io/posts/2025-11-03-using-server-instructions/)

## Verification

- `go test ./internal/mcp-server -count=1`, `make check-fmt check-build check-guardrails
  check-repo-docs`: pass. `TestGuardrail_WireContractBudgets` and `TestGuardrail_WireCatalogGoldens`
  cover the budget and the recorded `initialize` response.
- Before and after check (EVL-1, EVL-2). The server built from `main` and from this branch, in
  stdio mode against a local fake SigNoz, with Segment delivery pointed at a dead proxy so nothing
  left the machine. Client: Claude Code 2.1.331 headless with claude-sonnet-5-5, which deferred tool
  loading in every run. Three runs per cell; the number is runs that called
  `signoz_submit_feedback`.

  | Prompt | `main` | This branch |
  |---|---|---|
  | Error logs for a service; every query returns `500: internal error` | 0 of 3 | 3 of 3 |
  | A filter with a mistyped key; the error names the right key | 0 of 3 | 0 of 3 |
  | Logs for a service; the query succeeds | 0 of 3 | 0 of 3 |
  | The same, with a feature wish stated in passing | 0 of 3 | 1 of 3 |

  The three reports for the failed query used category `tool_error`, named `signoz_search_logs`,
  quoted the error, and replaced the service name with `<service>`. Two runs hit an inference API
  error before finishing and were rerun.
- Limits of that check: one client, one model, three runs per cell, and a fake SigNoz. It shows the
  rule changes behavior for a client that defers tool loading. It does not measure other clients,
  and feedback the user states in passing is still sent only sometimes.
- Not run: `make ci` in full locally (`check-deps` fails on this machine's modified module cache)
  and the e2e suite; CI runs both.

## Outcome

Pending.
