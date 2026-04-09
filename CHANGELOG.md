# Changelog

All notable changes to the SigNoz MCP Server will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [V0.1.1] - 2026-04-09

### Added
- add JWT token support and refactor authentication header handling ([#86](https://github.com/SigNoz/signoz-mcp-server/pull/86))
- add searchContext param and enrich logs with session ID ([#103](https://github.com/SigNoz/signoz-mcp-server/pull/103))
- add state filter param to signoz_get_alert_history tool ([#100](https://github.com/SigNoz/signoz-mcp-server/pull/100))
- add server-side filtering params to signoz_list_alerts tool ([#99](https://github.com/SigNoz/signoz-mcp-server/pull/99))
- Add stateless OAuth 2.1 authentication for MCP clients ([#84](https://github.com/SigNoz/signoz-mcp-server/pull/84))
- delegate step interval to backend and enhance server instructions ([#97](https://github.com/SigNoz/signoz-mcp-server/pull/97))
- add prompts, resource templates, hooks, and retry with backoff ([#95](https://github.com/SigNoz/signoz-mcp-server/pull/95))
- add signoz_query_metrics tool with smart aggregation defaults ([#88](https://github.com/SigNoz/signoz-mcp-server/pull/88))
- consolidate metrics tools and add generic field discovery tools ([#85](https://github.com/SigNoz/signoz-mcp-server/pull/85))
- add filter support to signoz_get_trace_error_analysis ([#80](https://github.com/SigNoz/signoz-mcp-server/pull/80))
- move signoz_execute_builder_query docs to MCP resource ([#79](https://github.com/SigNoz/signoz-mcp-server/pull/79))
- enhance context handling for API key and SigNoz URL in middleware ([#63](https://github.com/SigNoz/signoz-mcp-server/pull/63))
- add create/update dashboard tools to MCP server ([#43](https://github.com/SigNoz/signoz-mcp-server/pull/43))
- be able to pass api key over authorization header ([#27](https://github.com/SigNoz/signoz-mcp-server/pull/27))

### Fixed
- use service account endpoint for API key validation ([#112](https://github.com/SigNoz/signoz-mcp-server/pull/112))
- set destructiveHint=false on all read-only tools ([#102](https://github.com/SigNoz/signoz-mcp-server/pull/102))
- remove the selectFields params in logs ([#72](https://github.com/SigNoz/signoz-mcp-server/pull/72))
- allow time_series aggregations for logs ([#55](https://github.com/SigNoz/signoz-mcp-server/pull/55))
- trim trailing slashes from SIGNOZ_URL to prevent double-slash issues ([#50](https://github.com/SigNoz/signoz-mcp-server/pull/50))

### Changed
- remove signoz_get_logs_for_alert tool ([#104](https://github.com/SigNoz/signoz-mcp-server/pull/104))
- split handler.go, add Client interface and middleware ([#94](https://github.com/SigNoz/signoz-mcp-server/pull/94))
- add tool annotations, extract doRequest helper, name constants ([#93](https://github.com/SigNoz/signoz-mcp-server/pull/93))
- merge redundant tools (26 → 22) ([#92](https://github.com/SigNoz/signoz-mcp-server/pull/92))

### Documentation
- add changelog and clean up README ([#105](https://github.com/SigNoz/signoz-mcp-server/pull/105))
- add jsonschema description for legend field ([#83](https://github.com/SigNoz/signoz-mcp-server/pull/83))
- update SIGNOZ_URL example in README for clarity ([#57](https://github.com/SigNoz/signoz-mcp-server/pull/57))

### CI
- split builds of binary and docker ([#78](https://github.com/SigNoz/signoz-mcp-server/pull/78))
- add main branch to build workflow triggers ([#77](https://github.com/SigNoz/signoz-mcp-server/pull/77))
- push images to gcp repository ([#75](https://github.com/SigNoz/signoz-mcp-server/pull/75))
- fix id-token permission ([#40](https://github.com/SigNoz/signoz-mcp-server/pull/40))
- add docker build ([#38](https://github.com/SigNoz/signoz-mcp-server/pull/38))
- add more workflows ([#37](https://github.com/SigNoz/signoz-mcp-server/pull/37))
- fix name of goreleaser file ([#36](https://github.com/SigNoz/signoz-mcp-server/pull/36))
- fix main build path ([#35](https://github.com/SigNoz/signoz-mcp-server/pull/35))
- add primus workflow for publishing artifact ([#34](https://github.com/SigNoz/signoz-mcp-server/pull/34))

### Other
- simplify MCP link guidance and goimports setup ([#109](https://github.com/SigNoz/signoz-mcp-server/pull/109))
- add signoz cloud setup to readme ([#107](https://github.com/SigNoz/signoz-mcp-server/pull/107))
- bump version to v0.1.0 and update manifest ([#106](https://github.com/SigNoz/signoz-mcp-server/pull/106))
- add handler unit tests, mock client, and integration tests ([#96](https://github.com/SigNoz/signoz-mcp-server/pull/96))
- clarify the signal before querying if not clear ([#91](https://github.com/SigNoz/signoz-mcp-server/pull/91))
- Feature brainstorming and claude.md ([#87](https://github.com/SigNoz/signoz-mcp-server/pull/87))
- Remove hardcoded `scalar` resultType from trace and logs aggregate tools ([#82](https://github.com/SigNoz/signoz-mcp-server/pull/82))
- added selectFields for spans ([#74](https://github.com/SigNoz/signoz-mcp-server/pull/74))
- adds two new tools to aggregate logs and a generic log search ([#54](https://github.com/SigNoz/signoz-mcp-server/pull/54))
- Add support for getting real metrics/logs/trace fields + options ([#49](https://github.com/SigNoz/signoz-mcp-server/pull/49))
- ✨ feat: Prefix all tool names with `signoz_` ([#48](https://github.com/SigNoz/signoz-mcp-server/pull/48))
- adds server.json required for mcp registry ([#47](https://github.com/SigNoz/signoz-mcp-server/pull/47))
- add SigNoz MCP Server as Claude Desktop extension ([#33](https://github.com/SigNoz/signoz-mcp-server/pull/33))
- fix golang ci lint issue ([#46](https://github.com/SigNoz/signoz-mcp-server/pull/46))
- fix query builder for metric ([#45](https://github.com/SigNoz/signoz-mcp-server/pull/45))
- introduce pagination to client ([#44](https://github.com/SigNoz/signoz-mcp-server/pull/44))
- fix metric search by text. ([#42](https://github.com/SigNoz/signoz-mcp-server/pull/42))
- address feedbacks ([#41](https://github.com/SigNoz/signoz-mcp-server/pull/41))
- add label for mcp registry ([#39](https://github.com/SigNoz/signoz-mcp-server/pull/39))
- fix mcp looping with client ([#32](https://github.com/SigNoz/signoz-mcp-server/pull/32))
- logs and error message more understandable to LLMs ([#30](https://github.com/SigNoz/signoz-mcp-server/pull/30))
- this enhance time input from LLMs to our api calls ([#29](https://github.com/SigNoz/signoz-mcp-server/pull/29))
- this adds tools related to trace ([#28](https://github.com/SigNoz/signoz-mcp-server/pull/28))
- improves query builder call ([#24](https://github.com/SigNoz/signoz-mcp-server/pull/24))
- adds missing documents ([#25](https://github.com/SigNoz/signoz-mcp-server/pull/25))
- enhance logs and query builder prompt ([#21](https://github.com/SigNoz/signoz-mcp-server/pull/21))
- fix query builder. ([#18](https://github.com/SigNoz/signoz-mcp-server/pull/18))
- adds test for client. ([#15](https://github.com/SigNoz/signoz-mcp-server/pull/15))
- add doc for self hosted http mcp server ([#12](https://github.com/SigNoz/signoz-mcp-server/pull/12))
- fix alerts history ([#14](https://github.com/SigNoz/signoz-mcp-server/pull/14))
- enables http transport for signoz-mcp-server ([#9](https://github.com/SigNoz/signoz-mcp-server/pull/9))
- adds logs tool ([#11](https://github.com/SigNoz/signoz-mcp-server/pull/11))
- this adds alert history tool ([#10](https://github.com/SigNoz/signoz-mcp-server/pull/10))
- Adds querybuilder tool ([#7](https://github.com/SigNoz/signoz-mcp-server/pull/7))
- this adds years and owner in license ([#8](https://github.com/SigNoz/signoz-mcp-server/pull/8))
- Create LICENSE ([#4](https://github.com/SigNoz/signoz-mcp-server/pull/4))
- this update readme and logs ([#6](https://github.com/SigNoz/signoz-mcp-server/pull/6))
- Update README with API key location ([#5](https://github.com/SigNoz/signoz-mcp-server/pull/5))
- Update README.md ([#2](https://github.com/SigNoz/signoz-mcp-server/pull/2))
- remove .idea
-  signoz mcp server

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

[0.1.0]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.5...v0.1.0
[0.0.5]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/SigNoz/signoz-mcp-server/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/SigNoz/signoz-mcp-server/releases/tag/v0.0.1
[V0.1.1]: https://github.com/SigNoz/signoz-mcp-server/releases/tag/V0.1.1
