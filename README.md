# SigNoz MCP Server

[![Go Version](https://img.shields.io/badge/Go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![MCP Version](https://img.shields.io/badge/MCP-0.37.0-orange.svg)](https://modelcontextprotocol.io)

A Model Context Protocol (MCP) server that provides seamless access to SigNoz observability data through AI assistants and LLMs. This server enables natural language queries for metrics, alerts, dashboards, and service performance data.

## 🚀 Features

- **List Metric Keys**: Retrieve all available metric keys from SigNoz
- **Search Metric Keys**: Find specific metrics
- **List Alerts**: Get all active alerts with detailed status
- **Get Alert Details**: Retrieve comprehensive information about specific alert rules
- **Logs**: Gets log related to services, alerts, etc.  
- **Get Alert Details**: Retrieve comprehensive information about specific alert rules
- **List Dashboards**: Get dashboard summaries (name, UUID, description, tags)
- **Get Dashboard**: Retrieve complete dashboard configurations with panels and queries
- **List Services**: Discover all services within specified time ranges
- **Service Top Operations**: Analyze performance metrics for specific services
- **Query Builder**: Generates query to get complex response
## 🏗️ Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   MCP Client   │───▶│  MCP Server      │───▶│   SigNoz API    │
│  (AI Assistant)│    │  (Go)            │    │  (Observability)│
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

4. Restart Claude Desktop. You should see the `signoz` server load in the developer console and its tools become available.

Notes:
- Replace the `command` path with your actual binary location.

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
"Search for CPU-related metrics"
```

#### Alert Monitoring
```
"List all active alerts"
"Get details for alert rule ID abc123"
```

#### Dashboard Management
```
"List all dashboards"
"Show me the Host Metrics dashboard details"
```

#### Service Analysis
```
"List all services from the last 24 hours"
"What are the top operations for the paymentservice?"
```

### Tool Reference

#### `list_metric_keys`
Lists all available metric keys from SigNoz.

#### `search_metric_keys`
Searches for metrics by text query.
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
    - `start` (required) - Start time in nanoseconds
    - `end` (required) - End time in nanoseconds

#### `get_service_top_operations`
Gets top operations for a specific service.
- **Parameters**:
    - `start` (required) - Start time in nanoseconds
    - `end` (required) - End time in nanoseconds
    - `service` (required) - Service name
    - `tags` (optional) - JSON array of tags

### Time Format

All time parameters use **nanoseconds since Unix epoch**. For example:
- Current time: `1751328000000000000` (August 2025)
- 24 hours ago: `1751241600000000000`

### Response Format

All tools return JSON responses that are optimized for LLM consumption:
- **List operations**: Return summaries to avoid overwhelming responses
- **Detail operations**: Return complete data when specific information is requested
- **Error handling**: Structured error messages for debugging

## 🔧 Configuration & Deployment

### Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `SIGNOZ_URL` | SigNoz instance URL | Yes |
| `SIGNOZ_API_KEY` | SigNoz API key | Yes |

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
