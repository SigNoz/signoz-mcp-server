# Feature: Cache SigNoz Clients by URL — Context & Discussion

## Original Prompt
> work on https://github.com/SigNoz/signoz-mcp-server/issues/69
>
> Issue #69 (follow-up to a review comment on PR #63):
>
> internal/handler/tools/handler.go
> ```
> return nil, fmt.Errorf("missing tenant credentials in context (apiKey or signozURL)")
> }
> cacheKey := util.HashTenantKey(apiKey, signozURL)
> ```
>
> @therealpandey: In order to make the best use of the client cache, it is better to cache
> clients based on just signozURL instead of using a combination of apiKey + signozURL.
> Multiple apiKeys can reuse the same signozURL cache which is ideal for reusing connections
> to the same signozURL.
>
> @akshaysw: The client stores the apiKey and uses it to set the SIGNOZ-API-KEY header on
> every request. If we cache by just signozURL, different user with different API keys would
> share the same client — and requests would go out with the wrong API key.
>
> @therealpandey: Understood. Why is the client storing the apikey? If we are caching the
> http client, we should reuse as much as possible to reuse connections. Otherwise it's an
> incomplete attempt to cache the http client. If the answer to this is "legacy" code, please
> take it up as an enhancement to be done later.

## Reference Links
- [Issue #69](https://github.com/SigNoz/signoz-mcp-server/issues/69)
- [PR #63 review thread](https://github.com/SigNoz/signoz-mcp-server/pull/63#discussion_r2955296138)
- Design spec: `docs/superpowers/specs/2026-06-04-client-cache-by-url-design.md`
- Implementation plan: `docs/superpowers/plans/2026-06-04-client-cache-by-url.md`

## Key Decisions & Discussion Log

### 2026-06-04 — Chosen approach: context-sourced credentials
- The reviewer's question ("why is the client storing the apiKey?") is answered by removing
  the credential from the client entirely. Credentials already enter the system via the
  request context (both transports set apiKey/authHeader/signozURL on ctx before any handler
  runs). The client reads them from ctx at send time instead of from struct fields.
- Cache the full `SigNoz` client keyed by `signozURL` alone → connection pools are reused
  across API keys hitting the same URL. Considered (and rejected) a "share transport only"
  variant that keeps apiKey in the client — that's the incomplete fix the reviewer called out.

### 2026-06-04 — Open question resolved: analytics identity cache
- The per-client `cachedIdentity` is credential-specific (used for analytics attribution and
  picks the /me endpoint by auth header). With the client now shared per-URL, a single cached
  value would mix identities across API keys.
- **Resolved:** make it a per-credential map keyed by `HashCredential(apiKey, authHeader)`,
  guarded by the existing mutex. The auth-header default (`SIGNOZ-API-KEY`) is applied once so
  the cache key and the endpoint selection always agree.

### 2026-06-04 — Spec adversarial re-review caught 6 issues before implementation
- Non-compiling `NormalizeSigNozURL` in the cache-key pseudocode → use `strings.ToLower`.
- Fail-closed guard must cover `doValidationRequest` (shared by ValidateCredentials &
  fetchAnalyticsIdentity), not just `doRequest`.
- `fetchAnalyticsIdentity` endpoint selection must read auth header from ctx, not a struct field.
- Identity-cache key/endpoint default must be applied consistently to avoid a split-cache bug.
- ~40 `client_test.go` call sites need migration to ctx-supplied credentials.
- `HashTenantKey` had exactly one caller; repurposed → `HashCredential` (no dead code).

### 2026-06-04 — Cache-key keying choice
- `strings.ToLower(signozURL)` rather than the raw URL, to match the existing case-insensitive
  `EqualFold` custom-header gate. Deliberately NOT applying `NormalizeSigNozURL` (out of scope;
  not applied to the ctx URL today). Credentials are no longer part of the key, so the exact
  keying is a sharing-efficiency choice, not a correctness/security one.

## Open Questions
- [x] How should the analytics identity cache behave on a shared client? → per-credential map
  keyed by `HashCredential(apiKey, authHeader)`. (Resolved 2026-06-04.)
