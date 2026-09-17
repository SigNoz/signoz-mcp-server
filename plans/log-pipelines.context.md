# Feature: Log Ingestion Pipeline Read Tools — Context & Discussion

## Original Prompt
> Implement two read-only MCP tools for SigNoz log ingestion pipelines.
>
> 1. `signoz_list_log_pipelines` — read-only. Fetches the latest version and returns per-pipeline
>    **summaries only** (`id`, `name`, `alias`, `enabled`, `orderId`, `description`, and an operator
>    count e.g. `operatorCount`), plus the version-level context (`version`, `deployStatus`) so an
>    agent knows which config version it is looking at. Deliberately does NOT return full operator
>    chains — that is the whole point of splitting the two tools (keeps the common list case cheap).
>    Paginate the pipelines array. Params: `searchContext`, `limit`, `offset`, optionally
>    `enabledOnly`.
> 2. `signoz_get_log_pipeline` — read-only. Fetches the same version and selects ONE pipeline
>    **client-side** by `id` OR `name` (exactly one required; if both given, `id` wins). Returns that
>    pipeline's COMPLETE object including full `filter` and `config` operator array. If no pipeline
>    matches, return a NOT_FOUND coded error whose message lists the available pipeline names/ids so
>    the agent can self-correct — do not return an empty success. Params: `searchContext`, `id`,
>    `name`.
>
> Upstream API facts: the only read route is `GET /api/v1/logs/pipelines/{version}` with `{version}`
> REQUIRED (use the literal `latest`). The response is wrapped
> `{"status":"success","data":{...}}`, where `data` flattens the agent-config-version fields (`id`,
> `version`, `deployStatus`, `deploySequence`, `createdAt`, `createdBy`, ...) plus `pipelines` (each
> with `id`, `orderId`, `enabled`, `name`, `alias`, `description`, timestamps, a QB-v3 `filter`
> FilterSet, and a `config` array of `type`-discriminated operator objects) and `history`. There is
> NO get-one-pipeline-by-id endpoint — both tools call the same endpoint.
>
> This endpoint is undocumented, so the parse must be defensive: accept the wrapped form AND a bare
> unwrapped object, WARN when the response does not have the expected shape, and keep an empty
> pipeline list distinguishable from a shape change (fail open, never fail silent).

## Reference Links
- [Issue #270 — log ingestion pipeline read tools](https://github.com/SigNoz/signoz-mcp-server/issues/270)
- SigNoz backend route: `SigNoz/signoz` → `pkg/query-service/app/http_handler.go:3157`
  (`GET /api/v1/logs/pipelines/{version}`)

## Key Decisions & Discussion Log

### 2026-08-03 — API shape research and the two-tool split
- **Key finding: there is no get-one-pipeline-by-id endpoint upstream.** The only read route is
  `GET /api/v1/logs/pipelines/{version}`, and `{version}` is a required path segment. Both new tools
  therefore hit the *same* route with the literal version `latest`, and `signoz_get_log_pipeline`
  selects its pipeline **client-side** from that payload.
- **The two-tool split is justified by token cost, not by API shape.** Because one request returns
  every pipeline with its complete operator chain, a single tool would force the common question
  ("which pipelines exist / is X enabled / in what order do they run?") to pay for every regex,
  Grok pattern, and router route in the workspace. `signoz_list_log_pipelines` returns summaries plus
  `operatorCount`, and `signoz_get_log_pipeline` is the only path that returns `filter` and `config`.
  The list tool is tested to *not* contain `config`/`filter` so this contract cannot silently regress.
- **Version-level context is part of the list payload.** `version` and `deployStatus` are lifted to
  the top level of the list result so an agent always knows which deployed agent config it read.
  Without it, a stale or mid-deploy configuration is indistinguishable from the live one.
- **`id` wins over `name`** when both are supplied (rather than erroring), and `name` matching is
  case-insensitive and also accepts the pipeline `alias` — pipeline names are human-typed display
  strings, so exact-only matching would produce avoidable NOT_FOUNDs. This precedence is pinned by a
  test.
- **No-match returns `NOT_FOUND` listing every available `name (id=...)`**, never an empty success.
  An empty success reads as "this pipeline exists and is empty" and gives the agent nothing to
  recover with.
- **Defensive parse for an undocumented endpoint** (CLAUDE.md → "Testing across external contracts").
  `parseLogPipelinesResponse` accepts the wrapped `{"status":...,"data":{...}}` form and a bare
  unwrapped `{...}` object (older/legacy builds and some proxies), and emits a `WarnContext` for each
  unrecognised shape: `data` present but not an object, no wrapper *and* no `pipelines` key, no
  `pipelines` key in the body, and a non-array `pipelines` value. The result carries
  `pipelinesFieldPresent`, which makes "this workspace has zero pipelines" (`true`) distinguishable
  from "upstream renamed or reshaped the field" (`false`). Parsing fails open — a shape change yields
  an empty list with a WARN and a `false` flag rather than an error — but never fails silent.
- **Pipeline objects are passed through as `map[string]any` / `[]any`**, never bound to a
  hand-written struct, so new upstream operator types and fields survive to the client untouched. No
  `mcp.WithOutputSchema[T]()` was added, to avoid touching the pinned
  `expectedOutputSchemaTools` list.
- **`enabledOnly` is filtered before pagination** so `pagination.total` reflects the filtered set and
  page walking stays consistent. It uses `boolOrStringType()` + `parseTriStateBool`, matching
  `alerts.go`, so a garbage value hard-errors instead of silently widening the result.
- No guardrail was relaxed for this change.

## Open Questions
- [x] Is there a get-one-pipeline endpoint we should prefer for `signoz_get_log_pipeline`? —
      **Resolved: no.** Only `GET /api/v1/logs/pipelines/{version}` exists; selection is client-side.
- [x] Should `{version}` be exposed as a tool parameter? — **Resolved: no, not in this PR.** Both
      tools pin the literal `latest`. The client method takes a `version` argument, so exposing
      historical versions (the `history` array) is a later additive change, not a rework.
- [x] Should supplying both `id` and `name` be an error? — **Resolved: no**, `id` wins; pinned by
      `TestHandleGetLogPipeline_IDWinsOverName`.
