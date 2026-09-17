# Plan: Log Ingestion Pipeline Read Tools

## Status
Done

## Context

SigNoz log-ingestion pipelines decide how raw log bodies become structured attributes: which logs a
pipeline matches (`filter`) and the ordered chain of operators applied (`config` — regex/JSON/Grok
parsers, severity and timestamp parsing, add/remove/move, routers). Agents debugging "why is
`service.name` missing from my logs?" or "why is this field not parsed?" currently have no way to read
that configuration through this server, so they cannot connect a log-query result back to the pipeline
that shaped it.

The upstream read surface is a single route, `GET /api/v1/logs/pipelines/{version}` (required path
segment; `latest` for the deployed config). It returns *every* pipeline with its *complete* operator
chain in one payload, and there is no fetch-one-pipeline endpoint. Exposing that raw payload as one
tool would make the cheap, common question ("which pipelines exist and are they enabled?") pay for
every regex and router route in the workspace.

## Approach

Two read-only tools over one client method.

1. **`signoz_list_log_pipelines`** — fetches `latest`, projects each pipeline to a summary
   (`id`, `name`, `alias`, `enabled`, `orderId`, `description`, `operatorCount` = `len(config)`), and
   paginates via `paginate.ParseParamsClamped` → `paginate.Array` → `paginate.Wrap` → `listResult`.
   `version`, `deployStatus`, and `pipelinesFieldPresent` are attached to the envelope alongside
   `data`/`pagination`. Optional `enabledOnly` (`boolOrStringType()` + `parseTriStateBool`, as in
   `alerts.go`) filters before pagination so `total` matches the filtered set. Never returns `filter`
   or `config`.
2. **`signoz_get_log_pipeline`** — fetches the same payload and selects one pipeline client-side by
   `id` (exact) or `name` (case-insensitive, alias also accepted). Exactly one is required; `id` wins
   when both are given. Returns the pipeline object verbatim via `structuredResult`, including full
   `filter` and `config`. No match → `notFoundError` whose message enumerates every available
   `name (id=...)`.

**Defensive parse (`parseLogPipelinesResponse`).** The endpoint is undocumented, so the response is
decoded into `map[string]any` with a `UseNumber()` decoder (large SigNoz integers stay exact) and:
accepts both the wrapped `{"status":"success","data":{...}}` form and a bare unwrapped `{...}` object;
emits `WarnContext` for every unrecognised shape (non-object `data`, no wrapper and no `pipelines`,
missing `pipelines` key, non-array `pipelines`, non-object array entries); and reports
`pipelinesFieldPresent` so an empty list is distinguishable from a shape change. Fails open (empty
list + WARN, not an error), never fails silent. Pipelines are passed through as `map[string]any` /
`[]any` so unknown upstream fields survive; no output schema is declared.

Both tools carry `withReadOnlyToolAnnotations()`, the verbatim `searchContext` string option, register
through `h.addTool`, and never return a non-nil Go error.

## Files to Modify

- `internal/client/client.go` — add `GetLogPipelines(ctx, version)` issuing
  `GET {baseURL}/api/v1/logs/pipelines/{url.PathEscape(version)}` via `doRequest` with
  `DefaultQueryTimeout`.
- `internal/client/interface.go` — add `GetLogPipelines` to the `Client` interface.
- `internal/client/mock.go` — add `GetLogPipelinesFn` field and the mock method (returns `{}` when the
  Fn is nil).
- `internal/handler/tools/log_pipelines.go` — **new**: `RegisterLogPipelineHandlers`, both handlers,
  `parseLogPipelinesResponse`, `logPipelineSummary`, `withConfigVersionContext`.
- `internal/handler/tools/register.go` — call `RegisterLogPipelineHandlers` at the end of
  `RegisterAllToolHandlers`.
- `internal/handler/tools/annotations_inventory_test.go` — pin both tools as `readTriple`.
- `internal/handler/tools/nil_arguments_test.go` — add `signoz_get_log_pipeline` to the
  requires-an-argument table (not the list tool, whose params are all optional).
- `internal/handler/tools/log_pipelines_test.go` — **new**: handler tests.
- `internal/client/client_test.go` — `TestGetLogPipelines`.
- `manifest.json` — both tools in the `tools` array, beside the other logs tools.
- `README.md` — summary-table rows plus per-tool detail sections.
- `plans/log-pipelines.context.md`, `plans/log-pipelines.plan.md` — this file pair.

## Verification

Handler tests cover: list returns summaries and provably *not* `config`/`filter`/`history`; list
paginates (`limit=1&offset=1`); `enabledOnly` in bool and string forms plus a garbage value →
`VALIDATION_FAILED`; get-by-id returns the complete 2-operator chain and full filter with
`StructuredContent` set; get-by-name exact, case-insensitive, and by alias; `id` wins over `name`;
both selectors absent/blank → `VALIDATION_FAILED`; no match → `NOT_FOUND` naming every available
pipeline; zero pipelines → `NOT_FOUND` saying so; unwrapped legacy envelope parses for both tools;
empty array / explicit null / renamed key / non-array value → `pipelinesFieldPresent` true/true/false/false
with `total:0` and no error; unparseable and non-object bodies → error results; upstream error
propagates through `upstreamError` for both tools.

Client test is table-driven over `httptest.NewServer`, asserting `http.MethodGet`, path
`/api/v1/logs/pipelines/latest`, and the `SIGNOZ-API-KEY` header, with success, empty, 500, and 401
cases.

Commands run:

```
go build ./...
go test ./...
go test -count=1 -run '^TestGuardrail_' ./...
gofmt -l .
```

Live verification against a real SigNoz workspace (confirming a real `pipelines` payload matches the
parsed shape, per CLAUDE.md) is still outstanding and should be done by a subagent with credentials.
