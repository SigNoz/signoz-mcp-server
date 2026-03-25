# Changelog

All notable changes to the SigNoz MCP Server will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-03-25

### Deprecated
- **`Authorization: Bearer <key>` header for API key authentication.** Use the `SIGNOZ-API-KEY` header instead. The `Authorization` header will stop working in the next release. ([#86](https://github.com/SigNoz/signoz-mcp-server/pull/86))

### Added
- JWT token support and `SIGNOZ-API-KEY` header for authentication ([#86](https://github.com/SigNoz/signoz-mcp-server/pull/86))
- Stateless OAuth 2.1 authentication for MCP clients ([#84](https://github.com/SigNoz/signoz-mcp-server/pull/84))
- `searchContext` param and session ID enrichment for logs ([#103](https://github.com/SigNoz/signoz-mcp-server/pull/103))
- State filter param for `signoz_get_alert_history` tool ([#100](https://github.com/SigNoz/signoz-mcp-server/pull/100))
- Server-side filtering params for `signoz_list_alerts` tool ([#99](https://github.com/SigNoz/signoz-mcp-server/pull/99))
- `signoz_query_metrics` tool with smart aggregation defaults ([#88](https://github.com/SigNoz/signoz-mcp-server/pull/88))
- Consolidated metrics tools and generic field discovery tools ([#85](https://github.com/SigNoz/signoz-mcp-server/pull/85))
- Filter support for `signoz_get_trace_error_analysis` ([#80](https://github.com/SigNoz/signoz-mcp-server/pull/80))
- `signoz_execute_builder_query` docs as MCP resource ([#79](https://github.com/SigNoz/signoz-mcp-server/pull/79))
- Prompts, resource templates, hooks, and retry with backoff ([#95](https://github.com/SigNoz/signoz-mcp-server/pull/95))
- `selectFields` for spans ([#74](https://github.com/SigNoz/signoz-mcp-server/pull/74))
- Handler unit tests, mock client, and integration tests ([#96](https://github.com/SigNoz/signoz-mcp-server/pull/96))

### Fixed
- Set `destructiveHint=false` on all read-only tools ([#102](https://github.com/SigNoz/signoz-mcp-server/pull/102))
- Remove `selectFields` params in logs ([#72](https://github.com/SigNoz/signoz-mcp-server/pull/72))
- Remove hardcoded `scalar` resultType from trace and logs aggregate tools ([#82](https://github.com/SigNoz/signoz-mcp-server/pull/82))

### Changed
- Remove `signoz_get_logs_for_alert` tool ([#104](https://github.com/SigNoz/signoz-mcp-server/pull/104))
- Merge redundant tools (26 → 22) ([#92](https://github.com/SigNoz/signoz-mcp-server/pull/92))
- Split `handler.go`, add Client interface and middleware ([#94](https://github.com/SigNoz/signoz-mcp-server/pull/94))
- Add tool annotations, extract `doRequest` helper, name constants ([#93](https://github.com/SigNoz/signoz-mcp-server/pull/93))
- Delegate step interval to backend and enhance server instructions ([#97](https://github.com/SigNoz/signoz-mcp-server/pull/97))
- Clarify signal before querying if not clear ([#91](https://github.com/SigNoz/signoz-mcp-server/pull/91))
- Enhance context handling for API key and SigNoz URL in middleware ([#63](https://github.com/SigNoz/signoz-mcp-server/pull/63))
- Feature brainstorming and CLAUDE.md ([#87](https://github.com/SigNoz/signoz-mcp-server/pull/87))

### Documentation
- Add jsonschema description for legend field ([#83](https://github.com/SigNoz/signoz-mcp-server/pull/83))
- Update `SIGNOZ_URL` example in README for clarity ([#57](https://github.com/SigNoz/signoz-mcp-server/pull/57))

### CI
- Split builds of binary and docker ([#78](https://github.com/SigNoz/signoz-mcp-server/pull/78))
- Add main branch to build workflow triggers ([#77](https://github.com/SigNoz/signoz-mcp-server/pull/77))
- Push images to GCP repository ([#75](https://github.com/SigNoz/signoz-mcp-server/pull/75))

## [0.0.5] - 2026-02-20

### Added
- Aggregate logs and generic log search tools ([#54](https://github.com/SigNoz/signoz-mcp-server/pull/54))
- Support for getting real metrics/logs/trace fields and options
- Create and update dashboard tools ([#43](https://github.com/SigNoz/signoz-mcp-server/pull/43))
- SigNoz MCP Server as Claude Desktop extension ([#33](https://github.com/SigNoz/signoz-mcp-server/pull/33))
- `server.json` for MCP registry ([#47](https://github.com/SigNoz/signoz-mcp-server/pull/47))

### Fixed
- Allow `time_series` aggregations for logs ([#55](https://github.com/SigNoz/signoz-mcp-server/pull/55))
- Trim trailing slashes from `SIGNOZ_URL` to prevent double-slash issues ([#50](https://github.com/SigNoz/signoz-mcp-server/pull/50))

### Changed
- Prefix all tool names with `signoz_` ([#48](https://github.com/SigNoz/signoz-mcp-server/pull/48))

## [0.0.4] - 2025-11-10

### Added
- Pagination support for client ([#44](https://github.com/SigNoz/signoz-mcp-server/pull/44))

### Fixed
- Query builder for metrics ([#45](https://github.com/SigNoz/signoz-mcp-server/pull/45))
- Golang CI lint issues ([#46](https://github.com/SigNoz/signoz-mcp-server/pull/46))

## [0.0.3] - 2025-10-31

### Fixed
- Metric search by text ([#42](https://github.com/SigNoz/signoz-mcp-server/pull/42))

### Changed
- Address community feedback ([#41](https://github.com/SigNoz/signoz-mcp-server/pull/41))

## [0.0.2] - 2025-10-24

### Changed
- Add MCP registry label ([#39](https://github.com/SigNoz/signoz-mcp-server/pull/39))

### CI
- Add Docker build workflow ([#38](https://github.com/SigNoz/signoz-mcp-server/pull/38))
- Add additional CI workflows ([#37](https://github.com/SigNoz/signoz-mcp-server/pull/37))
- Fix id-token permission ([#40](https://github.com/SigNoz/signoz-mcp-server/pull/40))

## [0.0.1] - 2025-10-23

### Added
- Initial release of SigNoz MCP Server
- Query builder tool for metrics, logs, and traces
- Alert listing and alert history tools
- Log search and error log tools
- Trace search, detail, error analysis, and span hierarchy tools
- Service listing and top operations tools
- Dashboard listing and retrieval tools
- Saved log views listing and retrieval
- HTTP transport support for remote MCP server
- API key authentication via Authorization header
- Enhanced time input handling for LLM-to-API calls
- LLM-friendly error messages and logging

### CI
- Primus workflow for publishing artifacts

[Unreleased]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.5...v0.1.0
[0.0.5]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/SigNoz/signoz-mcp-server/releases/tag/v0.0.1
