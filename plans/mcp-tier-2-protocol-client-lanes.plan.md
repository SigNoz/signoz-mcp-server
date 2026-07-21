# Plan: MCP Tier 2 Inspector V1 Protocol Lane

## Status
In Progress

## Planning Baseline
- Priority: P1
- Estimated effort: Medium, one pull request
- Risk: Low-to-medium; the work adds one protocol CI lane without changing the public MCP contract
- Planned from: `5fc09a9` (`refactor(mcp): clarify full catalog metadata (#248)`), 2026-07-20
- Depends on: merged PRs #249 and #248

Before implementing any tranche, confirm that relevant code has not drifted:

`git diff --stat 5fc09a9..HEAD -- internal/mcp-server internal/handler/tools guardrails manifest.json .github/workflows tools scripts cmd plans/mcp-tier-2-protocol-client-lanes.*`

If it has, update this plan and append the reason to the context file before coding.

## Context
Tier 1 makes selected wire budgets, duplicate registration, resource pointers, retry safety, and result serialization visible in blocking pull-request CI. Tier 2 now asks one narrower question: does the real HTTP server interoperate with Inspector V1 over Streamable HTTP?

Tier 2 deliberately does not add a complete catalog oracle, snapshot, historical catalog comparison, migration ledger, registration-composition refactor, or expanded manifest parity.

## Goals
- Exercise Streamable HTTP with pinned MCP Inspector V1 on every pull request.

## Non-goals
- Building the later model-based reviewer agent.
- Completing the remaining Tier 1 per-tool response budgets or exhaustive 401/403 coded-error matrix.
- Adding a complete catalog oracle, snapshot, base/head comparison, migration ledger, registration-composition refactor, or expanded manifest parity.
- Running the MCP conformance suite before it has a stable release.
- Running protected Cursor, Claude Code, or Codex discovery/invocation lanes.
- Changing tool names, schemas, prompts, resources, or product behavior merely to make a compatibility test pass.
- Running paid/vendor-secret or live-SigNoz jobs on untrusted pull-request code.
- Live SigNoz upstream-contract automation, manual Devin marketplace checks, or next-protocol probes; these are linked follow-ups with different ownership and trust boundaries.
- Adding CODEOWNERS.

## Current State to Preserve
- `guardrails/tests.txt` is the exact Tier 1 guardrail inventory and `guardrails/README.md` is the central reviewer guide.
- `.github/workflows/guardrails.yaml` is an unprivileged, secret-free pull-request workflow. Tier 2 per-PR jobs must retain that trust boundary.
- The HTTP server exposes `/mcp` and `/readyz`. A local protocol job can use a dummy SigNoz URL/API key with OAuth and analytics disabled; no real SigNoz credential is needed for protocol checks.

## Delivery Plan

### Inspector V1 protocol lane

#### 1. Pin the protocol toolchain
- Add `tools/mcp-ci/package.json` and a committed lockfile.
- Pin `@modelcontextprotocol/inspector-cli@1.0.0`, use the latest patch of the Node 24 LTS line, and install dependencies with `npm ci`.
- Do not install the full V1 web UI/proxy package. Keep the CLI harness adapter isolated so Inspector V2's consolidated package can replace it without redesigning the server harness or assertions.
- Do not use floating `latest` versions in blocking jobs.

#### 2. Start the real HTTP server safely
- Add one reusable Bash protocol harness under `scripts/` that:
  - builds and starts the actual server in Streamable HTTP mode;
  - uses port `18080` by default, supports a `MCP_PROTOCOL_PORT` override, and fails clearly before startup if the selected port is occupied;
  - adds optional `MCP_SERVER_HOST` support with existing empty/default deployment behavior preserved, while the harness sets it to `127.0.0.1`;
  - sets fixed test-only `SIGNOZ_URL` and `SIGNOZ_API_KEY` values, with OAuth and analytics disabled;
  - waits for `/readyz` with a bounded timeout;
  - prints the failed phase, relevant Inspector stderr/JSON, and a bounded server-log tail on failure;
  - always terminates the process through a cleanup trap.
- Bound readiness to 30 seconds and every raw/Inspector command to 60 seconds. Poll only readiness; do not retry protocol commands.
- On cleanup, send TERM to the captured server PID, wait up to five seconds, then force-stop only that PID if still running.
- Do not upload workflow artifacts. Print a concise summary on success and always remove temporary files during cleanup.
- Keep this harness backend-independent. Use `signoz_search_docs` with the embedded docs index and assert its result envelope rather than exact ranked prose.
- Do not configure Inspector request auth headers or add a test-only auth bypass; use the production configured-credential fallback.
- Target `ubuntu-latest`; local macOS portability is not required for this harness.

#### 3. Exercise MCP Inspector
- Against `/mcp`, exercise `tools/list`, `resources/list`, resource-template listing, `prompts/list`, and one selected `tools/call`.
- Pass the Streamable HTTP URL, transport, method, and arguments directly as CLI flags; do not generate an Inspector config file.
- Require every list command to return valid JSON and a non-empty collection. Validate each item's minimal identity fields: tool/prompt `name`, resource `uri` and `name`, and template URI plus `name`.
- Require `tools/list` to contain `signoz_search_docs`; do not pin counts, complete inventories, descriptions, schemas, or ordering.
- Use `signoz_search_docs` for the deterministic tool call; it requires no live SigNoz backend or external network access after the embedded index is ready.
- Call it with `searchText: "docker"`, `searchContext: "How do I send Docker logs to SigNoz?"`, and `limit: 1`.
- Require `isError` to be absent or false, structured content with a non-empty `results` array, and non-empty text content. Do not pin the first result or any ranked content.
- Read `signoz://docs/sitemap`, get `debug_service_errors` with synthetic `service`/`timeRange` arguments, and call `logging/setLevel`. Require successful, non-empty resource/prompt responses without pinning their generated content.
- Require valid machine-readable results on every advertised surface and a successful tool result envelope. Do not pin exact catalog counts, descriptions, schemas, or ranked documentation text.
- Do not claim semantic catalog compatibility or treat process exit alone as sufficient.
- Use a minimal raw protocol probe for the initialize result because Inspector V1's CLI does not expose it. Assert negotiated version, capabilities, stable server identity, and instructions through that probe.
- Implement the probe inside the Bash harness with `curl` and `jq`; do not add a separate Node or Go probe program.
- Request `application/json`, require HTTP 200 plus JSON content type, and verify that no `Mcp-Session-Id` response header is issued.
- Pin protocol version `2025-11-25` and server name `SigNozMCP`; require non-empty server version and instructions plus tools, resources, prompts, and logging capabilities.
- Confirm the stateless server does not require a session ID. Do not pin the complete initialize response, exact server version, or instruction text.
- Fail on malformed JSON, missing surfaces, protocol errors, or an unexpected result envelope.
- Upgrade to Inspector V2 only through a separate reviewed dependency change after a stable V2 release passes the same assertions.

#### 4. Add the workflow check
- Add the separate `.github/workflows/mcp-protocol.yaml` on every `pull_request` targeting `main`, with no path filters, plus `workflow_dispatch` for manual diagnostics.
- Set workflow name `protocol` and job name `inspector` so the stable check name is `protocol / inspector`.
- Keep pull-request jobs unprivileged with `contents: read` and no secrets.
- Set the job timeout to 10 minutes.
- Use `actions/checkout@v4`, `actions/setup-go@v5` with Go `1.25` caching, and `actions/setup-node@v4` with Node `24` plus npm caching keyed by `tools/mcp-ci/package-lock.json`.
- Keep Tier 1 in `.github/workflows/guardrails.yaml`; do not add the Node toolchain or Inspector lifecycle to that workflow.
- Document Inspector policy and maintenance under `guardrails/README.md`.
- After the workflow succeeds on the default branch, add the exact `protocol / inspector` check to `main` branch protection as a separate administrative step.
- Do not use `continue-on-error` or an expected-failure bypass.
- Do not add a scheduled trigger.

## Linked Follow-ups Outside Tier 2
- Add protected Cursor, Claude Code, and Codex discovery/invocation lanes with explicit version, credential, cost, and ownership policy.
- Adopt the official MCP conformance suite after it publishes a stable release.
- Live SigNoz upstream-contract automation, with dedicated credentials, representative query/CRUD coverage, and confirmed cleanup.
- A manual Devin marketplace procedure with an explicit owner and recording location.
- A nonblocking next-protocol probe once the draft is supported well enough to exercise meaningfully.

These follow-ups must be tracked explicitly, but they do not block the Inspector V1 Tier 2 lane.

## Expected Files
- `internal/config/config.go` and tests — optional `MCP_SERVER_HOST`, preserving the existing default listener behavior.
- `internal/mcp-server/server.go` and focused tests — apply the optional host when constructing the HTTP listen address.
- `README.md` — document `MCP_SERVER_HOST` and its unchanged default behavior.
- `guardrails/README.md` — Inspector pin, policy, required-check promotion, update procedure, and verification commands. `guardrails/tests.txt` does not change.
- `tools/mcp-ci/package.json`, lockfile, and `.gitignore` — pinned Inspector V1 toolchain without vendored dependencies.
- `scripts/test-mcp-protocol.sh` — bounded Ubuntu production-server and Inspector harness.
- `.gitignore` — track the reviewed protocol harness while keeping other root scripts ignored.
- `.github/workflows/mcp-protocol.yaml` — secret-free per-PR and manual Inspector lane; no schedule.
- `plans/mcp-tier-2-protocol-client-lanes.*` — finalized design record committed with the implementation.

## Verification

### Inspector V1 lane
- Run the local server harness from a clean checkout and prove it always exits.
- Run the pinned Inspector list/call sequence and require valid machine-readable results.
- Run `bash -n scripts/test-mcp-protocol.sh`.
- Run ShellCheck on `scripts/test-mcp-protocol.sh`.
- Run `actionlint .github/workflows/mcp-protocol.yaml`.
- Run `npm ci` using only the committed lockfile and verify installed CLI versions equal the pins.

### Final repository checks
- `git diff --check`
- `go test -count=1 -run '^TestGuardrail_' ./...`
- `go test -count=1 ./...`
- `go vet ./...`
- `go build ./...`
- `go mod tidy -diff`
- `go mod verify`
- Explicit PR-summary statement on `README.md`/`docs/`/`manifest.json` updates and whether `SigNoz/agent-skills` needs a companion change

## Stop Conditions
- Stop if the pinned Inspector cannot provide stable machine-readable output; document and design the smallest explicit probe instead of parsing prose.

## Success Criteria
- Inspector V1 exercises initialization, tool discovery/invocation, resource discovery/read, resource-template discovery, prompt discovery/get, and logging level against the real HTTP server without secrets.
- `protocol / inspector` is required for pull requests targeting `main` after its successful bootstrap run.
- Human reviewers can find the Inspector pin and protocol policy under `guardrails/`.
