# SigNoz MCP Server

[![Go Version](https://img.shields.io/badge/Go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![MCP Version](https://img.shields.io/badge/MCP-0.37.0-orange.svg)](https://modelcontextprotocol.io)

A Model Context Protocol (MCP) server that provides seamless access to SigNoz observability data through AI assistants and LLMs. This server enables natural language queries for metrics, traces, logs, alerts, dashboards, and service performance data.

## 🚀 Features

- **List Metric Keys**: Retrieve all available metric keys from SigNoz.
- **Search Metric by text**: Find specific metric containing given text.
- **List Alerts**: Get all active alerts with detailed status.
- **Get Alert Details**: Retrieve comprehensive information about specific alert rules.
- **Get Alert History**: Gives you timeline of an alert.
- **Logs**: Gets log related to services, alerts, etc.  
- **Traces**: Search, analyze, get hierarchy and relationship of traces.
- **List Dashboards**: Get dashboard summaries (name, UUID, description, tags).
- **Get Dashboard**: Retrieve complete dashboard configurations with panels and queries.
- **List Services**: Discover all services within specified time ranges.
- **Service Top Operations**: Analyze performance metrics for specific services.
- **Query Builder**: Generates query to get complex response.

## 🏗️ Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   MCP Client    │───▶│  MCP Server      │───▶│   SigNoz API    │
│  (AI Assistant) │    │  (Go)            │    │  (Observability)│
└─────────────────┘    └──────────────────┘    └─────────────────┘
                              │
                              ▼
                       ┌──────────────────┐
                       │   Tool Handlers  │
                       │  (HTTP Client)   │
                       └──────────────────┘
```

### Core Components

- **MCP Server**: Handles MCP protocol communication
- **Tool Handlers**: Register and manage available tools
- **SigNoz Client**: HTTP client for SigNoz API interactions
- **Configuration**: Environment-based configuration management
- **Logging**: Structured logging with Zap

## 🧰 Usage

Use this mcp-server with MCP-compatible clients like Claude Desktop and Cursor.

### Claude Desktop

1. Build or locate the binary path for `signoz-mcp-server` (for example: `.../signoz-mcp-server/bin/signoz-mcp-server`).
2. Goto Claude -> Settings -> Developer -> Local MCP Server click on `edit config`
3. Edit `claude_desktop_config.json` Add shown config with your signoz url, api key and path to signoz-mcp-server binary.

```json
{
  "mcpServers": {
    "signoz": {
      "command": "/absolute/path/to/signoz-mcp-server/bin/signoz-mcp-server",
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

**Optional:** To prefix all tool names, add `--tool-prefix <prefix>` to the `args` array or set `SIGNOZ_TOOL_PREFIX` environment variable:
```json
{
  "mcpServers": {
    "signoz": {
      "command": "/absolute/path/to/signoz-mcp-server/bin/signoz-mcp-server",
      "args": ["--tool-prefix", "signoz"],
      "env": {
        "SIGNOZ_URL": "https://your-signoz-instance.com",
        "SIGNOZ_API_KEY": "your-api-key-here",
        "LOG_LEVEL": "info"
      }
    }
  }
}
```

Or use environment variable:
```json
{
  "mcpServers": {
    "signoz": {
      "command": "/absolute/path/to/signoz-mcp-server/bin/signoz-mcp-server",
      "args": [],
      "env": {
        "SIGNOZ_URL": "https://your-signoz-instance.com",
        "SIGNOZ_API_KEY": "your-api-key-here",
        "LOG_LEVEL": "info",
        "SIGNOZ_TOOL_PREFIX": "signoz"
      }
    }
  }
}
```

4. Restart Claude Desktop. You should see the `signoz` server load in the developer console and its tools become available.

Notes:
- Replace the `command` path with your actual binary location.
- When a prefix is specified, tool names will be prefixed with `<prefix>_` (e.g., with prefix `signoz`, `list_services` becomes `signoz_list_services`). Tools that already start with the prefix will not be double-prefixed.

### Cursor

Option A — GUI:
- Open Cursor → Settings → Cursor Settings → Tool & Integrations → `+` New MCP Server

Option B — Project config file:
Create `.cursor/mcp.json` in your project root:

For Both options use same json struct
```json
{
  "mcpServers": {
    "signoz": {
      "command": "/absolute/path/to/signoz-mcp-server/bin/signoz-mcp-server",
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

**Optional:** To prefix all tool names, add `--tool-prefix <prefix>` to the `args` array or set `SIGNOZ_TOOL_PREFIX` environment variable:
```json
{
  "mcpServers": {
    "signoz": {
      "command": "/absolute/path/to/signoz-mcp-server/bin/signoz-mcp-server",
      "args": ["--tool-prefix", "signoz"],
      "env": {
        "SIGNOZ_URL": "https://your-signoz-instance.com",
        "SIGNOZ_API_KEY": "your-api-key-here",
        "LOG_LEVEL": "info"
      }
    }
  }
}
```

Or use environment variable:
```json
{
  "mcpServers": {
    "signoz": {
      "command": "/absolute/path/to/signoz-mcp-server/bin/signoz-mcp-server",
      "args": [],
      "env": {
        "SIGNOZ_URL": "https://your-signoz-instance.com",
        "SIGNOZ_API_KEY": "your-api-key-here",
        "LOG_LEVEL": "info",
        "SIGNOZ_TOOL_PREFIX": "signoz"
      }
    }
  }
}
```

Once added, restart Cursor to use the SigNoz tools.

### HTTP based self hosted mcp server

### Claude Desktop

1. Build and run signoz-mcp-server with envs
   - SIGNOZ_URL=signoz_url SIGNOZ_API_KEY=signoz_apikey TRANSPORT_MODE=http MCP_SERVER_PORT=8000 LOG_LEVEL=log_level ./signoz-mcp-server
   - or use docker-compose 
2. Goto Claude -> Settings -> Developer -> Local MCP Server click on `edit config`
3. Edit `claude_desktop_config.json` Add shown config with your signoz url, api key and path to signoz-mcp-server binary.

```json
{
  "mcpServers": {
    "signoz": {
      "url": "http://localhost:8000/mcp",
      "headers": {
        "Authorization": "Bearer your-api-key-here"
      }
    }
  }
}
```

**Note:** You can pass the SigNoz API key either as:
- An environment variable (`SIGNOZ_API_KEY`) when starting the server, or
- Via the `Authorization` header in the client configuration as shown above

4. Restart Claude Desktop. You should see the `signoz` server load in the developer console and its tools become available.

### Cursor

Build and run signoz-mcp-server with envs
    - SIGNOZ_URL=signoz_url SIGNOZ_API_KEY=signoz_apikey TRANSPORT_MODE=http MCP_SERVER_PORT=8000 LOG_LEVEL=log_level ./signoz-mcp-server
    - or use docker-compose

Option A — GUI:
- Open Cursor → Settings → Cursor Settings → Tool & Integrations → `+` New MCP Server

Option B — Project config file:
Create `.cursor/mcp.json` in your project root:

For Both options use same json struct
```json
{
  "mcpServers": {
    "signoz": {
      "url": "http://localhost:8000/mcp",
      "headers": {
        "Authorization": "Bearer signoz-api-key-here"
      }
    }
  }
}
```

**Note:** You can pass the SigNoz API key either as:
- An environment variable (`SIGNOZ_API_KEY`) when starting the server, or
- Via the `Authorization` header in the client configuration as shown above


**Note:** By default, the server logs at `info` level. If you need detailed debugging information, set `LOG_LEVEL=debug` in your environment. For production use, consider using `LOG_LEVEL=warn` to reduce log verbosity.

## 🛠️ Development Guide

### Prerequisites

- Go 1.25 or higher
- SigNoz instance with API access
- Valid SigNoz API key

### Project Structure

```
signoz-mcp-server/
├── cmd/server/           # Main application entry point
├── internal/
│   ├── client/          # SigNoz API client
│   ├── config/          # Configuration management
│   ├── handler/tools/   # MCP tool implementations
│   ├── logger/          # Logging utilities
│   └── mcp-server/      # MCP server core
├── go.mod               # Go module dependencies
├── Makefile             # Build automation
└── README.md            
```

### Building from Source

```bash
# Clone the repository
git clone https://github.com/SigNoz/signoz-mcp-server.git
cd signoz-mcp-server

# Build the binary
make build

# Or build directly with Go
go build -o bin/signoz-mcp-server ./cmd/server/
```

### Configuration

Set the following environment variables:


```bash

export SIGNOZ_URL="https://your-signoz-instance.com"
export SIGNOZ_API_KEY="your-api-key-here" 
export LOG_LEVEL="info"  # Optional: debug, info, error (default: info)
```
In SigNoz Cloud, SIGNOZ_URL is typically - https://ingest.<region>.signoz.cloud

You can access API Key by going to Settings -> Workspace Settings -> API Key in SigNoz UI

### Running the Server

```bash
# Run the built binary
./bin/signoz-mcp-server

# Run with custom prefix for all tool names (e.g., 'signoz' makes 'list_services' become 'signoz_list_services')
./bin/signoz-mcp-server --tool-prefix signoz

# Or use environment variable
SIGNOZ_TOOL_PREFIX=signoz ./bin/signoz-mcp-server
```

### Development Workflow

1. **Add New Tools**: Implement in `internal/handler/tools/`
2. **Extend Client**: Add methods to `internal/client/client.go`
3. **Register Tools**: Add to appropriate handler registration
4. **Test**: Use MCP client to verify functionality

## 📖 User Guide

### For AI Assistants & LLMs

The MCP server provides the following tools that can be used through natural language:

#### Metrics Exploration
```
"Show me all available metrics"
"Search for CPU related metrics"
```

#### Alert Monitoring
```
"List all active alerts"
"Get details for alert rule ID abc123"
"Show me the history for alert rule abc123 from the last 6 hours"
"Get logs related to alert abc456"
```

#### Dashboard Management
```
"List all dashboards"
"Show me the Host Metrics dashboard details"
```

#### Service Analysis
```
"List all services from the last 6 hours"
"What are the top operations for the paymentservice?"
```

#### Log Analysis
```
"List all saved log views"
"Show me error logs for the paymentservice from the last hour"
"Search paymentservice logs for 'connection timeout' errors"
"Get error logs with FATAL severity"
```

#### Trace Analysis
```
"Show me all available trace fields"
"Search traces for the apple service from the last hour"
"Get details for trace ID ball123"
"Check for  error patterns in traces from the randomservice"
"Show me the span hierarchy for trace xyz789"
"Find traces with errors in the last 2 hours"
"Give me flow of this trace"
```


### Tool Reference

#### `list_metric_keys`
Lists all available metric keys from SigNoz.

#### `search_metric_by_text`
Searches metrics by text (uses SigNoz aggregate_attributes autocomplete).
- **Parameters**: `searchText` (required) - Text to search for

#### `list_alerts`
Lists all active alerts from SigNoz.

#### `get_alert`
Gets details of a specific alert rule.
- **Parameters**: `ruleId` (required) - Alert rule ID

#### `list_dashboards`
Lists all dashboards with summaries (name, UUID, description, tags).
- **Returns**: Simplified dashboard information for better LLM processing

#### `get_dashboard`
Gets complete dashboard configuration.
- **Parameters**: `uuid` (required) - Dashboard UUID

#### `list_services`
Lists all services within a time range.
- **Parameters**:
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in nanoseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in nanoseconds (defaults to now)

#### `get_service_top_operations`
Gets top operations for a specific service.
- **Parameters**:
    - `service` (required) - Service name
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in nanoseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in nanoseconds (defaults to now)
    - `tags` (optional) - JSON array of tags

#### `get_alert_history`
Gets alert history timeline for a specific rule.
- **Parameters**:
    - `ruleId` (required) - Alert rule ID
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start timestamp in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End timestamp in milliseconds (defaults to now)
    - `offset` (optional) - Offset for pagination (default: 0)
    - `limit` (optional) - Limit number of results (default: 20)
    - `order` (optional) - Sort order: 'asc' or 'desc' (default: 'asc')

#### `list_log_views`
Lists all saved log views from SigNoz.
- **Returns**: Summary with name, ID, description, and query details

#### `get_log_view`
Gets full details of a specific log view by ID.
- **Parameters**: `viewId` (required) - Log view ID

#### `get_logs_for_alert`
Gets logs related to a specific alert automatically.
- **Parameters**:
    - `alertId` (required) - Alert rule ID
    - `timeRange` (optional) - Time range around alert (e.g., '1h', '30m', '2h') - default: '1h'
    - `limit` (optional) - Maximum number of logs to return (default: 100)

#### `get_error_logs`
Gets logs with ERROR or FATAL severity within a time range.
- **Parameters**:
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in milliseconds (defaults to now)
    - `service` (optional) - Service name to filter by
    - `limit` (optional) - Maximum number of logs to return (default: 100)

#### `search_logs_by_service`
Searches logs for a specific service within a time range.
- **Parameters**:
    - `service` (required) - Service name to search logs for
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in milliseconds (defaults to now)
    - `severity` (optional) - Log severity filter (DEBUG, INFO, WARN, ERROR, FATAL)
    - `searchText` (optional) - Text to search for in log body
    - `limit` (optional) - Maximum number of logs to return (default: 100)

#### `get_trace_field_values`
Gets available field values for trace.
- **Parameters**:
    - `fieldName` (required) - Field name to get values for (e.g., 'service.name', 'http.method')
    - `searchText` (optional) - Search text to filter values

#### `search_traces_by_service`
Searches traces for a specific service.
- **Parameters**:
    - `service` (required) - Service name to search traces for
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in milliseconds (defaults to now)
    - `operation` (optional) - Operation name to filter by
    - `error` (optional) - Filter by error status (true/false)
    - `minDuration` (optional) - Minimum duration in nanoseconds
    - `maxDuration` (optional) - Maximum duration in nanoseconds
    - `limit` (optional) - Maximum number of traces to return (default: 100)

#### `get_trace_details`
Gets trace information including all spans and metadata.
- **Parameters**:
    - `traceId` (required) - Trace ID to get details for
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in milliseconds (defaults to now)
    - `includeSpans` (optional) - Include detailed span information (true/false, default: true)

#### `get_trace_error_analysis`
Analyzes error patterns in traces.
- **Parameters**:
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in milliseconds (defaults to now)
    - `service` (optional) - Service name to filter by
- **Returns**: Traces with errors, useful for identifying patterns and affected services

#### `get_trace_span_hierarchy`
Gets trace span relationships and hierarchy.
- **Parameters**:
    - `traceId` (required) - Trace ID to get span hierarchy for
    - `timeRange` (optional) - Time range like '2h', '6h', '2d', '7d'
    - `start` (optional) - Start time in milliseconds (defaults to 6 hours ago)
    - `end` (optional) - End time in milliseconds (defaults to now)

#### `signoz_execute_builder_query`
Executes a SigNoz Query Builder v5 query.
- **Parameters**: `query` (required) - Complete SigNoz Query Builder v5 JSON object
- **Documentation**: See [SigNoz Query Builder v5 docs](https://signoz.io/docs/userguide/query-builder-v5/)

#### `signoz_query_helper`
Helper tool for building SigNoz queries.
- **Parameters**:
    - `signal` (optional) - Signal type: traces, logs, or metrics
    - `query_type` (optional) - Type of help: fields, structure, examples, or all
- **Returns**: Guidance on available fields, signal types, and query structure

### Time Format

Most tools support flexible time parameters:

#### Recommended:  Time Ranges
Use the `timeRange` parameter with formats:
- `'30m'` - Last 30 minutes
- `'2h'` - Last 2 hours
- `'6h'` - Last 6 hours
- `'2d'` - Last 2 days
- `'7d'` - Last 7 days

The `timeRange` parameter automatically calculates the time window from now backwards. If not specified, most tools default to the last 6 hours. You can also specify time in milliseconds and nanoseconds
### Response Format

All tools return JSON responses that are optimized for LLM consumption:
- **List operations**: Return summaries to avoid overwhelming responses
- **Detail operations**: Return complete data when specific information is requested
- **Error handling**: Structured error messages for debugging

## 🔧 Configuration & Deployment

### Command-Line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--tool-prefix <prefix>` | Prefix to add to all tool names. The prefix will be added with an underscore (e.g., `--tool-prefix signoz` makes `list_services` become `signoz_list_services`). Tools that already start with the prefix will not be double-prefixed. | `""` (empty) |

### Environment Variables for Tool Prefix

| Variable | Description | Default |
|----------|-------------|---------|
| `SIGNOZ_TOOL_PREFIX` | Prefix to add to all tool names (alternative to `--tool-prefix` flag). If both are provided, the flag takes precedence. | `""` (empty) |

### Environment Variables

| Variable | Description                                                                   | Required |
|----------|-------------------------------------------------------------------------------|----------|
| `SIGNOZ_URL` | SigNoz instance URL  | Yes      |
| `SIGNOZ_API_KEY` | SigNoz API key (get from Settings → Workspace Settings → API Key in SigNoz UI) | Yes      |
| `LOG_LEVEL` | Logging level: `info`(default), `debug`, `warn`, `error`                      | No       |
| `TRANSPORT_MODE` | MCP transport mode: `stdio`(default) or `http`                                | No       |
| `MCP_SERVER_PORT` | Port for HTTP transport mode              | Yes only when `TRANSPORT_MODE=http` |


## 🤝 Contributing

We welcome contributions!

### Development Setup

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

### Code Style

- Follow Go best practices
- Use meaningful variable names
- Add comments for complex logic
- Ensure proper error handling

**Made with ❤️ for the observability community**
