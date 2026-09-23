# Plan: make ci target, shared CI checks, and agent guidance

Status: In Progress
Issue:
PR:

## Context

Several PR-gate checks had no local target: golangci-lint, `go test -race`, `go mod tidy` drift,
the guardrail inventory diff (inline bash in `guardrails.yaml`), and the protocol and conformance
lanes (documented as prose in `guardrails/README.md`). They were easy to miss until CI failed.

Two gaps surfaced while mapping the gate:

- The shared primus `go-fmt` job runs `gofmt -e -s -l -w` and never diffs afterwards, so
  formatting drift cannot fail `ci / fmt`. Local `make fmt` used `go fmt`, which skips `-s`.
- `plans/README.md` requires `Done` plus outcome and verification before merge, but nothing
  checked it. Two existing plans already deviated from the template.

The protocol scripts need GNU `timeout`, which macOS ships only as `gtimeout` via coreutils. With
`timeout` available, both scripts pass on macOS.

CLAUDE.md listed commands in prose, spread the tool-change checklist across three sections, had
one line of code style, and named only agent-skills as a downstream consumer.

## Approach

Mirror signoz-ai-assistant#455: a `make ci` target that runs every PR-gate check except the live
e2e suite, with each step as its own target, and CI workflows that call the same targets so local
and CI definitions cannot drift. Checks are read-only. Port that repo's plan validator to Go and
adapt it to this repo's `plans/README.md`, including its rule that a plan merged before
completion keeps its status with an explanation in Outcome. Rewrite CLAUDE.md around commands, test
rules, a single tool-change checklist, a done bar, and PR hygiene.

## Files to Modify

- `Makefile` — `ci` and per-step check targets; `fmt` uses `gofmt -s`; `fmt`/`goimports` only
  touch tracked and untracked-unignored Go files, so `.claude/worktrees` is never rewritten.
- `cmd/check-repo-docs/` — plan and doc-path validator with tests.
- `.github/workflows/guardrails.yaml`, `.github/workflows/mcp-protocol.yaml` — call the make
  targets.
- `.github/workflows/checks.yaml` — new `fmt` and `repo-docs` jobs.
- `scripts/test-mcp-protocol.sh`, `scripts/test-mcp-conformance.sh` — `gtimeout` fallback and an
  install hint.
- `plans/2026-09-16-docs-refresh-oom.md`, `plans/2026-09-16-docs-search-ranking-and-query-guidance.md`
  — normalize Status, PR, and section headings to the template without changing their record.
- `CLAUDE.md`, `CONTRIBUTING.md`, `guardrails/README.md` — point at the make targets; CLAUDE.md
  rewrite.

## Key Decisions

### 2026-09-23 — Run golangci-lint through `go run` at the primus version

- Decision: `lint` runs `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2`.
- Rationale: matches the `ci / lint` version with no install step; Go caches the build.
- Alternatives rejected: requiring a local install, which drifts from CI's pin.

### 2026-09-23 — Enforce plan completion only for plans the PR changes

- Decision: `-ready` applies to plans added or modified since the merge base. They must be `Done`,
  `Abandoned`, or explain in Outcome why they merge incomplete.
- Rationale: `plans/README.md` lets plans merge before completion with an explanation; requiring
  every plan in the tree to be `Done` would contradict it and block unrelated PRs.

### 2026-09-23 — Add a `checks / fmt` job instead of changing primus

- Decision: a repo-local job runs `make check-fmt`.
- Rationale: fixes the no-op format gate for this repo now; the primus fix can follow separately.

### 2026-09-23 — Name downstream consumers generically

- Decision: CLAUDE.md and CONTRIBUTING.md name MCP clients, SigNoz/agent-skills, and custom agents
  and clients as downstream consumers, not specific private repos.

## Reference Links

- [signoz-ai-assistant#455](https://github.com/SigNoz/signoz-ai-assistant/pull/455)
- [Plans README](README.md)

## Verification

- `GOTOOLCHAIN=go1.26.0 make ci` on macOS with a `gtimeout` shim: all steps passed in 169s
  (0 lint issues, 16 guarded tests, protocol and conformance lanes, ruff, plan validation).
- Under local Go 1.27, `test-race` fails `TestSchemaConversionFailuresIncludeDirectionAndTool`:
  the test expects `json.RawMessage` in a `%T`-formatted message, which Go 1.27 prints as
  `jsontext.Value`. It passes on Go 1.26, which CI uses. Pre-existing; fix separately before CI
  moves to Go 1.27.
- `actionlint` v1.7.7 on the three changed workflows: clean.
- The #313 branch's own plan passes the validator; its only failures are the two plans this
  change normalizes.
- Failure paths checked: an unformatted file fails `check-fmt` and lists it; an unsorted or
  unexpected `guardrails/tests.txt` entry fails `check-guardrails` with the diff; an unfinished new
  plan passes `check-repo-docs` but fails `check-repo-docs READY=1`; without `timeout` or
  `gtimeout` the protocol scripts print the coreutils hint; with only `gtimeout` they pass.
- `golangci-lint` v2.12.2: 0 issues. `go mod tidy -diff` and `go mod verify`: clean.

## Outcome
