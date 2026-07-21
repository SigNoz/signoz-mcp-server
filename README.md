# SigNoz MCP Server

[![Go Version](https://img.shields.io/badge/Go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![MCP Version](https://img.shields.io/badge/MCP-0.37.0-orange.svg)](https://modelcontextprotocol.io)

A Model Context Protocol (MCP) server that provides seamless access to SigNoz observability data through AI assistants and LLMs. Query metrics, traces, logs, alerts, dashboards, and services using natural language.

**[📖 Full Documentation](https://signoz.io/docs/ai/signoz-mcp-server/)**

## Table of Contents

- [Connect to SigNoz Cloud](#connect-to-signoz-cloud)
- [Self-Hosted Installation](#self-hosted-installation)
- [Connect to Self-Hosted SigNoz](#connect-to-self-hosted-signoz)
- [What Can You Do With It?](#what-can-you-do-with-it)
- [Available Tools](#available-tools)
- [Environment Variables](#environment-variables)
- [Claude Desktop Extension](#claude-desktop-extension)
- [Architecture](#architecture)
- [Contributing](#contributing)

## Connect to SigNoz Cloud

Connect your AI tool to SigNoz Cloud's hosted MCP server. No installation is required; just add the hosted MCP URL and authenticate.

```text
https://mcp.<region>.signoz.cloud/mcp
```

> Make sure you select the correct region that matches your SigNoz Cloud account. Using the wrong region will result in authentication failures.
>
> Find your region under **Settings → Ingestion** in SigNoz, or see the [SigNoz Cloud region reference](https://signoz.io/docs/ingestion/signoz-cloud/overview/#endpoint).

### One-Click Install Links

GitHub does not reliably make custom-protocol links like `cursor://` and `vscode:` clickable in README rendering.

Use the documentation page for one-click install buttons:

- [Open one-click install links for Cursor](https://signoz.io/docs/ai/signoz-mcp-server/#install-in-one-click)
- [Open one-click install links for VS Code](https://signoz.io/docs/ai/signoz-mcp-server/#install-in-one-click-1)

If you prefer, use the manual configuration examples below in this README.

### Cursor

#### Manual Configuration

Add this configuration to `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "signoz": {
      "url": "https://mcp.<region>.signoz.cloud/mcp"
    }
  }
}
```

Need help? See the [Cursor MCP docs](https://docs.cursor.com/context/model-context-protocol).

### VS Code / GitHub Copilot

#### Manual Configuration

Add this configuration to `.vscode/mcp.json`:

```json
{
  "servers": {
    "signoz": {
      "type": "http",
      "url": "https://mcp.<region>.signoz.cloud/mcp"
    }
  }
}
```

Need help? See the [VS Code MCP docs](https://code.visualstudio.com/docs/copilot/chat/mcp-servers).

### Claude Desktop

Add SigNoz Cloud as a custom connector in Claude Desktop:

1. Open Claude Desktop.
2. Go to **Settings → Developer** (or **Features**, depending on your version).
3. Click **Add Custom Connector** or **Add Remote MCP Server**.
4. Enter your SigNoz MCP URL: `https://mcp.<region>.signoz.cloud/mcp`

When prompted, complete the authentication flow.

### Claude Code

Run this command to add the hosted SigNoz MCP server:

```bash
claude mcp add --scope user --transport http signoz https://mcp.<region>.signoz.cloud/mcp
```

After configuring the MCP server, authenticate in a terminal:

```bash
claude /mcp
```

Select the `signoz` server and complete the authentication flow.

### OpenAI Codex

Run this command to add the hosted SigNoz MCP server:

```bash
codex mcp add signoz --url https://mcp.<region>.signoz.cloud/mcp
```

Or add this configuration to `config.toml`:

```toml
[mcp_servers.signoz]
url = "https://mcp.<region>.signoz.cloud/mcp"
```

After adding the server, authenticate:

```bash
codex mcp login signoz
```

Then run `/mcp` inside Codex to verify the connection.

### SigNoz Cloud Authentication

When you add the hosted MCP URL to your client, the client initiates an authentication flow. You will be prompted to enter:

1. Your SigNoz instance URL (for example, `your-instance.signoz.cloud`). Protocol-less URLs are accepted; paths, query parameters, and fragments are ignored.
2. Your API key

Create an API key in **Settings → API Keys** in SigNoz. Only **Admin** users can create API keys.

## Self-Hosted Installation

### Download Binary (Recommended)

Download the latest binary from [GitHub Releases](https://github.com/SigNoz/signoz-mcp-server/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/SigNoz/signoz-mcp-server/releases/latest/download/signoz-mcp-server_darwin_arm64.tar.gz | tar xz

# macOS (Intel)
curl -L https://github.com/SigNoz/signoz-mcp-server/releases/latest/download/signoz-mcp-server_darwin_amd64.tar.gz | tar xz

# Linux (amd64)
curl -L https://github.com/SigNoz/signoz-mcp-server/releases/latest/download/signoz-mcp-server_linux_amd64.tar.gz | tar xz
```

This extracts a `signoz-mcp-server` binary in the current directory. Move it somewhere on your PATH or note the absolute path for the config below.

### Go Install

```bash
go install github.com/SigNoz/signoz-mcp-server/cmd/server@latest
```

The binary is installed as `server` to `$GOPATH/bin/` (default: `$HOME/go/bin/server`). You may want to rename it:

```bash
mv "$(go env GOPATH)/bin/server" "$(go env GOPATH)/bin/signoz-mcp-server"
```

### Docker

Docker images are available on [Docker Hub](https://hub.docker.com/r/signoz/signoz-mcp-server/tags):

```bash
docker pull signoz/signoz-mcp-server:latest
```

Run in HTTP mode:

```bash
docker run -p 8000:8000 \
  -e TRANSPORT_MODE=http \
  -e MCP_SERVER_PORT=8000 \
  -e SIGNOZ_URL=https://your-signoz-instance.com \
  -e SIGNOZ_API_KEY=your-api-key \
  signoz/signoz-mcp-server:latest
```

Use a specific version tag (e.g. `v0.1.0`) instead of `latest` for pinned deployments.

### Build from Source

```bash
git clone https://github.com/SigNoz/signoz-mcp-server.git
cd signoz-mcp-server
make build
```

The binary is at `./bin/signoz-mcp-server`.

## Connect to Self-Hosted SigNoz

### Prerequisites

- A running [SigNoz](https://signoz.io) instance
- SigNoz v0.131.0 or newer for `signoz_check_metric_usage`
- SigNoz v0.120.0 or newer for alert-rule list/get/create/update/delete tools, and v0.118.0 or newer for alert history
- A SigNoz API key (Settings → API Keys in the SigNoz UI)
- The `signoz-mcp-server` binary (see [Self-Hosted Installation](#self-hosted-installation))

### Stdio Mode (Claude Desktop / Cursor / Any MCP Client)

Add this to your MCP client config (`claude_desktop_config.json`, `.cursor/mcp.json`, etc.). Replace the `command` path with the absolute path to your `signoz-mcp-server` binary:

```json
{
    "mcpServers": {
        "signoz": {
            "command": "/absolute/path/to/signoz-mcp-server",
            "args": [],
            "env": {
                "SIGNOZ_URL": "https://your-signoz-instance.com",
                "SIGNOZ_API_KEY": "your-api-key-here",
                "LOG_LEVEL": "info"
            }
        }
    }
}
```

### HTTP Mode

HTTP mode listens on all interfaces by default. Set `MCP_SERVER_HOST=127.0.0.1` when the server should accept loopback connections only.

#### With OAuth (Multi-Tenant / Cloud)

Start the server:

```bash
TRANSPORT_MODE=http \
MCP_SERVER_PORT=8000 \
OAUTH_ENABLED=true \
OAUTH_TOKEN_SECRET=$(openssl rand -base64 32) \
OAUTH_ISSUER_URL=https://your-public-mcp-url.com \
./signoz-mcp-server
```

Client config — just the URL, no keys needed:

```json
{
    "mcpServers": {
        "signoz": {
            "url": "https://your-public-mcp-url.com/mcp"
        }
    }
}
```

The client discovers OAuth endpoints automatically, opens a browser for credentials, and handles token exchange.

#### Without OAuth (Simple Setup)

The API key and SigNoz URL only need to be provided in **one** place — either on the server or on the client.

**Option A — Credentials on the server** (simpler client config):

```bash
SIGNOZ_URL=https://your-signoz-instance.com \
SIGNOZ_API_KEY=your-api-key \
TRANSPORT_MODE=http \
MCP_SERVER_PORT=8000 \
./signoz-mcp-server
```

```json
{
    "mcpServers": {
        "signoz": {
            "url": "http://localhost:8000/mcp"
        }
    }
}
```

**Option B — API key on the client** (server holds the URL, client sends the key):

```bash
SIGNOZ_URL=https://your-signoz-instance.com \
TRANSPORT_MODE=http \
MCP_SERVER_PORT=8000 \
./signoz-mcp-server
```

```json
{
    "mcpServers": {
        "signoz": {
            "url": "http://localhost:8000/mcp",
            "headers": {
                "SIGNOZ-API-KEY": "your-api-key-here"
            }
        }
    }
}
```

### HTTP Probe Endpoints

HTTP mode exposes unauthenticated probe endpoints. New Kubernetes deployments should use `/livez` for `livenessProbe` and `/readyz` for `readinessProbe`.

| Endpoint | Purpose |
|----------|---------|
| `/livez` | Shallow liveness probe. Returns `200 OK` when the server process can answer HTTP requests. It does not check dependencies. |
| `/readyz` | Readiness probe. Returns `200 OK` only after the pod is ready to receive traffic; currently this requires the docs index to be ready. Otherwise returns `503`. |
| `/healthz` | Legacy/generic health check kept for backward compatibility. It follows the same strict status as `/readyz`; use `/livez` for shallow liveness. |

## What Can You Do With It?

```
"Show me all available metrics"
"What's the p99 latency for http_request_duration_seconds?"
"List all active alerts"
"Show me error logs for the paymentservice from the last hour"
"How many errors per service in the last hour?"
"Search traces for the checkout service from the last hour"
"Get details for trace ID abc123"
"Create a dashboard with CPU and memory widgets"
"How do I send Docker logs to SigNoz?"
```

## Available Tools

> **SigNoz compatibility:** `signoz_check_metric_usage` targets `/api/v2/metrics/dashboards?metricName=...` and `/api/v2/metrics/alerts?metricName=...`, available in SigNoz v0.131.0 and newer. Alert-rule list/get/create/update/delete require SigNoz v0.120.0 or newer. `signoz_get_alert_history` requires v0.118.0 or newer. Self-hosted deployments on older SigNoz versions will see HTTP 404 from the affected tools. Notification-channel tools target the render-envelope `/api/v1/channels/*` routes introduced by SigNoz/signoz#10941, #10957, #10995, and #10997.

> **Tool metadata:** every tool accepts `searchContext`. Copy the user's entire original request verbatim, including preflight or confirmation context; it is used for MCP observability and is not forwarded to SigNoz APIs.

> **Input validation:** calls are never rejected for schema mismatches. Arguments are validated against each tool's advertised schema; a mismatched call still runs best-effort, and the successful result carries an appended `Input validation notice:` text block naming the mismatched parameter so self-correcting agents can adjust. Mismatches are also counted in the `mcp.tool.validation.mismatches` metric.

| Tool | Description |
|------|-------------|
| `signoz_list_metrics` | Discover active metric names and catalog metadata |
| `signoz_query_metrics` | Query known metrics for values, trends, breakdowns, or formulas |
| `signoz_get_top_metrics` | Return top 100 metrics ranked by ingested sample volume with pre-computed percentages for cost and volume analysis |
| `signoz_check_metric_usage` | Given a list of metric names (up to 50 per call), return which dashboards and alerts reference each one |
| `signoz_check_metric_cardinality` | Return label/attribute keys for a single metric with cardinality counts and sample values, sorted highest-cardinality first |
| `signoz_get_field_keys` | Discover available field keys for metrics, traces, or logs |
| `signoz_get_field_values` | Get possible values for a field key |
| `signoz_list_alerts` | List firing/silenced/inhibited Alertmanager alert *instances* (not rule definitions) |
| `signoz_list_alert_rules` | List configured alert-rule summaries, including inactive/OK and disabled rules |
| `signoz_get_alert` | Get one alert rule's full definition by `id` |
| `signoz_get_alert_history` | Get one rule's firing or state-transition history |
| `signoz_create_alert` | Create an alert after verifying notification-channel names |
| `signoz_update_alert` | Fully replace an alert after fetching it and verifying notification-channel names |
| `signoz_delete_alert` | Permanently delete a confirmed alert rule by UUIDv7 `id` |
| `signoz_list_dashboards` | List tenant-dashboard summaries and discover UUIDs |
| `signoz_get_dashboard` | Get one dashboard's full layout, variables, panels, and queries |
| `signoz_create_dashboard` | Create a custom multi-panel dashboard |
| `signoz_update_dashboard` | Fully replace a fetched dashboard while preserving unrequested fields |
| `signoz_patch_dashboard` | Apply a partial RFC 6902 JSON Patch without resending the whole dashboard |
| `signoz_delete_dashboard` | Permanently delete a confirmed dashboard by `id` |
| `signoz_import_dashboard` | Create a dashboard from a known curated template path |
| `signoz_list_dashboard_templates` | List curated templates and discover an import path |
| `signoz_list_services` | List APM services with trace activity in a time range |
| `signoz_get_service_top_operations` | Get ranked operations for one traced service |
| `signoz_list_views` | List saved Explorer views for traces/logs/metrics/Cost Meter and discover UUIDs |
| `signoz_get_view` | Get one saved Explorer view's complete definition by `id` |
| `signoz_search_docs` | Find ranked official-doc matches when no exact page is selected |
| `signoz_fetch_doc` | Fetch one known official-doc page or heading as Markdown |
| `signoz_create_view` | Save one reusable Explorer query |
| `signoz_update_view` | Fully replace a fetched saved view while preserving unrequested fields |
| `signoz_delete_view` | Permanently delete a confirmed saved view by `id` |
| `signoz_aggregate_logs` | Aggregate log statistics and grouped or top-N breakdowns |
| `signoz_search_logs` | Return individual log records matching filters |
| `signoz_aggregate_traces` | Aggregate span statistics and grouped or top-N breakdowns |
| `signoz_search_traces` | Return individual span rows or discover trace IDs |
| `signoz_get_trace_details` | Get one known trace with all spans and hierarchy |
| `signoz_execute_builder_query` | Query Builder v5 requests the dedicated tools cannot express |
| `signoz_list_notification_channels` | List channel summaries for name verification and ID discovery |
| `signoz_get_notification_channel` | Get all provider-specific settings for one channel by ID |
| `signoz_create_notification_channel` | Create a uniquely named channel and send a test notification |
| `signoz_update_notification_channel` | Fully replace a fetched channel and send a test notification |
| `signoz_delete_notification_channel` | Permanently delete a confirmed channel by ID |

For detailed usage and examples, see the [full documentation](https://signoz.io/docs/ai/signoz-mcp-server/).

> **Resource deep links:** the resource read tools (`signoz_list_dashboards`, `signoz_get_dashboard`, `signoz_list_alerts`, `signoz_list_alert_rules`, `signoz_get_alert`, `signoz_list_services`, `signoz_search_traces`, `signoz_get_trace_details`) and the dashboard write tools (`signoz_create_dashboard`, `signoz_update_dashboard`, `signoz_patch_dashboard`) include a `webUrl` field — an absolute deep link to the resource in the SigNoz web UI (per result row for `signoz_search_traces`) — when the request carries a SigNoz instance URL.

### Agent Routing Guidance

Use `signoz_search_docs` for topical discovery when no exact documentation page is selected, then `signoz_fetch_doc` for the chosen page or heading. Use live data tools for tenant telemetry, alert state, dashboards, saved views, and notification channels.

Docs tools use the same authentication path as other MCP tools.

### Available Resources

| Resource | Read when you need |
|---|---|
| `signoz://alert/instructions` | Alert schemas, fields, thresholds, evaluation, and notification workflow |
| `signoz://alert/examples` | Alert payload examples; replace example channels with verified names |
| `signoz://dashboard/instructions` | Dashboard fields, variables, chaining, and layout |
| `signoz://dashboard/widgets-instructions` | Panel choices and query-specific guides |
| `signoz://dashboard/widgets-examples` | Panel examples and validation patterns |
| `signoz://dashboard/query-builder-example` | Dashboard Query Builder aggregations, filters, legends, and functions |
| `signoz://promql/instructions` | PromQL widgets or alerts, especially dotted OTel metric names |
| `signoz://dashboard/clickhouse-schema-for-logs` | Bundled logs schema snapshot for dashboard SQL |
| `signoz://dashboard/clickhouse-logs-example` | Raw ClickHouse logs widget patterns |
| `signoz://dashboard/clickhouse-schema-for-metrics` | Bundled metrics schema snapshot for dashboard SQL |
| `signoz://dashboard/clickhouse-metrics-example` | Raw ClickHouse metrics widget patterns |
| `signoz://dashboard/clickhouse-schema-for-traces` | Bundled traces schema snapshot for dashboard SQL |
| `signoz://dashboard/clickhouse-traces-example` | Raw ClickHouse traces widget patterns |
| `signoz://logs/query-builder-guide` | Logs Query Builder v5 JSON or unfamiliar log fields |
| `signoz://traces/query-builder-guide` | Traces Query Builder v5 JSON or unfamiliar trace fields |
| `signoz://metrics-aggregation-guide` | Metric aggregations, formulas, grouping, limits, and Cost Meter queries |
| `signoz://view/instructions` | Saved Explorer view fields and read-before-replace workflow |
| `signoz://view/examples` | Saved-view payloads for traces, logs, metrics, and Cost Meter |
| `signoz://docs/sitemap` | Indexed official-doc catalog and page URLs |
| `signoz://alert/{id}/summary` | One live alert definition plus up to 10 history records from the preceding six hours |
| `signoz://dashboard/{id}/summary` | One full live dashboard definition; the URI remains backward-compatible |

<details>
<summary><strong>Parameter Reference</strong></summary>

#### `signoz_list_metrics`

Discover metric names and catalog metadata such as type, temporality, unit, and monotonicity. Use `signoz_query_metrics` for values or trends. This list has a limit but no offset pagination.

- **Parameters**:
  - `searchText` (optional) - Filter metrics by name substring (e.g., 'cpu', 'memory')
  - `limit` (optional) - Maximum number of metrics to return (default: 50)
  - `timeRange` (optional) - Relative range: 30m, 1h, 6h, 24h, 7d (default: 1h; ignored when both `start` and `end` are provided)
  - `start`/`end` (optional) - Unix ms timestamps. When both are provided, they override `timeRange`.
  - `source` (optional) - Data-source filter. Use `"meter"` to list Cost Meter metrics — the usage/billing metrics SigNoz meters on (currently telemetry ingestion volume); omit for the default metrics store
  - **Completeness note**: the response appends a note reporting `hasMore` (inferred from `returnedRows == limit`) so a `limit`-truncated list is never mistaken for the full set; narrow with `searchText` for more specificity

#### `signoz_query_metrics`

Query a known metric for values, trends, breakdowns, or formulas. The tool applies metric-aware defaults and auto-fetches omitted metadata; call it directly when `metricName` is known. Use `signoz_list_metrics` only to discover a name or inspect catalog metadata.

- **Parameters**:
  - `metricName` (required) - Metric name to query
  - `metricType` (optional) - gauge, sum, histogram, exponential_histogram (auto-fetched if absent)
  - `isMonotonic` (optional) - Boolean (or the strings `"true"`/`"false"`); auto-fetched if absent. An invalid value is rejected rather than silently treated as false
  - `temporality` (optional) - cumulative, delta, unspecified (auto-fetched if absent)
  - `timeAggregation` (optional) - Aggregation over time (auto-defaulted by type)
  - `spaceAggregation` (optional) - Aggregation across dimensions (auto-defaulted by type)
  - `groupBy` (optional) - Comma-separated field names or an array. Resource context is inferred for `k8s.*`, `container.*`, `host.*`, `cloud.*`, `deployment.*`, `process.*`, `service.*`, `telemetry.*`, and `os.*`; other names use attribute context
  - `filter` (optional) - Filter expression
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '7d'; default: '1h'; ignored when both `start` and `end` are provided)
  - `start`/`end` (optional) - Unix ms timestamps. When both are provided, they override `timeRange`.
  - `stepInterval` (optional) - Step in seconds (auto-calculated if omitted)
  - `requestType` (optional) - Response format. Enum: `time_series` (default), `scalar`. Unknown values are rejected.
  - `reduceTo` (optional) - For scalar: sum, count, avg, min, max, last, median
  - `formula` (optional) - Expression over named queries (e.g., "A / B * 100")
  - `formulaQueries` (optional) - Array or JSON-encoded array string of additional named metric queries for formula. Each object supports `name`, `metricName`, `metricType`, `isMonotonic`, `temporality`, `timeAggregation`, `spaceAggregation`, `groupBy`, and `filter`; `name` and `metricName` are required.
  - `source` (optional) - Data-source filter. Use `"meter"` to query Cost Meter data; omit for the default metrics store
  - **Result bounds**: standalone generated metric queries and formula results use `limit: 100` with `__result desc`. Every query feeding a formula uses `limit: 10000`, because component limits are applied before formula evaluation and independent top-100 inputs can discard a high-ratio group. The response decisions note reports both bounds. Narrow the filters/grouping when formula-input cardinality can exceed 10000.
  - **Key-not-found errors**: a filter referencing a key absent from this workspace's metrics metadata fails with recovery guidance in the error text plus a machine-readable `missingKeys` array in the structured error content

#### `signoz_get_top_metrics`

Return top 100 metrics ranked by ingested sample volume with pre-computed percentages. Use this to identify which metrics are driving the most ingestion volume and cost. Wraps `POST /api/v2/metrics/treemap`. Response fields: `metricName`, `percentage` (share of total sample volume), `totalValue` (absolute sample count).

- **Parameters**:
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '1h', '24h', '3d', '7d', '30d'; default: '7d'; ignored when both `start` and `end` are provided). Start with 7d; if the query times out, retry with 3d, then 24h
  - `start`/`end` (optional) - Unix ms timestamps. When both are provided, they override `timeRange`
  - **Completeness note**: returns a fixed top 100 by ingested sample volume; the response appends a note flagging whether the list was truncated at that cap (`hasMore`)

#### `signoz_check_metric_usage`

Given a list of metric names, return which dashboards and alerts reference each one. Wraps `/api/v2/metrics/dashboards?metricName=...` and `/api/v2/metrics/alerts?metricName=...` per metric. Requires SigNoz v0.131.0+.

- **Parameters**:
  - `metricNames` (required) - Array of metric name strings to check (max 50 per call). Example: `["system.disk.io", "k8s.node.condition"]`. For larger lists, split into batches of 50 and merge results.
- **Response**: Per metric — `dashboards` (list of dashboard names that reference the metric), `alerts` (list of alert names that reference the metric), `error` (non-empty when the lookup failed — do not treat the metric as unused in that case)
- **Limits**: Maximum 50 metrics per call; 30-second overall timeout (partial results returned on expiry)

#### `signoz_check_metric_cardinality`

Return label/attribute keys for one metric with cardinality counts and sample values, sorted highest first. Samples help distinguish unbounded values such as UUIDs from bounded dimensions such as status codes. This tool does not show whether the metric is used; call `signoz_check_metric_usage` before recommending a drop.

- **Parameters**:
  - `metricName` (required) - Metric name to inspect. Example: `k8s.container.memory_limit`
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '24h', '3d', '7d'; default: '7d'; ignored when both `start` and `end` are provided)
  - `start`/`end` (optional) - Unix ms timestamps. When both are provided, they override `timeRange`

#### `signoz_list_alerts`

Lists currently firing/silenced/inhibited alert *instances* from Alertmanager — **not** rule definitions. Use `signoz_list_alert_rules` for configured rules, `signoz_get_alert` with an `id` for one full rule definition, or `signoz_get_alert_history` for the state timeline.

- **Parameters**:
  - `limit` (optional) - Maximum number of alerts per page (default: 50)
  - `offset` (optional) - Number of results to skip for pagination (default: 0)
  - `active` / `silenced` / `inhibited` (optional) - Tri-state filters. Boolean (or the strings `"true"`/`"false"`). Omit to defer to the backend default (all states included). An invalid value is rejected rather than silently dropped
  - `filter` (optional) - Comma-separated alert-label comparisons using `=`, `!=`, `=~` (regex), or `!~` (negative regex), e.g. `alertname="HighCPU",severity="critical"`
  - `receiver` (optional) - Regex to filter alerts by receiver name

#### `signoz_list_alert_rules`

Lists configured alert-rule summaries from `GET /api/v2/rules`, including inactive/OK and disabled rules. Use `signoz_get_alert` for one full definition; use `signoz_list_alerts` for current Alertmanager instances.

- **Parameters**:
  - `limit` (optional) - Maximum number of rules to return per page (default: 50, max: 1000; higher values are clamped)
  - `offset` (optional) - Number of rules to skip for pagination (default: 0)

#### `signoz_get_alert`

Gets one alert rule's full definition (`GET /api/v2/rules/{id}`). Use `signoz_list_alert_rules` to discover IDs and call this before `signoz_update_alert` so unchanged fields can be preserved.

- **Parameters**: `id` (required) - Alert rule ID (UUIDv7 on v2-capable servers).
- **Note**: Response shape depends on the SigNoz server version. Post-#10997 servers return the canonical `Rule` type with `createdAt/updatedAt/createdBy/updatedBy`; older servers return `GettableRule` with `createAt/updateAt/createBy/updateBy` (no 'd').

#### `signoz_list_dashboards`

Lists paginated tenant-dashboard summaries (name, UUID, description, tags, timestamps). Use `signoz_get_dashboard` for panel and query definitions, and follow `pagination.nextOffset` while `pagination.hasMore` is true before concluding a dashboard is absent.

#### `signoz_get_dashboard`

Gets one known tenant dashboard's complete layout, variables, panels, and queries. Use `signoz_list_dashboards` to discover the UUID.

- **Parameters**: `id` (required) - Dashboard UUID

#### `signoz_create_dashboard`

Creates a custom multi-panel dashboard. Use `signoz_import_dashboard` when a curated template fits, or `signoz_create_view` to save one Explorer query. Read `signoz://dashboard/instructions`, `signoz://dashboard/widgets-instructions`, and `signoz://dashboard/widgets-examples` before composing the payload.

- **Parameters:**
  - `schemaVersion` (required) – Must be `"v6"`
  - `name` (DNS-1123 label) or `generateName: true` to derive it from `spec.display.name`
  - `tags` (required) – Array of key/value tags (may be empty)
  - `spec` (required) – Perses spec: `display`, `variables` (array), `panels` (map keyed by panel id), `layouts` (array)

#### `signoz_import_dashboard`

Creates a dashboard from a curated template hosted in the [SigNoz/dashboards](https://github.com/SigNoz/dashboards) repo (`main` branch). The server fetches the template JSON and creates the dashboard in one call.

When the relative template path is unknown, call `signoz_list_dashboard_templates` first. Pass its `path`, not a URL or absolute path.

- **Parameters:**
  - `path` (required) – Template path within the SigNoz/dashboards repo, e.g. `hostmetrics/hostmetrics.json`

#### `signoz_list_dashboard_templates`

Returns the full bundled catalog of curated SigNoz dashboard templates (id, title, path, description, category, keywords) as a JSON array. It does not list dashboards already created in the tenant; use `signoz_list_dashboards` for those.

- **Parameters:** none

#### `signoz_update_dashboard`

Fully replaces an existing dashboard. Fetch it with `signoz_get_dashboard`, merge only the requested changes, and preserve every other field. Use `signoz_update_view` for a saved Explorer query.

- **Parameters:**
  - `id` (required) – Dashboard id (the legacy `uuid` key is also accepted)
  - `schemaVersion`, `name`, `tags`, `spec` – the complete post-update state (see the tool's JSON Schema)

#### `signoz_patch_dashboard`

Applies an RFC 6902 JSON Patch to a dashboard — a partial update without re-sending the entire dashboard. Prefer this over `signoz_update_dashboard` for targeted edits (rename, add/edit one panel or query, tweak a variable).

- **Parameters:**
  - `id` (required) – Dashboard id (the legacy `uuid` key is also accepted)
  - `patch` (required) – Array of `{op, path, value}` operations; paths are JSON Pointers into the dashboard's postable shape, e.g. `/spec/display/name`, `/spec/panels/<panelId>`, `/tags/-`

#### `signoz_list_services`

Lists paginated APM services with trace activity in a time range. Absence means no trace activity in that window, not that the same `service.name` never appears in logs; use `signoz_get_field_values` with `signal="logs"` for log values.

- **Parameters**:
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '7d'; defaults to last 6 hours; ignored when both `start` and `end` are provided)
  - `start` (optional) - Start time in unix milliseconds (defaults to 6 hours ago).
  - `end` (optional) - End time in unix milliseconds (defaults to now)
  - `limit` (optional) - Maximum services per page (default: 50, max: 1000; higher values are clamped)
  - `offset` (optional) - Number of results to skip for pagination (default: 0)

#### `signoz_get_service_top_operations`

Gets the built-in operation table for one traced service, ranked by p99 latency with each operation's p50, p95, p99, call count, and error count. Use `signoz_aggregate_traces` for custom aggregation, grouping, time series, cross-service comparison, or arbitrary trace filters.

- **Parameters**:
  - `service` (required) - Exact traced service name, typically from `signoz_list_services`
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '7d'; defaults to last 6 hours; ignored when both `start` and `end` are provided)
  - `start` (optional) - Start time in unix milliseconds (defaults to 6 hours ago).
  - `end` (optional) - End time in unix milliseconds (defaults to now)
  - `tags` (optional) - JSON-encoded `TagQueryParam` array passed as a string, for example `[{"key":"http.method","tagType":"SpanAttribute","operator":"In","stringValues":["GET"]}]`; omit for no tag filter

#### `signoz_get_alert_history`

Gets one configured rule's firing or state-transition history. Defaults to the last 6 hours. Use `state` and `filter` to narrow results. For the next page, pass `data.nextCursor` as `cursor` and repeat the original filters, time range, and order.

The response is `{ "status": "success", "data": { "items": [...], "total": <n>, "nextCursor": "<opaque>" } }`; `nextCursor` is omitted on the final page.

- **Parameters**:
  - `id` (required) - Alert rule ID from `signoz_list_alert_rules`
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '7d'; defaults to last 6 hours; ignored when both `start` and `end` are provided)
  - `start` (optional) - Start timestamp in unix milliseconds (defaults to 6 hours ago).
  - `end` (optional) - End timestamp in unix milliseconds (defaults to now)
  - `state` (optional) - Filter by alert state. Enum: `inactive`, `pending`, `recovering`, `firing`, `nodata`, `disabled` (omit for all transitions)
  - `filter` (optional) - SigNoz query-builder expression over timeline labels. Combine conditions with `AND`, `OR`, and parentheses; quote strings with single quotes. Example: `severity = 'critical' AND (team = 'payments' OR service.name = 'checkout')`. To discover keys, first call without a filter and inspect `data.items[].labels[].key.name`. The backend-shaped `filterExpression` alias remains accepted for compatibility, but `filter` is canonical.
  - `cursor` (optional) - Opaque continuation cursor. Repeat the original time range, state, filter, and order when fetching the next page. Omit `cursor` for the first page.
  - `limit` (optional) - Rows per page. Default: 20; max: 10000 (higher values are clamped).
  - `order` (optional) - Sort order. Enum: `asc`, `desc` (default: 'asc')
  - **Legacy `offset`**: no longer supported; use the returned cursor instead.
  - **Completeness note**: the response appends a note reporting `hasMore` from `data.nextCursor` and names the cursor for the next page.

> **Requires SigNoz ≥ v0.118.0**, the first release to serve the v2 rule-history routes (`/api/v2/rules/{id}/history/*`, added in [SigNoz #10488](https://github.com/SigNoz/signoz/pull/10488)). If this tool returns `NOT_FOUND`, verify the rule `id` in the SigNoz UI or, on SigNoz v0.120.0+, with `signoz_list_alert_rules`; if the rule exists, upgrade SigNoz. Earlier deployments only expose the v1 `POST /api/v1/rules/{id}/history/timeline`.

#### `signoz_list_views`

List saved Explorer views or discover a view UUID for one Logs, Traces, Metrics, or Cost Meter page. A view stores one reusable Explorer query; it is not a multi-panel dashboard. Apply name/category filters before pagination and follow `pagination.nextOffset` while `pagination.hasMore` is true.

- **Parameters**:
  - `sourcePage` (required) - One of: `traces`, `logs`, `metrics`, `meter`. Cost Meter views are filed under `meter` (a distinct Explorer page), not `metrics`
  - `name` (optional) - Partial-match filter on view name (server-side)
  - `category` (optional) - Partial-match filter on view category (server-side)
  - `limit` (optional) - Page size (default: 50, max: 1000; higher values are clamped)
  - `offset` (optional) - Number of results to skip (default: 0)

#### `signoz_get_view`

Get one saved Explorer view's complete definition by UUID. Call this before `signoz_update_view`, which fully replaces the view.

- **Parameters**: `id` (required) - Saved view UUID

#### `signoz_search_docs`

Return ranked official-doc matches with URLs and snippets when no exact documentation page is selected. Do not use this for live tenant data; use `signoz_fetch_doc` after choosing a result.

- **Parameters**:
  - `searchText` (required) - Natural-language or keyword query to search official SigNoz docs
  - `limit` (optional) - Maximum results to return as a string (default: 10, max: 25; a numeric value is also accepted). The 25 ceiling is deliberate — each result hydrates document text out of the in-process docs index, so a larger limit inflates this server's resident memory.
  - `section_slug` (optional) - Exact top-level docs section filter, such as `setup`, `logs-management`, `apm-distributed-tracing`, `metrics`, `alerts`, `dashboards`, `signoz-apis`, `querying`, or `collection-agents`
  - `searchContext` - User's original question

#### `signoz_fetch_doc`

Fetch one known official SigNoz docs page's full Markdown or a requested heading. Use `signoz_search_docs` to discover a page first; accepted inputs are `https://signoz.io/docs/...` URLs or `/docs/...` paths.

- **Parameters**:
  - `url` (required) - Docs page URL or path
  - `heading` (optional) - Heading anchor ID or heading text
  - `searchContext` - User's original question

#### `signoz_create_view`

Save one reusable Explorer query. Use `signoz_create_dashboard` for a multi-panel dashboard. Cost Meter views use `sourcePage="meter"` with `signal="metrics"` and `source="meter"` in builder specs.

- **Parameters**: JSON payload matching the `SavedView` schema.
- **Required**: Read both MCP resources `signoz://view/instructions` and `signoz://view/examples` before composing any payload.

#### `signoz_update_view`

Fully replace an existing saved Explorer view. Fetch it with `signoz_get_view`, modify its returned `data` object, preserve every unrequested field, and pass that full object as `view`.

- **Parameters**:
  - `id` (required) - UUID of the view to replace
  - `view` (required) - Full `SavedView` object (`name`, `sourcePage`, `compositeQuery`, plus any of `category`, `tags`, `extraData`)
- **Resource rule**: Read `signoz://view/instructions` and `signoz://view/examples` when composing the body or changing `sourcePage`/`compositeQuery`. Skip them for name-, category-, or tags-only changes when a complete fetched body is already prepared. Call `signoz_get_view` first; partial bodies wipe unspecified fields.

#### `signoz_delete_view`

Permanently delete a confirmed saved Explorer view by UUID. Use `signoz_list_views` to discover the ID; use `signoz_delete_dashboard` for a dashboard.

- **Parameters**: `id` (required) - Saved view UUID



#### `signoz_aggregate_logs`

Return aggregate statistics over logs—counts, rates, averages, percentiles, or grouped/top-N breakdowns—not individual records. Use `signoz_search_logs` for log rows and message inspection.

- **Parameters**:
  - `aggregation` (required) - Aggregation function: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate
  - `aggregateOn` (optional) - Field to aggregate on (required for all except count and rate)
  - `groupBy` (optional) - Comma-separated fields to group by (e.g., 'service.name, severity_text')
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses. Unknown keys hard-error; ambiguous keys default to resource context. Log keys are workspace-specific — even `service.name` is only present when the log pipeline sets it. See `signoz://logs/query-builder-guide`
  - `service` (optional) - Shortcut filter for service name (adds `service.name = '<value>'`; fails with `key service.name not found` when the workspace's logs lack that attribute)
  - `severity` (optional) - Exact `severity_text`; DEBUG, INFO, WARN, ERROR, and FATAL are common examples, not an exhaustive enum. Discover values with `signoz_get_field_values(signal="logs", name="severity_text", fieldContext="log")`
  - `orderBy` (optional) - Order expression and direction (e.g., 'count() desc')
  - `limit` (optional) - Maximum number of groups to return (default: 100, max: 10000; higher values are clamped to bound server memory)
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '24h', '7d'; default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in unix milliseconds. When both are provided, they override `timeRange`.
  - `requestType` (optional) - `scalar` (default — one aggregate value over the whole range) or `time_series` (one value per time bucket). Unknown values are rejected.
  - `stepInterval` (optional) - Time bucket size in seconds for `time_series` mode. Accepts a number or numeric string (backend auto-selects when omitted)
  - **Time-series ranking note**: the limit selects top groups over the whole requested window, not independently per bucket. Narrow the window or adjust the limit when a short-lived series could otherwise be hidden.
  - **Key-not-found errors**: a filter referencing a key absent from this workspace's logs metadata fails with recovery guidance in the error text plus a machine-readable `missingKeys` array in the structured error content

#### `signoz_search_logs`

Return individual paginated log records matching text, service, severity, or field filters. Use `signoz_aggregate_logs` for counts, trends, or grouped breakdowns.

Calls using only `searchText`, `service`, `severity`, time, or pagination parameters need no guide read. Read `signoz://logs/query-builder-guide` only before composing `filter` with unfamiliar workspace fields.

- **Parameters**:
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses (e.g., "(severity_text = 'ERROR' OR body CONTAINS 'panic') AND service.name = 'payment-svc'"). Log keys are workspace-specific — even `service.name` is only present when the log pipeline sets it. Legacy `query` is still accepted for backward compatibility, but `filter` is canonical. See `signoz://logs/query-builder-guide`
  - `service` (optional) - Service name to filter by (adds `service.name = '<value>'`; fails with `key service.name not found` when the workspace's logs lack that attribute)
  - `severity` (optional) - Exact `severity_text`; DEBUG, INFO, WARN, ERROR, and FATAL are common examples, not an exhaustive enum. Discover values with `signoz_get_field_values(signal="logs", name="severity_text", fieldContext="log")`
  - `searchText` (optional) - Text to search for in log body (uses CONTAINS matching)
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '24h', '7d'; default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in unix milliseconds. When both are provided, they override `timeRange`.
  - `limit` (optional) - Maximum number of logs to return (default: 100, max: 10000; higher values are clamped — paginate with `offset`)
  - `offset` (optional) - Offset for pagination (default: 0)
  - **Ordering**: generated raw log queries use `timestamp desc`, then `id desc`, so offset pagination is deterministic when multiple rows share a timestamp.
  - **Completeness note**: the response appends a note reporting `hasMore` (inferred from `returnedRows == limit`) and the `nextOffset` to fetch, so a truncated page is never mistaken for the full result set
  - **Key-not-found errors**: a filter referencing a key absent from this workspace's logs metadata fails with recovery guidance in the error text plus a machine-readable `missingKeys` array in the structured error content

#### `signoz_get_field_keys`

Discover field names available for filtering or grouping metrics, traces, or logs. This returns keys, not observed values; use `signoz_get_field_values` after selecting a key.

- **Parameters**:
  - `signal` (required) - Signal type. Enum: `metrics`, `traces`, `logs`
  - `searchText` (optional) - Filter field keys by name substring
  - `metricName` (optional) - Filter by metric name (relevant for metrics signal)
  - `fieldContext` (optional) - Restrict to a field context: `resource`, `attribute` (alias `tag`), `scope`, `log`/`span`/`metric` (intrinsic/built-in columns), or `body` (JSON log body). Distinguishes intrinsic columns from user attributes.
  - `fieldDataType` (optional) - Restrict to a data type: `string`, `bool`, `int64`, `float64`, `number`, or array forms like `[]string`
  - `source` (optional) - For metrics, use `meter` for Cost Meter fields; omit for the default metrics store

#### `signoz_get_field_values`

Get observed values for a known field key. Use `signoz_get_field_keys` when the key is unknown, and match `signal` and `fieldContext` to the query that will use the value.

- **Parameters**:
  - `signal` (required) - Signal type. Enum: `metrics`, `traces`, `logs`
  - `name` (required) - Field key name to get values for (e.g., `service.name`, `http.method`)
  - `searchText` (optional) - Filter values by substring
  - `metricName` (optional) - Filter by metric name (relevant for metrics signal)
  - `fieldContext` (optional) - Restrict the lookup to a field context (`resource`, `attribute`/`tag`, `scope`, `log`/`span`/`metric`, `body`) when the same key name exists in more than one
  - `source` (optional) - For metrics, use `meter` for Cost Meter values; omit for the default metrics store


#### `signoz_search_traces`

Return individual paginated span rows matching service, operation, error, duration, or field filters, and use them to discover trace IDs. Use `signoz_aggregate_traces` for statistics or `signoz_get_trace_details` for one known trace.

- **Parameters**:
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses (e.g., "service.name = 'payment-svc' AND (has_error = true OR attribute.http.response.status_code >= 500)"). Legacy `query` is still accepted for backward compatibility, but `filter` is canonical. See `signoz://traces/query-builder-guide`
  - `service` (optional) - Service name to filter by
  - `operation` (optional) - Operation/span name to filter by
  - `error` (optional) - Filter by error status. Boolean (or the strings `"true"`/`"false"`). An invalid value is rejected rather than silently dropped
  - `minDuration` / `maxDuration` (optional) - Min/max span duration in nanoseconds (e.g., '500000000' for 500ms)
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '24h', '7d'; default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in unix milliseconds. When both are provided, they override `timeRange`.
  - `limit` (optional) - Maximum span rows to return (default: 100, max: 10000; higher values are clamped — paginate with `offset`)
  - `offset` (optional) - Number of span rows to skip (default: 0)
  - **Ordering**: generated raw trace queries use `timestamp desc`.
  - **Completeness note**: the response appends a note reporting `hasMore` (inferred from `returnedRows == limit`) and the `nextOffset` to fetch, so a truncated page is never mistaken for the full result set
  - **Output note**: raw result row keys follow canonical Query Builder field names (for example `trace_id`, `span_id`, `duration_nano`, `has_error`). Legacy caller-provided filters such as `hasError` still pass through to the backend alias layer, but new response parsers should read the canonical snake_case keys.
  - **Key-not-found errors**: a filter referencing a key absent from this workspace's traces metadata fails with recovery guidance in the error text plus a machine-readable `missingKeys` array in the structured error content

#### `signoz_aggregate_traces`

Return custom aggregate statistics over spans—counts, rates, latency percentiles, grouped/top-N breakdowns, or time series—not individual rows or a full trace hierarchy. For one traced service's built-in operation table ranked by p99, use `signoz_get_service_top_operations`. Read `signoz://traces/query-builder-guide` before calling this tool.

- **Parameters**:
  - `aggregation` (required) - Aggregation function: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate
  - `aggregateOn` (optional) - Field to aggregate on (e.g., 'duration_nano'). Required for all except count and rate
  - `groupBy` (optional) - Comma-separated fields to group by (e.g., 'service.name, name')
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses. Unknown keys hard-error; ambiguous keys default to resource context. See `signoz://traces/query-builder-guide`
  - `service` (optional) - Shortcut filter for service name
  - `operation` (optional) - Shortcut filter for span/operation name
  - `error` (optional) - Shortcut filter for error spans. Boolean (or the strings `"true"`/`"false"`). An invalid value is rejected rather than silently dropped
  - `orderBy` (optional) - Order expression and direction (e.g., 'avg(duration_nano) desc')
  - `limit` (optional) - Maximum number of groups to return (default: 100, max: 10000; higher values are clamped to bound server memory)
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '24h', '7d'; default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in unix milliseconds. When both are provided, they override `timeRange`.
  - `requestType` (optional) - `scalar` (default — one aggregate value over the whole range) or `time_series` (one value per time bucket). Unknown values are rejected.
  - `stepInterval` (optional) - Time bucket size in seconds for `time_series` mode. Accepts a number or numeric string (backend auto-selects when omitted)
  - **Time-series ranking note**: the limit selects top groups over the whole requested window, not independently per bucket. Narrow the window or adjust the limit when a short-lived series could otherwise be hidden.
  - **Key-not-found errors**: a filter referencing a key absent from this workspace's traces metadata fails with recovery guidance in the error text plus a machine-readable `missingKeys` array in the structured error content

#### `signoz_get_trace_details`

For a known trace ID, return its spans, metadata, and hierarchy. Use `signoz_search_traces` first when the ID is unknown, and choose a time window that contains the trace; the default last six hours can miss older traces.

- **Parameters**:
  - `traceId` (required) - Known trace ID, usually discovered with `signoz_search_traces`
  - `timeRange` (optional) - Relative time range `<number><unit>` where unit is `m`/`h`/`d` (e.g. '30m', '1h', '6h', '7d'; defaults to last 6 hours; ignored when both `start` and `end` are provided)
  - `start` (optional) - Start time in unix milliseconds (defaults to 6 hours ago).
  - `end` (optional) - End time in unix milliseconds (defaults to now)
  - `includeSpans` (optional) - Include detailed span information. Boolean (or the strings `"true"`/`"false"`), default: true



#### `signoz_create_alert`

Create a new alert rule in SigNoz via `POST /api/v2/rules`.

- **Parameters**: JSON payload matching the SigNoz alert rule schema.
- **Schema varies by `ruleType`**:
  - `threshold_rule` / `promql_rule` → **v2alpha1** (structured `condition.thresholds`, `evaluation`, `notificationSettings`).
  - `anomaly_rule` → **v1**, metrics only: top-level `evalWindow` and `frequency`; `condition.op`/`matchType`/`target`/`algorithm`/`seasonality`; anomaly function inside `compositeQuery.queries[].spec.functions`. Omit `thresholds`, `evaluation`, `schemaVersion`.
- **Notification channels**: Before creating, call `signoz_list_notification_channels` to verify every selected name or show valid choices. Never guess. At least one existing valid channel is required even with `notificationSettings.usePolicy=true`. If validation still rejects a channel name, show the current names and retry.
- **Tip**: Read MCP resources `signoz://alert/instructions` and `signoz://alert/examples` (examples based on SigNoz PR #11023, plus a Cost Meter cumulative-budget alert) before composing payloads. For `promql_rule`, also read `signoz://promql/instructions` — OTel dotted metric names require the Prometheus 3.x UTF-8 quoted-selector form.

#### `signoz_update_alert`

Update an existing alert rule via `PUT /api/v2/rules/{id}`. This fully replaces the rule: fetch it with `signoz_get_alert`, preserve unchanged fields, and verify every selected channel name with `signoz_list_notification_channels` before updating. If validation still rejects a channel name, show the current names and retry.

- **Parameters**:
  - `id` (required) - UUIDv7 of the rule to update (obtain from `signoz_list_alert_rules` / `signoz_get_alert`).
  - Plus all fields of the alert rule schema (same shape as `signoz_create_alert`).

#### `signoz_delete_alert`

Delete an alert rule via `DELETE /api/v2/rules/{id}`. Irreversible—discover the ID with `signoz_list_alert_rules` and confirm the exact rule first. When both steps are already complete, call the delete tool directly without repeating list/get preflight.

- **Parameters**:
  - `id` (required) - UUIDv7 of the rule to delete. The server rejects non-UUIDv7 values with `invalid_input`.

#### `signoz_delete_dashboard`

Permanently delete a confirmed tenant dashboard by ID. The deletion is irreversible; use `signoz_list_dashboards` to discover the UUID. Use `signoz_delete_view` for a saved Explorer view.

- **Parameters**: `id` (required) - Dashboard UUID to delete

#### `signoz_list_notification_channels`

List paginated notification-channel summaries (`id`, `name`, `type`, timestamps). Use this to verify alert channel names, avoid duplicate channel names, or discover an ID. It does not return provider-specific settings; use `signoz_get_notification_channel` for those.

- **Parameters**:
  - `limit` (optional) - Maximum number of channels to return per page (default: 50, max: 1000; higher values are clamped)
  - `offset` (optional) - Offset for pagination (default: 0)

#### `signoz_create_notification_channel`

Create a notification channel and send a test notification. First call `signoz_list_notification_channels` and confirm the requested name is unused.

- **Parameters**:
  - `type` (required) - Channel type: slack, webhook, pagerduty, email, opsgenie, msteams
  - `name` (required) - Unique channel name, verified against `signoz_list_notification_channels`
  - `send_resolved` (optional) - Send notifications when alerts resolve. Boolean (or the strings `"true"`/`"false"`), default: true
  - Type-specific fields (required by channel type), such as `slack_api_url`, `webhook_url`, `pagerduty_routing_key`, `email_to`, `opsgenie_api_key`, or `msteams_webhook_url`
- **Test-send behavior**: the channel is created first, then a test notification is sent. If the test fails, the tool still returns success (the channel WAS created) but appends a prominent warning note so the failure is not buried — verify the configuration and re-test.

#### `signoz_update_notification_channel`

Fully replace an existing notification channel and send a test notification. First find the ID with `signoz_list_notification_channels`, fetch the complete configuration with `signoz_get_notification_channel`, then preserve every field not requested for change.

- **Parameters**:
  - `id` (required) - Notification channel UUID
  - `type` (required) - Channel type
  - `name` (required) - Channel name
  - `send_resolved` (optional) - Complete replacement setting. Copy the fetched value unless changing it; omission resets to true
  - Full channel configuration fields for the selected channel type
- **Test-send behavior**: same as create — a failed verification test-send surfaces a prominent warning note instead of flipping the result to an error.

#### `signoz_get_notification_channel`

Get all provider-specific settings for one notification channel by ID (`GET /api/v1/channels/{id}`). Use `signoz_list_notification_channels` to discover IDs.

- **Parameters**:
  - `id` (required) - Notification channel UUID

#### `signoz_delete_notification_channel`

Delete a notification channel by ID (`DELETE /api/v1/channels/{id}`). Irreversible—resolve its ID and confirm the exact channel first. When both steps are already complete, call the delete tool directly without repeating list/get preflight. This tool does not check whether alert rules reference it; inspect configured rules first when dependency safety is required.

- **Parameters**:
  - `id` (required) - Notification channel UUID

#### `signoz_execute_builder_query`

Runs a SigNoz Query Builder v5 request that the dedicated tools cannot express, including multi-query requests, formulas, PromQL, and ClickHouse SQL. Prefer `signoz_search_logs` / `signoz_search_traces` for rows, `signoz_aggregate_logs` / `signoz_aggregate_traces` for grouped results, and `signoz_query_metrics` for ordinary metrics.

- **Parameters**: `query` (required) - Complete SigNoz Query Builder v5 JSON object
- **Query types**: the per-envelope `compositeQuery.queries[i].type` selects the spec shape:
  - `builder_query` — signal-specific spec (logs/traces/metrics) with filter, aggregations, groupBy, etc.
  - `builder_formula` — formula expression referencing other query names (e.g. `A / B * 100`).
  - `promql` — `{name, query, disabled, step?, legend?}`. PromQL for OTel metrics requires the Prometheus 3.x UTF-8 quoted-selector form `{"metric.name.with.dots"}`; read the `signoz://promql/instructions` resource for details.
  - `clickhouse_sql` — `{name, query, disabled, legend?}`.
- **Builder result bounds**: for predictable authored queries, explicitly supply a positive `spec.limit` and non-empty v5 `spec.order` (not dashboard/editor `orderBy`) on every `builder_query` and `builder_formula`. When omitted, null, or zero, standalone limits and formula-result limits default to `100`; a builder query referenced by a formula defaults to `10000` because base-query limits are applied before formula evaluation. Raw logs order by `timestamp desc, id desc`; raw traces by `timestamp desc`; metric scalar/time-series queries and formulas by `__result desc`; and log/trace scalar/time-series queries by the primary aggregation descending. Valid caller-supplied values are preserved. The response appends a decisions note when defaults are inserted.
- **Guide routing**: read `signoz://logs/query-builder-guide` for logs, `signoz://traces/query-builder-guide` for traces, `signoz://metrics-aggregation-guide` for metrics/formulas, and `signoz://promql/instructions` for PromQL.
- **Time-series ranking caveat**: top-N groups are ranked over the entire requested window. A short-lived spike can be omitted even when it dominates one bucket; narrow the window or adjust the limit when that matters.
- **Backend warnings**: non-fatal warnings the backend returns (e.g. ambiguous-key resolution) are surfaced as a note alongside the raw response and WARN-logged, matching the search/aggregate/query_metrics tools (previously the body was returned verbatim and warnings were dropped).
- **Key-not-found errors**: a filter referencing a key absent from the workspace's metadata for the queried signal fails with recovery guidance in the error text plus a machine-readable `missingKeys` array in the structured error content
- **Documentation**: See [SigNoz Query Builder v5 docs](https://signoz.io/docs/userguide/query-builder-v5/)

</details>

## Environment Variables

| Variable          | Description                                                                    | Required                            |
| ----------------- | ------------------------------------------------------------------------------ | ----------------------------------- |
| `SIGNOZ_URL`      | SigNoz instance URL                                                            | Yes (stdio); Optional (http with OAuth) |
| `SIGNOZ_API_KEY`  | SigNoz API key (get from Settings → API Keys in the SigNoz UI) | Yes (stdio); Optional (http with OAuth) |
| `LOG_LEVEL`       | Logging level: `info`(default), `debug`, `warn`, `error`                       | No                                  |
| `TRANSPORT_MODE`  | MCP transport mode: `stdio`(default) or `http`                                 | No                                  |
| `MCP_SERVER_HOST` | Host/interface for HTTP transport mode (default: empty, which listens on all interfaces). Set to `127.0.0.1` for loopback-only access. | No |
| `MCP_SERVER_PORT` | Port for HTTP transport mode (default: `8000`)                                 | No |
| `MCP_MAX_REQUEST_BYTES` | Max inbound MCP HTTP request body size in bytes (default: `4194304` / 4 MiB). Bounds memory from a single oversized request. | No |
| `CLIENT_CACHE_SIZE` | Maximum cached tenant clients in multi-tenant HTTP mode (default: `256`) | No |
| `CLIENT_CACHE_TTL_MINUTES` | Tenant-client cache lifetime in minutes (default: `30`) | No |
| `SIGNOZ_DOCS_REFRESH_INTERVAL` | Runtime docs sitemap refresh interval (Go duration, default: `6h`) | No |
| `SIGNOZ_DOCS_FULL_REFRESH_INTERVAL` | Runtime full docs refresh interval (Go duration, default: `24h`) | No |
| `OAUTH_ENABLED`   | Enable OAuth 2.1 authentication flow (`true`/`false`)                          | No (default: `false`)               |
| `OAUTH_TOKEN_SECRET` | Encryption key for OAuth tokens (min 32 bytes, e.g. `openssl rand -base64 32`) | Yes when `OAUTH_ENABLED=true`    |
| `OAUTH_ISSUER_URL` | Public URL of this MCP server (used in OAuth metadata discovery)              | Yes when `OAUTH_ENABLED=true`       |
| `OAUTH_ACCESS_TOKEN_TTL_MINUTES` | Access token lifetime in minutes (default: 60)                  | No                                  |
| `OAUTH_REFRESH_TOKEN_TTL_MINUTES` | Refresh token lifetime in minutes (default: 43200 / 30d)      | No                                  |
| `OAUTH_AUTH_CODE_TTL_SECONDS` | Authorization code lifetime in seconds (default: 600 / 10min)      | No                                  |
| `SIGNOZ_CUSTOM_HEADERS` | Extra HTTP headers added to every API request, useful when SigNoz is behind a reverse proxy requiring auth (e.g. `CF-Access-Client-Id:id.access,CF-Access-Client-Secret:secret`). Format: `Key1:Value1,Key2:Value2` | No |
| `SIGNOZ_INSTANCE_URL_ALLOWLIST` | Multi-tenant (http) only: comma-separated allowlist of SigNoz backend hosts the server will proxy to. Entries are exact hosts (`signoz.example.com`) or wildcards (`*.us.signoz.cloud`, which matches any subdomain ending in `.us.signoz.cloud`); a scheme/port/path accidentally included in an entry is tolerated and reduced to the bare host. When set, SigNoz instance URLs that do not match are refused at every ingress: the OAuth setup form and `X-SigNoz-URL` header return HTTP 403, the OAuth token endpoint (incl. existing refresh tokens) returns `invalid_grant`, and `/mcp` requests via an OAuth token return 403. All increment a `disallowed_signoz_url`-tagged failure metric for alerting (not logged per-request, to avoid noise from misconfigured/looping clients), and the rejection message points SigNoz Cloud users to their region's MCP URL (`mcp.<region>.signoz.cloud`) with a docs link. Empty/unset allows any host. The operator's own `SIGNOZ_URL` is exempt. | No |
| `ANALYTICS_ENABLED` | Enable product analytics (`true`/`false`; default: `false`) | No |
| `SEGMENT_KEY` | Segment write key used only when analytics is enabled | No |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP gRPC endpoint for the MCP server's own traces and metrics. Internal telemetry export is disabled when no OTLP endpoint/exporter is configured. For plaintext collectors, use an `http://` endpoint such as `http://localhost:4317`. | No |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Trace-specific OTLP gRPC endpoint; overrides `OTEL_EXPORTER_OTLP_ENDPOINT` for traces. | No |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Metrics-specific OTLP gRPC endpoint; overrides `OTEL_EXPORTER_OTLP_ENDPOINT` for metrics. | No |
| `OTEL_TRACES_EXPORTER` | Set to `none` to disable internal trace export even when an OTLP endpoint is configured. | No |
| `OTEL_METRICS_EXPORTER` | Set to `none` to disable internal metrics export and runtime metrics even when an OTLP endpoint is configured. | No |

The MCP server does not run an OTLP log exporter; logs are emitted as JSON to stderr. `OTEL_LOGS_EXPORTER` is therefore not used.

## Claude Desktop Extension

### Building the Bundle

Requires **Node.js**. See [Anthropic MCPB](https://github.com/anthropics/mcpb) for details.

```bash
make bundle
```

### Installing

1. Open **Claude Desktop → Settings → Developer → Edit Config → Add bundle.mcpb**
2. Select `./bundle/bundle.mcpb`
3. Enter your `SIGNOZ_URL`, `SIGNOZ_API_KEY`, and optionally `LOG_LEVEL`
4. Restart Claude Desktop

## Architecture

For a detailed overview of request flow, component interactions, and design decisions, see [docs/architecture.md](docs/architecture.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development workflow, required docs/manifest sync for MCP changes, and PR checklist.

**Made with ❤️ for the observability community**
