# Feature: MCP Tier 2 Protocol and Client Lanes — Context & Discussion

## Original Prompt
> Merged this PR, now let's move to tier 2, only create plan

## Reference Links
- [SigNoz/nerve-pod#34](https://github.com/SigNoz/nerve-pod/issues/34)
- [Reconciled Tier 2 lanes](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5010045025)
- [Initialized-catalog and metadata best practices](https://github.com/SigNoz/nerve-pod/issues/34#issuecomment-5014863452)
- [Merged Tier 1 PR #249](https://github.com/SigNoz/signoz-mcp-server/pull/249)
- [MCP Inspector](https://github.com/modelcontextprotocol/inspector)
- [MCP conformance suite](https://github.com/modelcontextprotocol/conformance)
- [MCP protocol versioning](https://modelcontextprotocol.io/docs/learn/versioning)
- [2026-07-28 release candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/)
- [Cursor CLI MCP commands](https://docs.cursor.com/en/cli/reference/parameters)

## Key Decisions & Discussion Log

### 2026-07-19 — Tier 2 planning started
- This is planning only. No production code, tests, workflows, commits, or pull requests are part of this step.
- Tier 2 means the protocol and real-client compatibility lanes from the reconciled nerve-pod plan. The model-based reviewer agent remains a later tier.
- The work will be delivered as small, independently reviewable tranches rather than one workflow containing every protocol, vendor, and live-environment dependency.
- Tier 2 starts with a normalized in-process catalog snapshot, semantic comparison, and runtime/manifest parity. These form the stable oracle that Inspector and client lanes compare against and close the semantic snapshot gap deliberately deferred from Tier 1.
- The remaining Tier 1 work on per-tool-class response budgets and exhaustive coded-error injection is orthogonal and remains outside this plan. Tier 2 must not silently mark those items complete.
- Per-PR lanes must be deterministic, unprivileged, and secret-free. Paid or authenticated vendor clients and a live SigNoz instance run only from protected schedules or manual dispatches.
- Client checks will assert machine-observed catalog data or receipt of a synthetic sentinel call. Asking a model to enumerate tools is not an acceptable compatibility oracle.

### 2026-07-19 — Delivery sequence
- Tranche A establishes one production-derived catalog, a canonical snapshot, semantic breaking-change classification, reviewed migration records, and `manifest.json` parity.
- Tranche B adds blocking MCP Inspector and official conformance checks against a locally started server.
- Tranche C adds protected nightly Cursor, Claude Code, and Codex lanes using pinned clients, plus nonblocking latest-version probes.
- Tranche D adds the separately protected live-SigNoz contract lane, a manual Devin checklist, and a nonblocking next-protocol lane.

### 2026-07-19 — External tool baseline
- The planning baseline is Node.js 22, `@modelcontextprotocol/inspector@1.0.0`, and `@modelcontextprotocol/conformance@0.1.16`. Exact versions and their documented CLI behavior must be reverified when Tranche B begins, then pinned in a committed lockfile.
- Cursor, Claude Code, and Codex versions will be pinned in one reviewed client-version policy file. A client version must pass the lane before it becomes the blocking pin; upgrades are explicit policy changes.
- The current MCP protocol version is `2025-11-25`. The `2026-07-28` candidate remains nonblocking until the official versioning page marks it current and stable mcp-go and conformance support are available.

### 2026-07-19 — Central review surfaces
- Human-reviewed compatibility policy will live under `guardrails/`: the catalog snapshot, migration ledger, conformance expected-failure baseline, client pins, parser fixtures, and manual-lane documentation.
- Workflows only execute this policy. A contributor must not be able to hide a semantic break by updating the snapshot alone: pull-request CI compares the base catalog with the head catalog and requires an exact reviewed migration entry for any allowed break.
- GitHub CODEOWNERS changes are not part of this plan, consistent with the Tier 1 decision.

### 2026-07-20 — Design interview opened after Tier 1 merge
- Tier 1 PR #249 and the follow-up catalog-metadata PR #248 are merged on `main`.
- The user asked to revisit Tier 2 through a dependency-ordered design interview, one question batch at a time. Existing Tier 2 choices remain draft until explicitly resolved during this interview.
- Each question will include a recommended answer, counterpoints, and alternatives. Repository facts will be checked directly rather than asked back to the user.

### 2026-07-20 — Tier 2 scope boundary resolved
- Tier 2 covers three dependency-ordered areas: the production-derived catalog oracle, Inspector/conformance protocol checks, and protected Cursor/Claude Code/Codex compatibility lanes.
- Live SigNoz upstream testing, the manual Devin marketplace check, and next-protocol probes remain valuable linked follow-ups, but are outside Tier 2 and cannot delay its completion.
- This supersedes the earlier four-tranche delivery sequence. It keeps downstream MCP/client compatibility cohesive while separating work with different credentials, cleanup, ownership, or dependency-readiness constraints.

### 2026-07-20 — Committed catalog snapshot rejected
- The user chose to omit a committed catalog snapshot entirely.
- `guardrails/catalog.snapshot.json` and snapshot generate/verify workflows are therefore out of scope. Earlier snapshot-specific planning is superseded.
- The next dependency is to decide whether CI should compare catalogs generated from the pull request base and head revisions at runtime, or use narrower invariant/protocol checks without historical catalog comparison.

### 2026-07-21 — Catalog branch removed from Tier 2
- The user chose to skip the first Tier 2 area completely: no production/test registration refactor, catalog extractor, catalog normalization, historical comparison, migration ledger, or expanded manifest parity is part of Tier 2.
- Tier 2 now contains only protocol checks and protected real-client compatibility lanes.
- This accepts that Tier 2 will not detect semantic catalog removal or narrowing beyond the existing Tier 1 guardrails, integration tests, and manifest checks.

### 2026-07-21 — Inspector V1 retained
- The user chose to include MCP Inspector V1 now and upgrade to V2 when V2 is ready.
- Pin Inspector `1.0.0` and Node.js `22.7.5` or newer within the Node 22 line. Keep the Inspector wrapper isolated so the V2 replacement does not require redesigning the server harness or assertions.
- Inspector V1's CLI does not expose the initialize result. Use a minimal raw protocol probe for negotiated version, capabilities, server identity, and instructions; use Inspector for its supported list/read/call operations.
- V2 promotion is a separate reviewed dependency update after a stable release passes the same required assertions.

### 2026-07-21 — Conformance deferred
- The user chose to omit the official MCP conformance suite until it becomes stable.
- Tier 2 will not add the `0.1.16` package, expected-failure baseline, conformance workflow check, or branch-protection rule.
- Reconsider conformance as a linked follow-up when the project publishes a stable release; its adoption must then be planned and reviewed separately.

### 2026-07-21 — Real-client lanes deferred
- The user chose to omit Cursor, Claude Code, and Codex compatibility lanes from Tier 2 for now.
- Tier 2 will not add a sentinel server, client-version policy, vendor credentials, scheduled client workflow, parser fixtures, or model-driven invocation checks.
- Real-client compatibility remains a linked follow-up. Active Tier 2 is now only the Inspector V1 protocol lane.

### 2026-07-21 — Inspector covers all advertised surfaces shallowly
- The user chose broad surface coverage rather than the minimal initialize/list/call path.
- A raw protocol probe validates initialization metadata because Inspector V1 cannot emit it. Inspector validates `tools/list`, `resources/list`, resource-template listing, `prompts/list`, and one deterministic `signoz_search_docs` invocation backed by the embedded docs index.
- Assertions cover successful, well-formed responses and capability usability. They do not pin exact catalog counts, descriptions, schemas, or ranked documentation text.

### 2026-07-21 — Inspector runs on every pull request
- The user chose to run the Inspector workflow on every pull request to `main` with no path filters.
- Required-check status and any additional manual or scheduled trigger remain separate decisions.

### 2026-07-21 — Inspector becomes required after bootstrap
- The user chose to make `protocol / inspector` a required check for `main`.
- First merge and run the workflow successfully on the default branch, then add its exact check name to branch protection as a separate administrative step.
- Do not use `continue-on-error` or an expected-failure bypass.

### 2026-07-21 — Inspector trigger policy resolved
- The workflow runs on every pull request to `main` and supports `workflow_dispatch` for manual diagnostics.
- It does not run on a schedule because the code and toolchain are pinned; recurring unchanged runs are not part of Tier 2.

### 2026-07-21 — Inspector uses a separate workflow
- The user chose a separate `.github/workflows/mcp-protocol.yaml` rather than adding Inspector to `.github/workflows/guardrails.yaml`.
- The stable required-check name is `protocol / inspector`. Tier 1 stays a fast Go-only workflow, while Inspector owns its Node toolchain, server lifecycle, logs, and manual trigger.
- Protocol policy and contributor guidance remain centralized under `guardrails/README.md`.

### 2026-07-21 — Use a reusable Bash harness
- The user chose a reusable Bash harness under `scripts/` around the real production binary rather than a Go harness, inline workflow commands, or an in-process server.
- The harness builds and starts the binary on a fixed high default port with an environment override, supplies isolated test configuration, waits for `/readyz` with a bounded timeout, runs the raw initialize probe and Inspector commands, captures logs on failure, and always terminates the server through an exit trap.

### 2026-07-21 — Use environment-provided test credentials
- The user chose fixed test-only `SIGNOZ_URL` and `SIGNOZ_API_KEY` environment values rather than Inspector request headers or an authentication bypass.
- OAuth and analytics are disabled. The Inspector lane exercises the production configured-credential fallback, while existing Go tests retain responsibility for other authentication branches.
- The selected embedded-docs tool call must not contact the dummy SigNoz backend.

### 2026-07-21 — Selective initialize contract
- The user chose selective initialize assertions rather than success-only validation or a full-response snapshot.
- Pin protocol version `2025-11-25`, server name `SigNozMCP`, expected tools/resources/prompts/logging capability presence, non-empty server version and instructions, and stateless operation without a required session ID.
- Do not pin the exact server version or instruction text.

### 2026-07-21 — Minimal discovery metadata assertions
- The user chose non-empty collection and minimal identity validation for every Inspector list surface.
- Require valid JSON; non-empty tools, resources, templates, and prompts; and non-empty item identity fields (`name`, plus resource `uri` or template URI as applicable).
- Require `signoz_search_docs` to be present because the lane invokes it. Do not pin counts, complete inventories, descriptions, schemas, or ordering.

### 2026-07-21 — Deterministic docs tool invocation
- The user chose `signoz_search_docs` with `searchText: "docker"`, a fixed verbatim `searchContext`, and `limit: 1` for the Inspector tool call.
- Require a successful result, non-empty structured `results`, and non-empty text content. Do not pin result URL, title, snippet, rank, or wording.
- This intentionally overlaps the in-process docs test at a different boundary: the production binary, auth middleware, Streamable HTTP transport, and Inspector client.

### 2026-07-21 — Raw initialize probe uses curl and jq
- The user chose an explicit `curl` JSON-RPC initialize request plus `jq` assertions inside the Bash harness rather than another Node/Go program or relying only on Inspector's internal handshake.
- Request an `application/json` response, require HTTP 200 and JSON content type, apply the selective initialize assertions, and verify that the stateless server does not return `Mcp-Session-Id`.

### 2026-07-21 — Strict timeout and retry policy
- The user chose a 10-minute workflow timeout, 30-second readiness deadline, and 60-second limit for each raw/Inspector command.
- Only readiness polling repeats. Protocol commands are not retried, so intermittent compatibility failures remain visible.
- Cleanup targets only the captured server PID: send TERM, wait up to five seconds, then force-stop that PID if necessary.

### 2026-07-21 — Print failure logs only
- The user rejected uploaded failure artifacts.
- Successful runs print a concise summary. Failed runs print the failed phase, its relevant Inspector stderr/JSON, and a bounded tail of server logs directly in the job log.
- Temporary files are always removed during cleanup; no workflow artifact is retained.

### 2026-07-21 — Pin the CLI-only Inspector V1 package
- The user chose `@modelcontextprotocol/inspector-cli@1.0.0` rather than the full Inspector package.
- Commit an npm lockfile and install with `npm ci`. The full V1 web UI and proxy dependencies are not needed by this lane.
- The isolated harness adapter owns the future migration to Inspector V2's consolidated package and changed CLI surface.

### 2026-07-21 — Inspector connection and usable-surface coverage
- The user chose direct Inspector CLI flags rather than a generated MCP configuration file.
- The production binary binds to `127.0.0.1:18080` by default for the harness, supports a `MCP_PROTOCOL_PORT` override, and fails clearly before startup if the selected port is occupied.
- In addition to initialize, list calls, and docs search, exercise `resources/read` for `signoz://docs/sitemap`, `prompts/get` for `debug_service_errors` with synthetic arguments, and `logging/setLevel`. Require successful, non-empty responses without pinning generated content.

### 2026-07-21 — Bind-address correction
- Code inspection found that the production HTTP server currently accepts only a port and listens on `:<port>` (all interfaces). Connecting to `127.0.0.1` does not make the listener loopback-only.
- The prior discussion entry's loopback-binding statement is therefore corrected. Whether to add an explicit bind-host configuration remains unresolved.

### 2026-07-21 — Bind host and runner portability resolved
- The user chose an optional `MCP_SERVER_HOST` configuration. Existing empty/default behavior remains unchanged for deployments; the Inspector harness sets it to `127.0.0.1`.
- The user chose Ubuntu GitHub runner support only. Local macOS portability is not a Tier 2 requirement, so the harness may use utilities and timeout behavior available on `ubuntu-latest`.
- The Node major remains under discussion: distinguish the absolute latest Current release from the latest patch of a selected supported/LTS line.

### 2026-07-21 — CI toolchain and bookkeeping finalized
- The user chose the latest patch of the Node 24 LTS line, not the absolute latest Current Node major or an exact patch. Inspector's `>=22.7.5` engine requirement remains satisfied.
- Follow repository convention with `actions/checkout@v4`, `actions/setup-go@v5`, and `actions/setup-node@v4`; enable Go and npm caches, with npm keyed by the committed Inspector lockfile.
- Document the Inspector pin, workflow, update process, and local/CI commands in `guardrails/README.md`. Do not add the shell workflow to the Go-only `guardrails/tests.txt` inventory and do not create a second protocol inventory file.
- Validate the harness and workflow with `bash -n`, ShellCheck, and actionlint, then run the focused Inspector lane and existing Go verification.
- The Tier 2 design interview is complete; implementation has not started.

### 2026-07-21 — Implementation started
- The user approved implementation of the finalized Inspector V1 protocol lane.
- Work is split across optional bind-host support, the pinned CLI-only Inspector harness, and the separate required workflow/policy surface. Scope exclusions agreed during planning remain unchanged.

### 2026-07-21 — Protocol harness tracking
- The repository ignores new root-level scripts by default. `.gitignore` now admits only `scripts/test-mcp-protocol.sh`, keeping the agreed reusable harness path without exposing unrelated local scripts.

### 2026-07-21 — Implementation verified
- The planned source areas had not drifted from the planning baseline before implementation.
- Inspector 1.0.0 resolves package metadata relative to its working directory, so the isolated adapter runs the official CLI launcher from its package build directory.
- Agent CI exposed two runner-image assumptions. The workflow now uses `bash -n` instead of assuming ShellCheck is installed, and the harness uses Node to preflight the exact loopback bind instead of assuming `ss` is installed. ShellCheck remains a documented maintainer check.
- Agent CI passed both `guardrails / contract` and `protocol / inspector`; the protocol lane exercised every agreed surface against the production binary.
- Focused guardrails, the full Go suite, vet, build, tidy diff, npm lockfile installation, Inspector version verification, ShellCheck, and actionlint passed locally. Two modified entries in the shared Go module cache were independently downloaded and verified in an isolated cache.
- `README.md` documents `MCP_SERVER_HOST`. No `docs/`, `manifest.json`, or companion `SigNoz/agent-skills` change is needed because the stdio manifest and taught MCP tool contracts are unchanged.
- Implementation is complete and remains `In Progress` until the change is delivered; the required branch-check setting is a post-merge administrative step.

## Open Questions
- [x] Without a committed snapshot, should pull-request CI generate and compare the base and head catalogs at runtime, or avoid historical catalog comparison entirely? Resolved: skip historical catalog comparison and the complete catalog branch.
- [x] Should the official conformance suite run alongside Inspector V1, and when should it become a required check? Resolved: omit it until a stable release is available.
- [x] Should `protocol / inspector` become a required branch check or remain advisory? Resolved: require it after its first successful default-branch run.
- [x] In addition to every pull request, should the Inspector workflow support manual dispatch or a schedule? Resolved: support manual dispatch and omit scheduled runs.
- [x] Which exact Cursor, Claude Code, and Codex versions should become the first blocking pins? Resolved for Tier 2: defer all three client lanes and decide versions in a linked follow-up.
- [x] Which repository/environment secrets and live SigNoz environment should Tranche D use? Resolved for Tier 2: move this decision to the linked live-upstream follow-up.
- [x] Which conformance scenarios, if any, are legitimate expected failures with the pinned mcp-go version? Resolved for Tier 2: conformance is deferred until a stable release, so no baseline is created now.
- [x] Who owns the weekly Devin marketplace check and where should its result be recorded? Resolved for Tier 2: move ownership and recording policy to the linked Devin follow-up.
