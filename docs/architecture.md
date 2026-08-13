# SigNoz MCP Server — Architecture

## System Overview

```mermaid
flowchart TB

subgraph Startup["Server Initialization"]
    ENV["Env Vars: SIGNOZ_URL, SIGNOZ_API_KEY,<br/>LOG_LEVEL, TRANSPORT_MODE, MCP_SERVER_PORT,<br/>CLIENT_CACHE_SIZE, CLIENT_CACHE_TTL_MINUTES,<br/>OAUTH_ENABLED, OAUTH_TOKEN_SECRET, OAUTH_ISSUER_URL,<br/>OTEL_EXPORTER_OTLP_*"]
    ENV --> CFG["config.LoadConfig"]
    CFG --> VALIDATE["config.ValidateConfig"]
    VALIDATE --> LOG["log.New"]
    LOG --> OTEL["Init OpenTelemetry<br/>(Tracer, Meter; OTLP export only when configured)"]
    OTEL --> HANDLER["Handler with LRU clientCache"]
    HANDLER --> CHSCHEMA["dashboard.InitClickhouseSchema"]
    CHSCHEMA --> MCPSRV["NewMCPServer<br/>official MCP Go SDK v1.7.0"]
    MCPSRV --> REGISTER["Register all tool handlers<br/>(Org Overview, Metrics, TopMetrics, MetricUsage, Alerts, Dashboards, Services,<br/>QueryBuilderV5, Logs, Docs, Traces)"]
    REGISTER --> MODE{"TransportMode?"}
end

subgraph StdioPath["Stdio Transport — Single Tenant"]
    MODE -->|stdio| STDIO["runStdio"]
    STDIO --> CTXFUNC["Seed API key, auth header,<br/>SigNoz URL, and client source"]
    CTXFUNC --> SETCTX_S["official Server.Run<br/>with StdioTransport"]
    SETCTX_S --> TOOL_S["Tool Handler Called"]
end

subgraph HTTPPath["HTTP Transport — Multi Tenant"]
    MODE -->|http| HTTP["HTTP Server<br/>/mcp + /healthz + /livez + /readyz + /oauth/* + /.well-known/*"]
    HTTP --> OTELWRAP["otelhttp.NewHandler<br/>(wraps entire mux)"]
    OTELWRAP --> REQ["Incoming HTTP Request"]
    REQ --> HEALTHCHECK{"Path?"}

    HEALTHCHECK -->|/livez| LIVE["200 OK — liveness only, no dependency checks"]
    HEALTHCHECK -->|/healthz| HC200["Legacy health check<br/>same strict status as /readyz"]
    HEALTHCHECK -->|/readyz| READY["200 when docs index is ready<br/>503 while docs index is warming"]
    HEALTHCHECK -->|/.well-known/*<br/>/oauth/*| OAUTHFLOW["OAuth 2.1 Endpoints<br/>(no auth required)"]
    HEALTHCHECK -->|/mcp| AUTH["authMiddleware"]

    AUTH --> APIKEY{"Authorization header?"}
    APIKEY -->|Yes + OAuth enabled| TRYDECRYPT["Try decrypt as OAuth token"]
    TRYDECRYPT -->|Success| OAUTHCTX["Extract apiKey + signozURL<br/>from encrypted token"]
    TRYDECRYPT -->|Expired| EXPIRED["401 + WWW-Authenticate challenge"]
    TRYDECRYPT -->|Not OAuth format| RAWKEY["Forward token upstream<br/>as Authorization: Bearer"]
    APIKEY -->|Yes + OAuth disabled| PARSE["Forward token upstream<br/>as Authorization: Bearer"]
    APIKEY -->|No + OAuth enabled| CHALLENGE["401 + WWW-Authenticate<br/>resource_metadata URL"]
    APIKEY -->|No + env set| ENVKEY["Use config.APIKey"]
    APIKEY -->|No + no env| DENY["401 Unauthorized"]

    AUTH --> URLCHECK{"SigNoz URL source?"}
    URLCHECK -->|OAuth token| FROMTOKEN["Already extracted<br/>from decrypted token"]
    URLCHECK -->|X-SigNoz-URL header| NORMALIZE["normalizeSigNozURL"]
    URLCHECK -->|env set| ENVURL["Use config.URL"]
    URLCHECK -->|none| NOURL["400 Bad Request"]

    subgraph URLValidation["URL Validation (normalizeSigNozURL)"]
        NORMALIZE --> SCHEME["Validate scheme (http/https only)"]
        SCHEME --> PATHCHECK["Reject path/query/fragment"]
        PATHCHECK --> LOCALHOST["Block localhost, 0.0.0.0, [::]"]
        LOCALHOST --> STRIPPORT["Strip default ports (80/443)"]
        STRIPPORT --> ORIGIN["Return canonical origin"]
    end

    ORIGIN --> SETCTX_H["Set apiKey and signozURL into ctx"]
    PARSE --> SETCTX_H
    OAUTHCTX --> SETCTX_H
    RAWKEY --> SETCTX_H
    ENVKEY --> SETCTX_H
    ENVURL --> SETCTX_H

    SETCTX_H --> TOOL_H["Tool Handler Called"]
end

subgraph OAuthFlow["OAuth 2.1 Flow (Stateless)"]
    direction TB
    DISC["Client: GET /.well-known/oauth-protected-resource<br/>GET /.well-known/oauth-authorization-server"]
    DISC --> REGCLIENT["POST /oauth/register<br/>{client_name, redirect_uris}"]
    REGCLIENT --> ENCID["client_id = encrypt(redirect_uris, name)"]
    ENCID --> AUTHPAGE["GET /oauth/authorize<br/>Browser opens, user sees form"]
    AUTHPAGE --> SUBMIT["POST /oauth/authorize<br/>User submits SigNoz URL + API key"]
    SUBMIT --> VALIDATE["Normalize SigNoz URL and validate<br/>credentials against SigNoz API"]
    VALIDATE -->|success| ENCCODE["auth_code = encrypt(api_key, signoz_url,<br/>client_id, redirect_uri, code_challenge)"]
    VALIDATE -->|invalid URL / rejected creds / unreachable instance| AUTHPAGEERR["Re-render authorize page<br/>with inline error"]
    ENCCODE --> REDIRECT["302 Redirect to client<br/>with ?code=encrypted"]
    REDIRECT --> EXCHANGE["POST /oauth/token<br/>{code, code_verifier}"]
    EXCHANGE --> VERIFY["Decrypt code, verify PKCE,<br/>verify client_id + redirect_uri match"]
    VERIFY --> ISSUE["access_token = encrypt(api_key, signoz_url)<br/>refresh_token = encrypt(api_key, signoz_url)"]
    ISSUE --> USE["Client uses access_token on /mcp<br/>Middleware decrypts → apiKey + signozURL"]

    REFRESH["POST /oauth/token<br/>{grant_type: refresh_token}"]
    REFRESH --> DECREF["Decrypt refresh_token<br/>→ api_key, signoz_url"]
    DECREF --> ISSUE
end

subgraph GetClient["GetClient — Unified for Both Transports"]
    TOOL["Tool Handler"]
    TOOL --> TLOG["tenantLogger(ctx)"]
    TLOG --> READ["Read apiKey and signozURL from ctx"]
    READ --> MISSING{"Both present?"}

    MISSING -->|No| ERR["Return error"]
    MISSING -->|Yes| HASH["cacheKey = SHA256(apiKey + delimiter + signozURL)"]

    HASH --> LOOKUP{"clientCache LRU hit?"}
    LOOKUP -->|Yes| HIT["Return cached client"]
    LOOKUP -->|No| CREATE["Create new SigNoz client and cache it"]

    CREATE --> HIT
end

subgraph Cache["Bounded Cache (expirable LRU)"]
    LRU_C["clientCache<br/>maxSize: 256, TTL: 30min"]
end

subgraph APICall["SigNoz API Call"]
    CLIENT["SigNoz Client<br/>(otelhttp instrumented)"]
    CLIENT --> APIREQ["HTTP Request with SIGNOZ-API-KEY header"]
    APIREQ --> S1["SigNoz Instance 1"]
    APIREQ --> S2["SigNoz Instance 2"]
    APIREQ --> SN["SigNoz Instance N"]
end

TOOL_S --> TOOL
TOOL_H --> TOOL
HIT --> CLIENT

LOOKUP -.->|read/write| LRU_C
```

## MCP Runtime and Transports

The runtime is `github.com/modelcontextprotocol/go-sdk` v1.7.0. The same
production catalog supports both lifecycle models on HTTP and stdio:

| Protocol era | Lifecycle and request identity |
|---|---|
| `2025-11-25` | The client performs `initialize` and `notifications/initialized`; initialize telemetry records negotiated client and protocol information. |
| `2026-07-28` | The client may call `server/discover` and then send requests directly. Protocol version, client identity, and client capabilities come from each request's `_meta`, so they are never shared between callers. |

### Streamable HTTP

The `/mcp` handler uses the official SDK with `Stateless: true` and
`JSONResponse: true`. Requests are authenticated and resolve the SigNoz URL on
every POST. The handler does not issue or require `Mcp-Session-Id`; inbound
session headers have no effect, so any instance can serve any request without sticky routing. `GET /mcp` and
`DELETE /mcp` return `405 Method Not Allowed`; the former GET listener and its
heartbeat were intentionally removed because this server sends no
server-initiated messages.

Successful MCP calls remain HTTP 200 `application/json`. The outer HTTP server
provides OpenTelemetry request spans. MCP requests pass through `otelhttp`, the
server mux, cross-origin protection, the request-size limit, authentication,
and finally the official SDK handler; SDK receiving middleware records the method/tool lifecycle after
protocol validation. Standard-library cross-origin protection rejects browser
POSTs whose Origin does not match the MCP endpoint before authentication or
dispatch, while non-browser and same-origin clients continue normally. Modern body/header metadata is validated by the official
transport before dispatch. Request cancellation is propagated for modern HTTP
calls; legacy clients should use MCP cancellation notifications because
disconnect-only cancellation is not guaranteed by the official transport.

The migration intentionally accepts the official SDK's protocol-owned behavior:
no advertised logging capability, no discovery-order guarantee, standard
invalid-params responses for unknown tools/resources/prompts, cache metadata on
cacheable responses in both eras, and modern result/server metadata. Tool descriptions, schemas, annotations, resource content,
prompt content, structured tool results, and coded tool errors remain unchanged.

### Stdio

Stdio uses the official newline-delimited JSON transport and the same dual-era
catalog. API credentials, SigNoz URL, and default client source are seeded into
the process context. SIGTERM/context cancellation is normalized as graceful
shutdown. An invalid JSON frame terminates that one-client process under the
official SDK; the server does not carry a custom framing layer solely to retain
the previous parse-error-and-continue behavior.

## OAuth 2.1 — Stateless Token Design

The OAuth implementation is fully stateless — no database or in-memory store is needed. All state is encrypted into the tokens themselves using AES-GCM with a shared `OAUTH_TOKEN_SECRET`.

### Encrypted Blob Types

Each blob is prefixed with a type byte to prevent cross-type confusion:

| Type | Blob | Contents | Created At | Used At |
|------|------|----------|------------|---------|
| `0x01` | `client_id` | `{redirect_uris, client_name, created_at}` | `/oauth/register` | `/oauth/authorize` |
| `0x02` | `authorization_code` | `{api_key, signoz_url, client_id, redirect_uri, code_challenge, expires_at}` | `/oauth/authorize` (form submit) | `/oauth/token` |
| `0x03` | `refresh_token` | `{api_key, signoz_url, client_id, expires_at}` | `/oauth/token` | `/oauth/token` (refresh grant) |
| (untagged) | `access_token` | `{api_key, signoz_url, client_id, expires_at}` | `/oauth/token` | `/mcp` (every request) |

### Multi-Instance Deployment

Since tokens are self-contained encrypted blobs, any server instance with the same `OAUTH_TOKEN_SECRET` can validate any token. No sticky sessions or shared state needed. The only requirement is that all instances share the same encryption key.

### Credential header routing

The auth middleware forwards each credential upstream on the **header the client used** — SigNoz classifies credentials by header name, not token shape:

- `SIGNOZ-API-KEY: <key>` → forwarded as `SIGNOZ-API-KEY` (service-account API keys).
- `Authorization: [Bearer] <token>` → forwarded as `Authorization: Bearer <token>` (user/session tokens, JWT **or** opaque).

When OAuth is enabled, the middleware first tries to decrypt an `Authorization` Bearer token as a server-issued OAuth access token; a valid one unwraps to a stored API key forwarded via `SIGNOZ-API-KEY`. Only if decryption fails (and a SigNoz URL is available) is the token treated as a direct credential and forwarded on `Authorization`.

> **Removed (breaking):** earlier versions used a shape heuristic (`isJWTToken`) to reroute non-JWT `Authorization` tokens to `SIGNOZ-API-KEY`. That heuristic misrouted opaque user/session tokens (which SigNoz only accepts on `Authorization`) and has been removed. Clients sending a service-account API key must use the `SIGNOZ-API-KEY` header, not `Authorization`.
