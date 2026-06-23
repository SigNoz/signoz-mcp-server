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
- SigNoz v0.120.0 or newer for alert rule tools that use the `/api/v2/rules` APIs
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

> **SigNoz compatibility:** alert-rule tools target `/api/v2/rules/*`, which is available in SigNoz v0.120.0 and newer. Self-hosted deployments on older SigNoz versions will see HTTP 404 from the affected alert-rule tools. Notification-channel tools target the render-envelope `/api/v1/channels/*` routes introduced by SigNoz/signoz#10941, #10957, #10995, and #10997.

> **Tool metadata:** every tool accepts `searchContext`, the user's original question/search text. It is used for MCP observability and is not forwarded to SigNoz APIs.

| Tool | Description |
|------|-------------|
| `signoz_list_metrics` | Search and list available metrics |
| `signoz_query_metrics` | Query metrics with smart aggregation defaults |
| `signoz_get_field_keys` | Discover available field keys for metrics, traces, or logs |
| `signoz_get_field_values` | Get possible values for a field key |
| `signoz_list_alerts` | List firing/silenced/inhibited Alertmanager alert *instances* (not rule definitions) |
| `signoz_list_alert_rules` | List configured alert rules, including inactive/OK and disabled rules |
| `signoz_get_alert` | Get an alert rule definition by ID via GET /api/v2/rules/{ruleId} |
| `signoz_get_alert_history` | Get alert state history timeline for a rule |
| `signoz_create_alert` | Create an alert rule via POST /api/v2/rules; v2alpha1 for threshold/promql, v1 for anomaly |
| `signoz_update_alert` | Update an alert rule by UUIDv7 via PUT /api/v2/rules/{ruleId} |
| `signoz_delete_alert` | Delete an alert rule by UUIDv7 via DELETE /api/v2/rules/{ruleId} |
| `signoz_list_dashboards` | List all dashboards with summaries |
| `signoz_get_dashboard` | Get full dashboard configuration |
| `signoz_create_dashboard` | Create a new dashboard |
| `signoz_update_dashboard` | Update an existing dashboard |
| `signoz_delete_dashboard` | Delete a dashboard by UUID |
| `signoz_import_dashboard` | Create a dashboard from a curated SigNoz/dashboards template by path |
| `signoz_list_dashboard_templates` | List the bundled curated SigNoz dashboard template catalog so the model can pick a template |
| `signoz_list_services` | List services within a time range |
| `signoz_get_service_top_operations` | Get top operations for a service |
| `signoz_list_views` | List saved Explorer views for a sourcePage (traces/logs/metrics/meter) |
| `signoz_get_view` | Get a saved view by UUID |
| `signoz_search_docs` | Search official SigNoz docs for product, setup, instrumentation, config, API, deployment, or troubleshooting questions |
| `signoz_fetch_doc` | Fetch full markdown for one official SigNoz docs page or heading |
| `signoz_create_view` | Create a new saved Explorer view |
| `signoz_update_view` | Replace an existing saved view (full-body PUT) |
| `signoz_delete_view` | Delete a saved view by UUID |
| `signoz_aggregate_logs` | Aggregate logs (count, avg, p99, etc.) with grouping |
| `signoz_search_logs` | Search logs with flexible filtering |
| `signoz_aggregate_traces` | Aggregate trace statistics with grouping |
| `signoz_search_traces` | Search traces with flexible filtering |
| `signoz_get_trace_details` | Get full trace with all spans |
| `signoz_execute_builder_query` | Execute a raw Query Builder v5 query |
| `signoz_list_notification_channels` | List notification channels |
| `signoz_get_notification_channel` | Get a single notification channel by ID |
| `signoz_create_notification_channel` | Create a notification channel and send a test notification |
| `signoz_update_notification_channel` | Update a notification channel and send a test notification |
| `signoz_delete_notification_channel` | Delete a notification channel by ID |

For detailed usage and examples, see the [full documentation](https://signoz.io/docs/ai/signoz-mcp-server/).

> **Resource deep links:** the resource read tools (`signoz_list_dashboards`, `signoz_get_dashboard`, `signoz_list_alerts`, `signoz_list_alert_rules`, `signoz_get_alert`, `signoz_list_services`, `signoz_search_traces`, `signoz_get_trace_details`) include a `webUrl` field — an absolute deep link to the resource in the SigNoz web UI (per result row for `signoz_search_traces`) — when the request carries a SigNoz instance URL.

### Agent Routing Guidance

Use `signoz_search_docs` for any SigNoz product question: how-to, feature usage, setup, configuration, API behavior, deployment, instrumentation, OpenTelemetry integration with SigNoz, and troubleshooting. Use live data tools for actual telemetry, alert state, dashboard contents, saved views, and tenant-specific resources. When a docs search result needs exact commands or a specific section, call `signoz_fetch_doc`.

Docs tools use the same authentication path as other MCP tools.

<details>
<summary><strong>Parameter Reference</strong></summary>

#### `signoz_list_metrics`

Search and list available metrics from SigNoz. Supports filtering by name substring, time range, and source.

- **Parameters**:
  - `searchText` (optional) - Filter metrics by name substring (e.g., 'cpu', 'memory')
  - `limit` (optional) - Maximum number of metrics to return (default: 50)
  - `start` (optional) - Start time in unix milliseconds
  - `end` (optional) - End time in unix milliseconds
  - `source` (optional) - Data-source filter. Use `"meter"` to list Cost Meter metrics — the usage/billing metrics SigNoz meters on (currently telemetry ingestion volume); omit for the default metrics store

#### `signoz_query_metrics`

Query metrics with smart aggregation defaults and validation. Automatically applies the right timeAggregation and spaceAggregation based on metric type (gauge, counter, histogram). Auto-fetches metric metadata if not provided.

- **Parameters**:
  - `metricName` (required) - Metric name to query
  - `metricType` (optional) - gauge, sum, histogram, exponential_histogram (auto-fetched if absent)
  - `isMonotonic` (optional) - true/false (auto-fetched if absent)
  - `temporality` (optional) - cumulative, delta, unspecified (auto-fetched if absent)
  - `timeAggregation` (optional) - Aggregation over time (auto-defaulted by type)
  - `spaceAggregation` (optional) - Aggregation across dimensions (auto-defaulted by type)
  - `groupBy` (optional) - Comma-separated field names
  - `filter` (optional) - Filter expression
  - `timeRange` (optional) - Relative range: 30m, 1h, 6h, 24h, 7d (default: 1h; ignored when both `start` and `end` are provided)
  - `start`/`end` (optional) - Unix ms timestamps. When both are provided, they override `timeRange`
  - `stepInterval` (optional) - Step in seconds (auto-calculated if omitted)
  - `requestType` (optional) - time_series (default) or scalar
  - `reduceTo` (optional) - For scalar: sum, count, avg, min, max, last, median
  - `formula` (optional) - Expression over named queries (e.g., "A / B * 100")
  - `formulaQueries` (optional) - JSON array of additional named metric queries for formula
  - `source` (optional) - Data-source filter. Use `"meter"` to query Cost Meter data; omit for the default metrics store

#### `signoz_list_alerts`

Lists currently firing/silenced/inhibited alert *instances* from Alertmanager — **not** rule definitions. Use `signoz_list_alert_rules` for configured rules, `signoz_get_alert` with a `ruleId` for one full rule definition, or `signoz_get_alert_history` for the state timeline.

#### `signoz_list_alert_rules`

Lists configured alert rules from `GET /api/v2/rules`, including inactive/OK and disabled rules. Returns compact summaries with `ruleId`, `alert`, `alertType`, `ruleType`, `state`, `disabled`, `severity`, `labels`, `createdAt`, and `updatedAt`.

- **Parameters**:
  - `limit` (optional) - Maximum number of rules to return (default: 50)
  - `offset` (optional) - Number of rules to skip for pagination (default: 0)

#### `signoz_get_alert`

Gets the rule definition for an alert (`GET /api/v2/rules/{ruleId}`).

- **Parameters**: `ruleId` (required) - Alert rule ID (UUIDv7 on v2-capable servers).
- **Note**: Response shape depends on the SigNoz server version. Post-#10997 servers return the canonical `Rule` type with `createdAt/updatedAt/createdBy/updatedBy`; older servers return `GettableRule` with `createAt/updateAt/createBy/updateBy` (no 'd').

#### `signoz_list_dashboards`

Lists all dashboards with summaries (name, UUID, description, tags).

#### `signoz_get_dashboard`

Gets complete dashboard configuration.

- **Parameters**: `uuid` (required) - Dashboard UUID

#### `signoz_create_dashboard`

Creates a dashboard.

- **Parameters:**
  - `title` (required) – Dashboard name
  - `description` (optional) – Short summary of what the dashboard shows
  - `tags` (optional) – List of tags
  - `layout` (required) – Widget positioning grid
  - `variables` (optional) – Map of variables available for use in queries
  - `widgets` (required) – List of widgets added to the dashboard

#### `signoz_import_dashboard`

Creates a dashboard from a curated template hosted in the [SigNoz/dashboards](https://github.com/SigNoz/dashboards) repo (`main` branch). The server fetches the template JSON, validates it, and creates the dashboard in one call.

To discover available paths, call `signoz_list_dashboard_templates` first and let the model pick the best match.

- **Parameters:**
  - `path` (required) – Template path within the SigNoz/dashboards repo, e.g. `hostmetrics/hostmetrics.json`

#### `signoz_list_dashboard_templates`

Returns the full bundled catalog of curated SigNoz dashboard templates (id, title, path, description, category, keywords) as a JSON array. Pair with `signoz_import_dashboard`: have the model read the catalog, choose the entry that best matches the user's intent, then import it by its `path`.

- **Parameters:** none

#### `signoz_update_dashboard`

Updates an existing dashboard.

- **Parameters:**
  - `uuid` (required) – Unique identifier of the dashboard to update
  - `dashboard` (required) – Complete dashboard object representing the post-update state
    - `title` (required) – Dashboard name
    - `description` (optional) – Short summary of what the dashboard shows
    - `tags` (optional) – List of tags applied to the dashboard
    - `layout` (required) – Full widget positioning grid
    - `variables` (optional) – Map of variables available for use in queries
    - `widgets` (required) – Complete set of widgets defining the updated dashboard

#### `signoz_list_services`

Lists all services within a time range.

- **Parameters**:
  - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d' (ignored when both `start` and `end` are provided)
  - `start` (optional) - Start time in nanoseconds (defaults to 6 hours ago)
  - `end` (optional) - End time in nanoseconds (defaults to now)

#### `signoz_get_service_top_operations`

Gets top operations for a specific service.

- **Parameters**:
  - `service` (required) - Service name
  - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d' (ignored when both `start` and `end` are provided)
  - `start` (optional) - Start time in nanoseconds (defaults to 6 hours ago)
  - `end` (optional) - End time in nanoseconds (defaults to now)
  - `tags` (optional) - JSON array of tags

#### `signoz_get_alert_history`

Gets alert history timeline for a specific rule.

- **Parameters**:
  - `ruleId` (required) - Alert rule ID
  - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d' (ignored when both `start` and `end` are provided)
  - `start` (optional) - Start timestamp in milliseconds (defaults to 6 hours ago)
  - `end` (optional) - End timestamp in milliseconds (defaults to now)
  - `offset` (optional) - Offset for pagination (default: 0)
  - `limit` (optional) - Limit number of results (default: 20)
  - `order` (optional) - Sort order: 'asc' or 'desc' (default: 'asc')

#### `signoz_list_views`

List SigNoz saved Explorer views for a given sourcePage. Supports pagination; response includes a `pagination` block with `total`, `hasMore`, and `nextOffset`.

- **Parameters**:
  - `sourcePage` (required) - One of: `traces`, `logs`, `metrics`, `meter`. Cost Meter views are filed under `meter` (a distinct Explorer page), not `metrics`
  - `name` (optional) - Partial-match filter on view name (server-side)
  - `category` (optional) - Partial-match filter on view category (server-side)
  - `limit` (optional) - Page size (default: 50)
  - `offset` (optional) - Number of results to skip (default: 0)

#### `signoz_get_view`

Get a single saved view by UUID.

- **Parameters**: `viewId` (required) - Saved view UUID

#### `signoz_search_docs`

Search official SigNoz documentation with BM25 over indexed markdown content.

- **Parameters**:
  - `query` (required) - Natural-language or keyword query
  - `limit` (optional) - Maximum results to return (default: 10, max: 25)
  - `section_slug` (optional) - Exact top-level docs section filter, such as `setup`, `logs-management`, `apm-distributed-tracing`, `metrics`, `alerts`, `dashboards`, `signoz-apis`, `querying`, or `collection-agents`
  - `searchContext` - User's original question

#### `signoz_fetch_doc`

Fetch full markdown for one official SigNoz docs page from the local index. Accepts only `https://signoz.io/docs/...` URLs or `/docs/...` paths.

- **Parameters**:
  - `url` (required) - Docs page URL or path
  - `heading` (optional) - Heading anchor ID or heading text
  - `searchContext` - User's original question

#### `signoz://docs/sitemap`

Read-only MCP resource containing the indexed docs sitemap used by the docs search and fetch tools.

#### `signoz://logs/query-builder-guide`

Read-only MCP resource with logs Query Builder v5 filter syntax, field contexts, body text search, body JSON-path search, timestamp format, and complete raw/aggregation/time-series examples.

#### `signoz://traces/query-builder-guide`

Read-only MCP resource with traces Query Builder v5 filter syntax, field contexts, built-in span columns, timestamp format, and complete raw/aggregation/time-series examples.

#### `signoz_create_view`

Create a new saved Explorer view.

- **Parameters**: JSON payload matching the `SavedView` schema.
- **Tip**: Read MCP resources `signoz://view/instructions` and `signoz://view/examples` before composing payloads.

#### `signoz_update_view`

Replace an existing saved view (full-body PUT).

- **Parameters**:
  - `viewId` (required) - UUID of the view to replace
  - `view` (required) - Full `SavedView` object (`name`, `sourcePage`, `compositeQuery`, plus any of `category`, `tags`, `extraData`)
- **Tip**: Read MCP resources `signoz://view/instructions` and `signoz://view/examples` before composing payloads. Call `signoz_get_view` first, pass its `data` object under `view` with whichever fields changed. Partial bodies wipe unspecified fields.

#### `signoz_delete_view`

Delete a saved view by UUID.

- **Parameters**: `viewId` (required) - Saved view UUID



#### `signoz_aggregate_logs`

Aggregate logs with count, average, sum, min, max, or percentiles, optionally grouped by fields.

- **Parameters**:
  - `aggregation` (required) - Aggregation function: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate
  - `aggregateOn` (optional) - Field to aggregate on (required for all except count and rate)
  - `groupBy` (optional) - Comma-separated fields to group by (e.g., 'service.name, severity_text')
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses. Unknown keys hard-error; ambiguous keys default to resource context. See `signoz://logs/query-builder-guide`
  - `service` (optional) - Shortcut filter for service name
  - `severity` (optional) - Shortcut filter for severity (DEBUG, INFO, WARN, ERROR, FATAL)
  - `orderBy` (optional) - Order expression and direction (e.g., 'count() desc')
  - `limit` (optional) - Maximum number of groups to return (default: 10, max: 10000; higher values are clamped to bound server memory)
  - `timeRange` (optional) - Time range like '30m', '1h', '6h', '24h' (default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in milliseconds. When both are provided, they override `timeRange`

#### `signoz_search_logs`

Search logs with flexible filtering across all services.

- **Parameters**:
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses (e.g., "(severity_text = 'ERROR' OR body CONTAINS 'panic') AND service.name = 'payment-svc'"). Legacy `query` is still accepted for backward compatibility, but `filter` is canonical. See `signoz://logs/query-builder-guide`
  - `service` (optional) - Service name to filter by
  - `severity` (optional) - Severity filter (DEBUG, INFO, WARN, ERROR, FATAL)
  - `searchText` (optional) - Text to search for in log body (uses CONTAINS matching)
  - `timeRange` (optional) - Time range like '30m', '1h', '6h', '24h' (default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in milliseconds. When both are provided, they override `timeRange`
  - `limit` (optional) - Maximum number of logs to return (default: 100, max: 10000; higher values are clamped — paginate with `offset`)
  - `offset` (optional) - Offset for pagination (default: 0)

#### `signoz_get_field_keys`

Get available field keys for a given signal (metrics, traces, or logs).

- **Parameters**:
  - `signal` (required) - Signal type: `metrics`, `traces`, or `logs`
  - `searchText` (optional) - Filter field keys by name substring
  - `metricName` (optional) - Filter by metric name (relevant for metrics signal)
  - `fieldContext` (optional) - Restrict to a field context: `resource`, `attribute` (alias `tag`), `scope`, `log`/`span`/`metric` (intrinsic/built-in columns), or `body` (JSON log body). Distinguishes intrinsic columns from user attributes.
  - `fieldDataType` (optional) - Restrict to a data type: `string`, `bool`, `int64`, `float64`, `number`, or array forms like `[]string`
  - `source` (optional) - Filter by source

#### `signoz_get_field_values`

Get possible values for a specific field key for a given signal.

- **Parameters**:
  - `signal` (required) - Signal type: `metrics`, `traces`, or `logs`
  - `name` (required) - Field key name to get values for (e.g., `service.name`, `http.method`)
  - `searchText` (optional) - Filter values by substring
  - `metricName` (optional) - Filter by metric name (relevant for metrics signal)
  - `fieldContext` (optional) - Restrict the lookup to a field context (`resource`, `attribute`/`tag`, `scope`, `log`/`span`/`metric`, `body`) when the same key name exists in more than one
  - `source` (optional) - Filter by source


#### `signoz_search_traces`

Search traces/spans with flexible filtering.

- **Parameters**:
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses (e.g., "service.name = 'payment-svc' AND (hasError = true OR responseStatusCode >= 500)"). Legacy `query` is still accepted for backward compatibility, but `filter` is canonical. See `signoz://traces/query-builder-guide`
  - `service` (optional) - Service name to filter by
  - `operation` (optional) - Operation/span name to filter by
  - `error` (optional) - Filter by error status ('true' or 'false')
  - `minDuration` / `maxDuration` (optional) - Min/max span duration in nanoseconds (e.g., '500000000' for 500ms)
  - `timeRange` (optional) - Time range like '30m', '1h', '6h', '24h' (default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in milliseconds. When both are provided, they override `timeRange`
  - `limit` (optional) - Maximum number of traces to return (default: 100, max: 10000; higher values are clamped — paginate with `offset`)
  - `offset` (optional) - Offset for pagination (default: 0)

#### `signoz_aggregate_traces`

Aggregate trace statistics like count, average, sum, min, max, or percentiles over spans, optionally grouped by fields.

- **Parameters**:
  - `aggregation` (required) - Aggregation function: count, count_distinct, avg, sum, min, max, p50, p75, p90, p95, p99, rate
  - `aggregateOn` (optional) - Field to aggregate on (e.g., 'durationNano'). Required for all except count and rate
  - `groupBy` (optional) - Comma-separated fields to group by (e.g., 'service.name, name')
  - `filter` (optional) - Filter expression using SigNoz search syntax. Combine conditions with AND, OR, and parentheses. Unknown keys hard-error; ambiguous keys default to resource context. See `signoz://traces/query-builder-guide`
  - `service` (optional) - Shortcut filter for service name
  - `operation` (optional) - Shortcut filter for span/operation name
  - `error` (optional) - Shortcut filter for error spans ('true' or 'false')
  - `orderBy` (optional) - Order expression and direction (e.g., 'avg(durationNano) desc')
  - `limit` (optional) - Maximum number of groups to return (default: 10, max: 10000; higher values are clamped to bound server memory)
  - `timeRange` (optional) - Time range like '30m', '1h', '6h', '24h' (default: '1h'; ignored when both `start` and `end` are provided)
  - `start` / `end` (optional) - Start/end time in milliseconds. When both are provided, they override `timeRange`

#### `signoz_get_trace_details`

Gets trace information including all spans and metadata.

- **Parameters**:
  - `traceId` (required) - Trace ID to get details for
  - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d' (ignored when both `start` and `end` are provided)
  - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
  - `end` (optional) - End time in milliseconds (defaults to now)
  - `includeSpans` (optional) - Include detailed span information (true/false, default: true)



#### `signoz_create_alert`

Create a new alert rule in SigNoz via `POST /api/v2/rules`.

- **Parameters**: JSON payload matching the SigNoz alert rule schema.
- **Schema varies by `ruleType`**:
  - `threshold_rule` / `promql_rule` → **v2alpha1** (structured `condition.thresholds`, `evaluation`, `notificationSettings`).
  - `anomaly_rule` → **v1** schema: top-level `evalWindow` and `frequency`; `condition.op`/`matchType`/`target`/`algorithm`/`seasonality`; anomaly function inside `compositeQuery.queries[].spec.functions`. Omit `thresholds`, `evaluation`, `schemaVersion`.
- **Tip**: Read MCP resources `signoz://alert/instructions` and `signoz://alert/examples` (the ten canonical SigNoz PR #11023 payloads plus a Cost Meter cumulative-budget example) before composing payloads. For `promql_rule`, also read `signoz://promql/instructions` — OTel dotted metric names require the Prometheus 3.x UTF-8 quoted-selector form.

#### `signoz_update_alert`

Update an existing alert rule via `PUT /api/v2/rules/{ruleId}`. Replaces the full rule configuration — fetch the current rule with `signoz_get_alert` first and merge changes on top of it.

- **Parameters**:
  - `ruleId` (required) - UUIDv7 of the rule to update (obtain from `signoz_list_alert_rules` / `signoz_get_alert`).
  - Plus all fields of the alert rule schema (same shape as `signoz_create_alert`).

#### `signoz_delete_alert`

Delete an alert rule via `DELETE /api/v2/rules/{ruleId}`. Irreversible — confirm with the user first.

- **Parameters**:
  - `ruleId` (required) - UUIDv7 of the rule to delete. The server rejects non-UUIDv7 values with `invalid_input`.

#### `signoz_delete_dashboard`

Delete a dashboard by UUID.

- **Parameters**: `uuid` (required) - Dashboard UUID to delete

#### `signoz_list_notification_channels`

List notification channels configured in SigNoz.

- **Parameters**:
  - `limit` (optional) - Maximum number of channels to return (default: 50)
  - `offset` (optional) - Offset for pagination (default: 0)

#### `signoz_create_notification_channel`

Create a notification channel and send a test notification.

- **Parameters**:
  - `type` (required) - Channel type: slack, webhook, pagerduty, email, opsgenie, msteams
  - `name` (required) - Channel name
  - Type-specific fields (required by channel type), such as `slack_api_url`, `webhook_url`, `pagerduty_routing_key`, `email_to`, `opsgenie_api_key`, or `msteams_webhook_url`

#### `signoz_update_notification_channel`

Update an existing notification channel and send a test notification.

- **Parameters**:
  - `id` (required) - Notification channel UUID
  - `type` (required) - Channel type
  - `name` (required) - Channel name
  - Full channel configuration fields for the selected channel type

#### `signoz_get_notification_channel`

Get a single notification channel by ID (`GET /api/v1/channels/{id}`).

- **Parameters**:
  - `id` (required) - Notification channel UUID

#### `signoz_delete_notification_channel`

Delete a notification channel by ID (`DELETE /api/v1/channels/{id}`). Irreversible — warn if alert rules still reference this channel.

- **Parameters**:
  - `id` (required) - Notification channel UUID

#### `signoz_execute_builder_query`

Executes a SigNoz Query Builder v5 query.

- **Parameters**: `query` (required) - Complete SigNoz Query Builder v5 JSON object
- **Query types**: the per-envelope `compositeQuery.queries[i].type` selects the spec shape:
  - `builder_query` — signal-specific spec (logs/traces/metrics) with filter, aggregations, groupBy, etc.
  - `builder_formula` — formula expression referencing other query names (e.g. `A / B * 100`).
  - `promql` — `{name, query, disabled, step?, legend?}`. PromQL for OTel metrics requires the Prometheus 3.x UTF-8 quoted-selector form `{"metric.name.with.dots"}`; read the `signoz://promql/instructions` resource for details.
  - `clickhouse_sql` — `{name, query, disabled, legend?}`.
- **Documentation**: See [SigNoz Query Builder v5 docs](https://signoz.io/docs/userguide/query-builder-v5/)

</details>

## Environment Variables

| Variable          | Description                                                                    | Required                            |
| ----------------- | ------------------------------------------------------------------------------ | ----------------------------------- |
| `SIGNOZ_URL`      | SigNoz instance URL                                                            | Yes (stdio); Optional (http with OAuth) |
| `SIGNOZ_API_KEY`  | SigNoz API key (get from Settings → API Keys in the SigNoz UI) | Yes (stdio); Optional (http with OAuth) |
| `LOG_LEVEL`       | Logging level: `info`(default), `debug`, `warn`, `error`                       | No                                  |
| `TRANSPORT_MODE`  | MCP transport mode: `stdio`(default) or `http`                                 | No                                  |
| `MCP_SERVER_PORT` | Port for HTTP transport mode                                                   | Yes only when `TRANSPORT_MODE=http` |
| `MCP_MAX_REQUEST_BYTES` | Max inbound MCP HTTP request body size in bytes (default: `4194304` / 4 MiB). Bounds memory from a single oversized request. | No |
| `SIGNOZ_DOCS_REFRESH_INTERVAL` | Runtime docs sitemap refresh interval (Go duration, default: `6h`) | No |
| `SIGNOZ_DOCS_FULL_REFRESH_INTERVAL` | Runtime full docs refresh interval (Go duration, default: `24h`) | No |
| `OAUTH_ENABLED`   | Enable OAuth 2.1 authentication flow (`true`/`false`)                          | No (default: `false`)               |
| `OAUTH_TOKEN_SECRET` | Encryption key for OAuth tokens (min 32 bytes, e.g. `openssl rand -base64 32`) | Yes when `OAUTH_ENABLED=true`    |
| `OAUTH_ISSUER_URL` | Public URL of this MCP server (used in OAuth metadata discovery)              | Yes when `OAUTH_ENABLED=true`       |
| `OAUTH_ACCESS_TOKEN_TTL_MINUTES` | Access token lifetime in minutes (default: 60)                  | No                                  |
| `OAUTH_REFRESH_TOKEN_TTL_MINUTES` | Refresh token lifetime in minutes (default: 1440 / 24h)       | No                                  |
| `OAUTH_AUTH_CODE_TTL_SECONDS` | Authorization code lifetime in seconds (default: 600 / 10min)      | No                                  |
| `SIGNOZ_CUSTOM_HEADERS` | Extra HTTP headers added to every API request, useful when SigNoz is behind a reverse proxy requiring auth (e.g. `CF-Access-Client-Id:id.access,CF-Access-Client-Secret:secret`). Format: `Key1:Value1,Key2:Value2` | No |
| `SIGNOZ_INSTANCE_URL_ALLOWLIST` | Multi-tenant (http) only: comma-separated allowlist of SigNoz backend hosts the server will proxy to. Entries are exact hosts (`signoz.example.com`) or wildcards (`*.us.signoz.cloud`, which matches any subdomain ending in `.us.signoz.cloud`); a scheme/port/path accidentally included in an entry is tolerated and reduced to the bare host. When set, SigNoz instance URLs that do not match are refused at every ingress: the OAuth setup form and `X-SigNoz-URL` header return HTTP 403, the OAuth token endpoint (incl. existing refresh tokens) returns `invalid_grant`, and `/mcp` requests via an OAuth token return 403. All increment a `disallowed_signoz_url`-tagged failure metric for alerting (not logged per-request, to avoid noise from misconfigured/looping clients), and the rejection message points SigNoz Cloud users to their region's MCP URL (`mcp.<region>.signoz.cloud`) with a docs link. Empty/unset allows any host. The operator's own `SIGNOZ_URL` is exempt. | No |
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
