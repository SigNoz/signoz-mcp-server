# Plan: Code Quality & MCP Best Practices

## Status
Planning

## Context
Staff engineer review of the SigNoz MCP server identified code organization improvements and unused MCP protocol features. This plan covers both internal refactoring and MCP-specific enhancements that improve the developer experience for MCP clients consuming this server.

---

## Phase 1 — Quick Wins

### 1.1 Add Tool Annotations
Mark read-only tools with `ReadOnlyHint=true` so MCP clients understand tool safety. Currently all 22 tools default to `destructive=true`.

**Read-only tools** (should be annotated):
- All `list_*`, `get_*`, `search_*`, `aggregate_*`, `execute_builder_query`

**Write tools** (keep defaults or mark `DestructiveHint=true`):
- `signoz_create_dashboard`, `signoz_update_dashboard`

**Files:** `internal/handler/tools/handler.go` — add `mcp.WithAnnotations(...)` to each `mcp.NewTool()` call.

### 1.2 Add Tool Handler Middleware
Use `server.WithToolHandlerMiddleware()` to centralize:
- Client retrieval (`h.GetClient(ctx)`)
- Structured logging (tool name, duration, error)
- Telemetry (tool call counters, latency histograms)
- Error wrapping

This eliminates the repeated boilerplate in every handler callback.

**Files:** `internal/mcp-server/server.go` — add middleware option to `NewMCPServer()`.

### 1.3 Extract `doRequest` Helper in Client
Replace ~20 identical HTTP request patterns with a single method.

```go
func (s *SigNoz) doRequest(ctx context.Context, method, url string, body io.Reader, timeout time.Duration) (json.RawMessage, error)
```

**Files:** `internal/client/client.go` — add helper, refactor all methods to use it.

---

## Phase 2 — Structural Refactoring

### 2.1 Split handler.go Into Domain Files
Move each `Register*Handlers` method and its tool definitions into separate files:

```
internal/handler/tools/
├── handler.go           → Handler struct, GetClient, tenantLogger, shared helpers
├── metrics.go           → RegisterMetricsHandlers
├── alerts.go            → RegisterAlertsHandlers
├── dashboards.go        → RegisterDashboardHandlers
├── logs.go              → RegisterLogsHandlers
├── traces.go            → RegisterTracesHandlers
├── services.go          → RegisterServiceHandlers
├── fields.go            → RegisterFieldsHandlers
└── query_builder.go     → RegisterQueryBuilderV5Handlers
```

**Files:** `internal/handler/tools/handler.go` — split into 9 files.

### 2.2 Define SigNozClient Interface
Extract an interface from the concrete `SigNoz` client:

```go
type SigNozClient interface {
    ListMetrics(ctx context.Context, params url.Values) (json.RawMessage, error)
    QueryBuilderV5(ctx context.Context, body []byte) (json.RawMessage, error)
    // ... all public methods
}
```

Handler accepts the interface → enables mocking in tests.

**Files:**
- `internal/client/interface.go` — new file with interface definition
- `internal/handler/tools/handler.go` — change `GetClient` return type

### 2.3 Add Prompts for Common Workflows
Register MCP prompts for common observability tasks:

- `debug_service_errors` — "Investigate errors for a service" (takes service name arg)
- `latency_analysis` — "Analyze p99 latency for a service" (takes service name, time range)
- `compare_metrics` — "Compare a metric across two time periods" (takes metric name, periods)
- `incident_triage` — "Triage an active alert" (takes alert ID)

**Files:**
- `pkg/prompts/prompts.go` — new package with prompt definitions
- `internal/handler/tools/handler.go` — register prompts via `s.AddPrompt()`

---

## Phase 3 — Advanced MCP Features

### 3.1 Resource Templates
Add dynamic resources using URI templates:

- `signoz://service/{name}/overview` — fetches service health, top operations, error rate
- `signoz://alert/{ruleId}/summary` — fetches alert config + recent history
- `signoz://dashboard/{uuid}/summary` — fetches dashboard metadata + widget list

These are more useful than static guides because they provide live data.

**Files:**
- `internal/handler/tools/handler.go` — add `s.AddResourceTemplate()` calls
- May need new client methods for aggregated data

### 3.2 Lifecycle Hooks for Observability
Use `server.WithHooks()` to add:

- `OnBeforeCallTool` — start span, log tool name
- `OnAfterCallTool` — record duration, log result size, increment counters
- `OnError` — log errors with full context
- `OnRegisterSession` / `OnUnregisterSession` — track active sessions

**Files:** `internal/mcp-server/server.go` — define hooks and pass via `WithHooks()`.

### 3.3 Retry with Exponential Backoff
Add retry logic to `doRequest` for transient failures:
- Retry on: 429 (rate limited), 502/503/504 (server errors), network timeouts
- Max 3 attempts, exponential backoff (100ms, 400ms, 1600ms)
- No retry on: 4xx client errors, context cancellation

**Files:** `internal/client/client.go` — add retry wrapper around `doRequest`.

---

## Phase 4 — Testing & Hardening

### 4.1 Add Handler Unit Tests — DONE
- Created `internal/client/mock.go` — MockClient implementing Client with function fields
- Added `clientOverride` field to Handler for test injection (bypasses cache/context lookup)
- Created `internal/handler/tools/testutil_test.go` — test helpers (newTestHandler, makeToolRequest, testCtx)
- Created `internal/handler/tools/logs_test.go` — 11 tests: search, aggregate, list views, get view, validation
- Created `internal/handler/tools/traces_test.go` — 10 tests: search, aggregate, trace details, validation
- Created `internal/handler/tools/alerts_test.go` — 10 tests: list, get, history, pagination, validation, errors

### 4.2 Add Integration Tests — DONE
- Created `internal/mcp-server/integration_test.go`
- Tests: Initialize + list 22 tools, list 4 prompts, list 2 resource templates
- Uses `NewInProcessClient` from mcp-go for full MCP protocol round-trip

### 4.3 Extract and Name All Constants
```go
const (
    DefaultQueryTimeout     = 600 * time.Second
    DashboardWriteTimeout   = 30 * time.Second
    DefaultLimit            = 100
    MaxErrorLogsLimit       = 200
    DefaultTraceSpanLimit   = 1000
)
```

---

## Verification
Each phase should independently compile and pass:
- `go build ./...`
- `go vet ./...`
- `go test ./...`

Phase 1 and 2 are independent of each other. Phase 3 depends on Phase 2 (interface for hooks). Phase 4 depends on Phase 2 (interface for mocking).
