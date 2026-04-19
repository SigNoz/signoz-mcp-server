# Design: `mcp_` Test Token Format

## Summary

Add a **testing-only** bearer token format, `mcp_<base64url(json)>`, that carries both the SigNoz base URL and API key in a single token. When enabled via `MCP_TEST_TOKEN_ENABLED=true`, the HTTP auth middleware extracts URL and key from the token and uses them for the request, removing the need to pass `X-SigNoz-URL` and `SIGNOZ-API-KEY` as separate headers.

This format is **unsigned and unencrypted**. It exists to simplify local testing and demos — not for production use.

## Motivation

The current HTTP auth flow requires a client to send either:
- `SIGNOZ-API-KEY` + `X-SigNoz-URL` headers, or
- an encrypted OAuth bearer token (gated by `OAUTH_ENABLED`), or
- a JWT in `Authorization` alongside `X-SigNoz-URL`.

For quick multi-tenant testing, juggling two headers is friction. A single bearer token that carries everything makes `curl` and test harnesses simpler.

## Token Format

```
mcp_<base64url-encoded-json>
```

Decoded payload:

```json
{
  "headers": {
    "Authorization": "Bearer",
    "X-SigNoz-URL": "https://tenant.signoz.cloud",
    "KEY": "<signoz-api-key>"
  }
}
```

### Parsing rules

- Prefix `mcp_` is a literal ASCII match (case-sensitive). Prefix absence → fall through to existing auth paths untouched.
- The body is decoded with `base64.RawURLEncoding` first; if that fails, `base64.URLEncoding` (padded) is tried as a tolerance.
- Decoded bytes must be valid JSON containing a `headers` object.
- Only two keys in `headers` drive behavior:
  - `X-SigNoz-URL` — required, must be a non-empty absolute HTTPS URL. Trailing slash is trimmed (matches `config.go:66`).
  - `KEY` — required, non-empty string, used as the SigNoz API key.
- All other keys in `headers` (including `Authorization`) are ignored.
- No signing, no encryption, no expiry. The enabling env var is the only guardrail.

### Failure modes

Any parse, decode, or validation failure returns `401 Unauthorized`. Once the middleware commits to the `mcp_` branch, it does **not** fall through to OAuth/JWT/PAT paths — a malformed `mcp_` token is a hard failure, not ambiguous input.

## Configuration

New env var in `internal/config/config.go`:

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `MCP_TEST_TOKEN_ENABLED` | bool | `false` | When true, the HTTP auth middleware recognizes `Bearer mcp_...` tokens and extracts URL + key from them. |

No other env vars required. The flag is independent of `OAUTH_ENABLED` — both can be on simultaneously (the `mcp_` branch runs first and is orthogonal to the OAuth-decrypt branch).

## Middleware Integration

Lives in `authMiddleware` at `internal/mcp-server/server.go:552-678`, inserted as the **first branch** after reading the `Authorization` header:

```
authMiddleware(r):
  auth = r.Header.Get("Authorization")
  if cfg.MCPTestTokenEnabled && strings.HasPrefix(auth, "Bearer mcp_"):
      url, key, err = parseMCPTestToken(auth)   // strips "Bearer " prefix internally
      if err: return 401 with err.Error()
      ctx = util.SetSigNozURL(ctx, url)
      ctx = util.SetAPIKey(ctx, key)
      ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
      return next.ServeHTTP(w, r.WithContext(ctx))
  // ... existing OAuth / JWT / PAT / legacy branches unchanged
```

### Precedence (token wins)

When the `mcp_` branch fires, it ignores:
- `X-SigNoz-URL` HTTP header on the request
- `SIGNOZ-API-KEY` HTTP header on the request

The token is self-contained. If a user sends both an `mcp_` token and a conflicting `X-SigNoz-URL` header, the token's URL is used.

### Auth header selection

The payload carries a raw SigNoz API key, so the downstream client uses `SIGNOZ-API-KEY` as the auth header (same as the current PAT flow at `handler.go:88`). The literal `"Authorization": "Bearer"` string inside the payload is a placeholder and is not forwarded anywhere.

### Client cache

Untouched. The existing LRU in `internal/handler/tools/handler.go` keyed by `sha256(apiKey + "\x00" + url)` handles `mcp_`-sourced credentials identically to any other source — same cache key, same TTL.

### Behavior when disabled

If `MCP_TEST_TOKEN_ENABLED=false` and a request arrives with `Bearer mcp_...`, the new branch is skipped and the token falls through to the existing auth logic, where it will fail naturally:
- OAuth-decrypt path: base64-JSON is not a valid AES-GCM blob → decrypt error → 401.
- JWT detection (`server.go:548-550`): three-dot shape won't match → treated as PAT.
- Legacy PAT path: SigNoz API rejects the token as an invalid key.

No special fallthrough handling needed.

## New Files

- `internal/auth/mcptesttoken.go` — the parser:
  ```go
  package auth
  func ParseMCPTestToken(authHeader string) (signozURL, apiKey string, err error)
  ```
  Keeping this outside `mcp-server/server.go` keeps the middleware readable and the parser unit-testable without spinning up an HTTP server.

- `internal/auth/mcptesttoken_test.go` — unit tests (see Testing section).

## Modified Files

| File | Change |
|---|---|
| `internal/config/config.go` | Add `MCPTestTokenEnabled bool` field, `MCPTestTokenEnabledEnv` const, default false. Load with `getEnvBool`. |
| `internal/mcp-server/server.go` | Pass `cfg.MCPTestTokenEnabled` into `authMiddleware` (or reference via existing config closure); add the new branch as described above. Emit warn log at startup when flag is on. |
| `internal/mcp-server/server_test.go` | Add integration tests for middleware behavior with flag on/off. |

## Logging

- **Success:** debug level — `"authenticated via mcp_ test token"` with the SigNoz URL. **Never log the key.**
- **Parse failure:** warn level — with the specific error reason, no token contents.
- **Startup:** if `MCP_TEST_TOKEN_ENABLED=true`, warn-level log — `"MCP_TEST_TOKEN_ENABLED is on — accepting unsigned mcp_ tokens; do not use in production"`. Makes the security posture visible in operator logs.

## Error Reference

All return `401 Unauthorized` with a JSON body matching the middleware's existing error shape:

| Condition | Message |
|---|---|
| base64 decode failure | `invalid mcp_ token: bad base64` |
| JSON unmarshal failure | `invalid mcp_ token: bad json` |
| `headers` key missing or not an object | `invalid mcp_ token: missing headers object` |
| `X-SigNoz-URL` missing / non-HTTPS / malformed | `invalid mcp_ token: missing or invalid X-SigNoz-URL` |
| `KEY` missing or empty | `invalid mcp_ token: missing KEY` |

## Testing

### Unit tests — `internal/auth/mcptesttoken_test.go`

- Valid token, unpadded base64 → URL + key extracted.
- Valid token, padded base64 → URL + key extracted.
- Valid payload with extra keys in `headers` → ignored, URL + key still extracted.
- Invalid base64 → `bad base64` error.
- Valid base64 but not JSON → `bad json` error.
- JSON without `headers` → `missing headers object` error.
- `X-SigNoz-URL` missing → error.
- `X-SigNoz-URL` present but not HTTPS (e.g. `http://...`) → error.
- `X-SigNoz-URL` with trailing slash → slash trimmed in returned URL.
- `KEY` missing → error.
- `KEY` empty string → error.

### Integration tests — `internal/mcp-server/server_test.go`

- Flag off + `Bearer mcp_<valid>` → behavior unchanged; new code path not entered.
- Flag on + valid `Bearer mcp_...` → context carries URL, key, and `SIGNOZ-API-KEY` as auth header name.
- Flag on + valid `Bearer mcp_...` **and** conflicting `X-SigNoz-URL` header → token's URL wins.
- Flag on + malformed `Bearer mcp_...` → 401, does not fall through.
- Flag on + non-`mcp_` bearer → existing OAuth/JWT/PAT paths unchanged.

## End-to-End Verification

1. Start server with `MCP_TEST_TOKEN_ENABLED=true`, no `SIGNOZ_URL` or `SIGNOZ_API_KEY` set, in `TRANSPORT_MODE=http`.
2. Build a token:
   ```bash
   PAYLOAD='{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}'
   TOKEN="mcp_$(printf '%s' "$PAYLOAD" | base64 | tr -d '=' | tr '/+' '_-')"
   ```
3. Hit a tool endpoint:
   ```bash
   curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/mcp ...
   ```
4. Expect the tool to execute against `tenant.signoz.cloud` using `sk_xxx`.
5. Confirm startup logs include the `MCP_TEST_TOKEN_ENABLED is on — ...` warning.

## Out of Scope

Explicitly not part of this change:

- Token expiry, signing, rotation.
- Forwarding arbitrary extra headers from the payload to SigNoz.
- A CLI helper for minting tokens (the `base64` one-liner above is sufficient).
- Any change to OAuth, JWT, PAT, or stdio auth paths.
- Any change to client caching, tool handlers, or the SigNoz client factory.

## Security Notes

- The `mcp_` token carries a raw SigNoz API key base64-encoded — anyone who sees the token sees the key. This is identical to sending the key directly in a header, so the token adds no *new* exposure beyond the existing PAT flow, but it also provides no confidentiality.
- No expiry means a leaked token is valid until the underlying SigNoz API key is revoked. This matches today's PAT behavior.
- The enabling flag defaults to false. The startup warn log ensures the security posture is visible when operators turn it on.
- This format **must not** be advertised as a production option. README/docs changes (if any) should mark it as testing-only.
