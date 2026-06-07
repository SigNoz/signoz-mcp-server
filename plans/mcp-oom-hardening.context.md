# Feature: MCP Server OOM Hardening — Context & Discussion

## Original Prompt
> Investigate a prod OOMKill of signoz-mcp-server (pod ...wljg5, 2026-06-06 ~03:42 UTC), debug it,
> check the memory profile locally, and check whether current production servers are enough.
> Then: "Let's go" — implement the fixes.

## Reference Links
- Alert investigated: `[platform][k8s] pod has been oomkilled` (ruleId 019dd9d2-5eee-7220-96f7-554bae501e6a) on nightswatch.signoz.cloud
- Memory: `~/.claude/.../memory/project_mcp_oom.md` (measured model)

## Key Decisions & Discussion Log
### 2026-06-07 — Root-cause findings (measured)
- Prod OOM (wljg5): `go.memory.used`/`gc.goal`/`go.goroutine.count` all FLAT through the spike → sub-60s burst invisible to the 60s OTel export. NOT a leak, NOT goroutine pileup. Docs refresh ruled out (generation flat at 4, 0 fetches). No tool call logged in the spike window. Specific trigger unidentified.
- Root condition: hot replicaset (`84454bd5dd`: v946p 1496 MiB, cnmws 1461 MiB at ~1.5 GiB limit) + **no GOMEMLIMIT** → any transient → SIGKILL.
- Offline profile (`internal/docs/memprofile_test.go`): one in-memory bleve index = **323 MiB resident** for 777 pages, **~1.5 GiB alloc churn/rebuild**, ~520 MiB double-resident during Swap, no leak. Heap profile: 93% in `BuildIndex` → bleve **upsidedown + gtreap** backend (legacy, memory-hungry). `bleve.NewMemOnly` = upsidedown/gtreap.
- Real-backend load profile (`internal/handler/tools/loadprofile_test.go`, vs app.us.staging): request path bounded — 1.68 MiB result / 5.9 MiB churn per 1000-log query; 8.2 MiB / 29 MiB for 5000 logs; 40 concurrent peaked only +16 MiB (staging latency serializes). BUT `limit` is uncapped (`intArg`, no max; no clamp) → a single huge-limit query is an unbounded OOM vector.

### 2026-06-07 — Fix decisions
- Biggest lever = docs index backend. Swap `bleve.NewMemOnly` (upsidedown/gtreap) → in-memory **scorch** (`bleve.NewUsing("", mapping, scorch.Name, scorch.Name, nil)`). Transparent to our code (generic `bleve.Index`). bleve v2.5.4 already defaults on-disk index type to scorch; scorch supports path=="" in-memory.
- Cap `limit` in raw-data tools (search_logs, search_traces) to close the unbounded single-request vector. Make the cap a constant (with room to make env-configurable).
- GOMEMLIMIT + container limit bump = deploy/infra-repo change (NOT this repo) → leave as a recommendation in the PR description.
- Mapping trim (dedup body / term-vectors) deferred: both `body` (highlight) and `body_markdown` (snippet retrieval) are used; backend swap is the dominant win — measure first.

### 2026-06-07 — Implemented + measured results
- **Scorch swap landed** (`internal/docs/index.go`: `bleve.NewMemOnly` → `bleve.NewUsing("", mapping, scorch.Name, scorch.Name, nil)`). Re-profiled via `memprofile_test.go`: resident/index **323→32.4 MiB (−90%)**, churn/rebuild **1510→414 MiB (−73%)**, Swap double-resident peak **520→62 MiB (−88%)**, no leak. All `internal/docs` tests pass unchanged (no golden updates needed).
- **Limit clamp landed** (`MaxRawResultLimit=10000` + `clampLimit`/`rawSearchResult` in `aggregate_helper.go`; applied in `logs_helper.go`/`traces_helper.go`; surfaced via a pagination note; param descriptions + README updated). Validated against real staging: `limit=50000` now returns 16.5 MiB (~10k rows) instead of ~82 MiB. Unit test in `limit_clamp_test.go`. Full `go test ./...` green.
- Deploy-side fixes (GOMEMLIMIT≈1280MiB, container limit 1.5→2 GiB) remain for the infra repo — call out in PR description, not in this repo.

### 2026-06-07 — Codex (gpt-5.5 / xhigh) review + responses
- Codex verified the scorch swap against bleve v2.5.4 source: `NewUsing("", mapping, scorch.Name, scorch.Name, nil)` ⇒ in-memory mode (no MkdirAll/Bolt/persister/merger); non-empty kvstore string required (`index_impl.go:87`), scorch ignores it; introducer goroutine ⇒ `Close()` required (our `closeWhenDrained` already does it). Highlight/stored-fields safe. No change needed — high-risk change validated.
- **Fixed (Codex blocker):** clamp note was prepended into the JSON payload, breaking JSON parsing. Now the JSON is `Content[0]` (parseable) and the note is a separate `Content[1]` block. User chose "note as separate content block." Locked with `TestRawSearchResult_NoteIsSeparateBlock`.
- **Fixed (Codex real bug):** trace `offset` was a no-op — `BuildTracesQueryPayload` hardcoded `Offset:0` and didn't take the param, so `parseSearchTracesArgs` discarded offset and the clamp note's "paginate with offset" was false for traces. Wired offset through `BuildTracesQueryPayload` (+ `client.GetTraceDetails` passes 0). Logs already honored offset.
- **Scope:** user chose "targeted now + follow-up" — ship search_logs/search_traces clamp; follow-ups recorded in `.plan.md`.

## Open Questions
- [x] Do scorch in-memory search results match the golden tests? — **Yes.** Full `internal/docs` suite (golden/verification/index/sitemap) passes with no golden changes.
- [x] Final cap value for `limit`? — **10000** (≈16 MiB result, validated). Constant `MaxRawResultLimit`; can be promoted to env config later if needed.
- [x] How to surface clamping without breaking JSON? — **Separate MCP content block** (JSON stays parseable as `Content[0]`).
