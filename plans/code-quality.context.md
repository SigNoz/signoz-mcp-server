# Feature: Code Quality & MCP Best Practices — Context & Discussion

## Original Prompt
> Go through the repo structure and suggest what can be improved from code organization POV. Think like a staff engineer who writes scalable code. What more practices can be added? Also, look into other MCP server practices being followed and suggest if any feature/pattern can be used which is specific to MCP.

## Reference Links
- [mcp-go library v0.38.0](https://github.com/mark3labs/mcp-go) — Go MCP SDK used by this server
- [MCP Specification](https://modelcontextprotocol.io/specification) — official protocol spec

## Key Decisions & Discussion Log

### 2026-03-24 — Initial staff engineer review

**Codebase stats:** ~30 Go files, ~10,000 lines, 22 registered tools, 13 static resources.

**Strengths identified:**
- Clean entry point and dependency injection (main.go → config → telemetry → handler → server)
- No circular dependencies — clean top-down dependency graph
- Structured logging with zap + multi-tenant context enrichment
- OpenTelemetry integration for tracing/metrics
- SSRF protection in URL validation middleware
- Feature planning discipline (plans/ directory)

**Anti-patterns identified:**
1. **handler.go is a 1486-line god file** — 8 registration functions with inline tool definitions and closures
2. **client.go repeats HTTP request boilerplate ~20 times** — create request → set headers → timeout → execute → check status → read body
3. **No interfaces anywhere** — tight coupling between handler and client, impossible to unit test handlers
4. **Test coverage at ~17%** — only client.go and metricsrules have real tests, handler logic untested
5. **No retry logic in HTTP client** — single network hiccup fails the entire tool call
6. **Magic numbers** — 600s/30s timeouts, limit caps scattered without named constants
7. **ListDashboards potential bug** — reads response body twice (inefficient, could lose data on non-seekable readers)

**MCP features unused but available in mcp-go v0.38.0:**
- Tool Annotations (ReadOnlyHint, DestructiveHint, IdempotentHint)
- Prompts (reusable LLM interaction templates)
- Resource Templates (dynamic URI-based resources)
- Hooks (OnBeforeCallTool, OnAfterCallTool — lifecycle management)
- Tool Handler Middleware (centralized middleware chain)
- Tool Filtering (per-session tool visibility)
- Progress Reporting (for long-running operations)
- Resource Subscriptions and Change Notifications
- Server-enforced Pagination Limit

### 2026-03-24 — Prioritization discussion

Agreed on three tiers:
- **Quick wins:** tool annotations, tool handler middleware, extract doRequest helper
- **Medium effort:** split handler.go, add prompts, define SigNozClient interface
- **Larger investments:** resource templates, hooks for observability, retry with backoff, test coverage

### 2026-03-24 — Phase 4 (Testing) implemented

- Created MockClient in `internal/client/mock.go` implementing `client.Client` with function fields for each method
- Added `clientOverride` field to Handler struct — when set, GetClient returns it directly, bypassing cache/context. This is test-only plumbing that keeps production code unchanged.
- Created test utility helpers: `newTestHandler`, `makeToolRequest`, `testCtx` in `testutil_test.go`
- Wrote 31 handler unit tests across 3 files: logs (11 tests), traces (10 tests), alerts (10 tests)
  - Tests cover: happy paths, parameter validation, missing/empty required params, invalid values, client errors, pagination, filter combinations
- Wrote 3 integration tests using `NewInProcessClient` for full MCP protocol round-trips: initialize + list 22 tools, list 4 prompts, list 2 resource templates
- All tests pass: `go build ./...`, `go vet ./...`, `go test ./...` all clean

## Open Questions
- [ ] Should prompts target specific personas (on-call engineer, platform engineer, developer)?
- [ ] Should resource templates replace some existing static resources, or be additive?
- [ ] What retry policy makes sense? (number of attempts, backoff strategy, which status codes to retry)
- [ ] Should tool filtering be based on API key permissions from SigNoz backend?
