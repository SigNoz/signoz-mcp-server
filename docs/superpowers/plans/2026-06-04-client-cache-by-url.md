# Cache SigNoz Clients by URL Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cache one `SigNoz` client (and its connection pool) per `signozURL`, shared across API keys, by reading per-request credentials from context instead of baking them into the client.

**Architecture:** Remove `apiKey`/`authHeaderName` from the `SigNoz` struct; `doRequest` and `doValidationRequest` read them from the request context (fail closed if absent). The handler cache keys on the URL alone. The per-client analytics identity cache becomes a per-credential map so attribution stays correct.

**Tech Stack:** Go 1.25, `net/http`, `hashicorp/golang-lru/v2/expirable`, `slog`, `testify`.

Spec: `docs/superpowers/specs/2026-06-04-client-cache-by-url-design.md`. Issue: [#69](https://github.com/SigNoz/signoz-mcp-server/issues/69).

---

## File Structure

- `pkg/util/context.go` — rename `HashTenantKey(apiKey, signozURL)` → `HashCredential(apiKey, authHeader)` (same SHA-256 body).
- `internal/client/client.go` — drop `apiKey`/`authHeaderName` from struct + `NewClient`; read creds from ctx in `doRequest`/`doValidationRequest`; per-credential identity cache.
- `internal/handler/tools/handler.go` — cache key = `strings.ToLower(signozURL)`; updated `NewClient` call.
- `internal/oauth/handlers.go` — `validateSigNozCredentials` seeds ctx with creds before calling `ValidateCredentials`.
- `internal/client/client_test.go` — migrate ~40 `NewClient` call sites to 3-arg form + credentialed ctx; add per-credential identity-cache test.
- `docs/architecture.md` — update caching description.

> **Note on TDD ordering:** because the `NewClient` signature changes, the package will not compile until Task 2 + Task 6 are both done. Tasks 1–6 form one compile unit; run the full `go build ./...` / `go test ./internal/client/...` only at the checkpoints noted. Commit per task regardless (a red build mid-sequence is expected and called out).

---

### Task 1: Rename `HashTenantKey` → `HashCredential`

**Files:**
- Modify: `pkg/util/context.go:138-146`
- Test: `pkg/util/` (add if a context_test.go exists; otherwise skip — pure rename)

- [ ] **Step 1: Rename the function and update its doc comment**

In `pkg/util/context.go`, replace the existing `HashTenantKey`:

```go
// HashCredential returns a SHA-256 hash of apiKey and authHeader, suitable for
// use as a map key without exposing the raw API key in memory. A null-byte
// separator prevents collisions between pairs that contain colons.
func HashCredential(apiKey, authHeader string) string {
	h := sha256.Sum256([]byte(apiKey + "\x00" + authHeader))
	return hex.EncodeToString(h[:])
}
```

- [ ] **Step 2: Verify no other references remain**

Run: `grep -rn "HashTenantKey" --include="*.go" .`
Expected: no output (the only caller, `handler.go`, is rewritten in Task 5).

- [ ] **Step 3: Commit**

```bash
git add pkg/util/context.go
git commit -m "refactor(util): rename HashTenantKey to HashCredential"
```

---

### Task 2: Drop baked-in credentials from the `SigNoz` struct and `NewClient`

**Files:**
- Modify: `internal/client/client.go:55-87`

> This task intentionally leaves the package non-compiling (the `s.apiKey` / `s.authHeaderName` references in `doRequest`/`doValidationRequest`/`fetchAnalyticsIdentity` and the identity-cache fields are fixed in Tasks 3–4). Commit anyway.

- [ ] **Step 1: Edit the struct — remove `apiKey`, `authHeaderName`, `cachedIdentity`, `identityCachedAt`; add the identity map**

Replace the `SigNoz` struct (lines 55-67) with:

```go
type SigNoz struct {
	baseURL       string
	logger        *slog.Logger
	httpClient    *http.Client
	customHeaders map[string]string

	identityMu    sync.Mutex
	identityCache map[string]identityEntry // key: util.HashCredential(apiKey, authHeader)
	meters        *otelpkg.Meters
}

// identityEntry is one cached analytics identity with its fetch time, so the
// per-credential cache can expire entries independently.
type identityEntry struct {
	identity *AnalyticsIdentity
	cachedAt time.Time
}
```

- [ ] **Step 2: Edit `NewClient` — drop `apiKey`/`authHeaderName` params, init the map**

Replace the `NewClient` signature and body (lines 69-87):

```go
func NewClient(log *slog.Logger, baseURL string, customHeaders map[string]string) *SigNoz {
	return &SigNoz{
		logger:        log,
		baseURL:       baseURL,
		customHeaders: customHeaders,
		identityCache: make(map[string]identityEntry),
		httpClient: &http.Client{
			// Default client span name is just the HTTP method (per OTel HTTP
			// semconv — the client doesn't know a templated route). We keep
			// the default rather than stamping the raw path because several
			// SigNoz API paths embed IDs (/dashboards/{uuid}, /rules/{id},
			// /channels/{id}, /explorer/views/{id}, /rules/{id}/history/...)
			// which would blow up span-name cardinality in the backend. The
			// full URL is still attached as a span attribute for drilling.
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}
```

- [ ] **Step 3: Add the util import if not present**

Check the import block already has `"github.com/SigNoz/signoz-mcp-server/pkg/util"` (it does — used by `ensureTenantContext`). No change needed.

- [ ] **Step 4: Commit (build still red — expected)**

```bash
git add internal/client/client.go
git commit -m "refactor(client): remove baked-in credentials from SigNoz struct"
```

---

### Task 3: Read credentials from context in the two request senders

**Files:**
- Modify: `internal/client/client.go` — `doValidationRequest` (~239-270) and `doRequest` (~298-338)

- [ ] **Step 1: Add a small credential resolver near the senders**

Add this helper above `doValidationRequest`:

```go
// credentialsFromContext pulls the per-request API key and auth header from
// ctx. The auth header defaults to SIGNOZ-API-KEY (matching the handler's
// fallback). An empty apiKey is an error: we fail closed rather than send an
// unauthenticated request, since the client no longer stores a credential.
func credentialsFromContext(ctx context.Context) (apiKey, authHeader string, err error) {
	apiKey, _ = util.GetAPIKey(ctx)
	authHeader, _ = util.GetAuthHeader(ctx)
	if authHeader == "" {
		authHeader = SignozApiKey
	}
	if apiKey == "" {
		return "", "", errors.New("missing API key in request context")
	}
	return apiKey, authHeader, nil
}
```

(`SignozApiKey` is the existing const `"SIGNOZ-API-KEY"` at client.go:26. `errors` is already imported.)

- [ ] **Step 2: Update `doValidationRequest` to use ctx creds**

Replace the header-stamping block (currently lines 245-252):

```go
	apiKey, authHeader, err := credentialsFromContext(ctx)
	if err != nil {
		return 0, nil, err
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(authHeader, apiKey)

	for k, v := range s.customHeaders {
		if !strings.EqualFold(k, ContentType) && !strings.EqualFold(k, authHeader) {
			req.Header.Set(k, v)
		}
	}
```

(The `err` from `credentialsFromContext` shadows nothing — `req, err :=` is above it; reuse `err =` if Go complains about redeclaration, i.e. write `apiKey, authHeader, err := ...` only if `err` is not already in scope at that point. It is in scope from `http.NewRequestWithContext`, so use `var apiKey, authHeader string; apiKey, authHeader, err = credentialsFromContext(ctx)` OR simply `apiKey, authHeader, credErr := credentialsFromContext(ctx); if credErr != nil { return 0, nil, credErr }`. Use the `credErr` form to avoid the shadowing question.)

Final form for clarity:

```go
	apiKey, authHeader, credErr := credentialsFromContext(ctx)
	if credErr != nil {
		return 0, nil, credErr
	}

	req.Header.Set(ContentType, "application/json")
	req.Header.Set(authHeader, apiKey)

	for k, v := range s.customHeaders {
		if !strings.EqualFold(k, ContentType) && !strings.EqualFold(k, authHeader) {
			req.Header.Set(k, v)
		}
	}
```

- [ ] **Step 3: Update `doRequest` to use ctx creds**

In `doRequest`, resolve credentials once before the retry loop (after the body-buffering block, before `for attempt := range maxRetries`):

```go
	apiKey, authHeader, credErr := credentialsFromContext(ctx)
	if credErr != nil {
		return nil, credErr
	}
```

Then replace the in-loop header block (currently lines 327-338):

```go
		req.Header.Set(ContentType, "application/json")
		req.Header.Set(authHeader, apiKey)

		for k, v := range s.customHeaders {
			if strings.EqualFold(k, ContentType) || strings.EqualFold(k, authHeader) {
				s.logger.WarnContext(ctx, "Custom header overrides a reserved header",
					slog.String("header", k), slog.String("value", v))
				continue
			}
			req.Header.Set(k, v)
		}
```

- [ ] **Step 4: Commit (build still red until Task 4 — expected)**

```bash
git add internal/client/client.go
git commit -m "refactor(client): stamp auth header from request context"
```

---

### Task 4: Make the analytics identity cache per-credential

**Files:**
- Modify: `internal/client/client.go` — `GetAnalyticsIdentity` (~143-168), `fetchAnalyticsIdentity` (~170-200)

- [ ] **Step 1: Rewrite `GetAnalyticsIdentity` to key the cache by credential**

Replace the body of `GetAnalyticsIdentity` (from `ctx = s.ensureTenantContext(ctx)` through the final `return`):

```go
func (s *SigNoz) GetAnalyticsIdentity(ctx context.Context) (*AnalyticsIdentity, error) {
	ctx = s.ensureTenantContext(ctx)

	apiKey, authHeader, credErr := credentialsFromContext(ctx)
	if credErr != nil {
		return nil, credErr
	}
	cacheKey := util.HashCredential(apiKey, authHeader)

	s.identityMu.Lock()
	defer s.identityMu.Unlock()

	if entry, ok := s.identityCache[cacheKey]; ok && time.Since(entry.cachedAt) < analyticsIdentityCacheTTL {
		if s.meters != nil {
			attrs := otelpkg.AppendTenantURL(ctx, nil)
			s.meters.IdentityCacheHits.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
		return entry.identity, nil
	}
	if s.meters != nil {
		attrs := otelpkg.AppendTenantURL(ctx, nil)
		s.meters.IdentityCacheMisses.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	identity, err := s.fetchAnalyticsIdentity(ctx, authHeader)
	if err != nil {
		return nil, err
	}

	s.identityCache[cacheKey] = identityEntry{identity: identity, cachedAt: time.Now()}
	return identity, nil
}
```

- [ ] **Step 2: Update `fetchAnalyticsIdentity` to take the auth header explicitly**

Change the signature and endpoint selection (replace `func (s *SigNoz) fetchAnalyticsIdentity(ctx context.Context)` and the `strings.EqualFold(s.authHeaderName, ...)` block):

```go
func (s *SigNoz) fetchAnalyticsIdentity(ctx context.Context, authHeader string) (*AnalyticsIdentity, error) {
	ctx = s.ensureTenantContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	endpoint := "/api/v1/service_accounts/me"
	principal := "service_account"
	if strings.EqualFold(authHeader, "Authorization") {
		endpoint = "/api/v2/users/me"
		principal = "user"
	}
	// ... rest unchanged (reqURL, doValidationRequest, parse) ...
```

Passing `authHeader` explicitly (rather than re-reading ctx) guarantees the endpoint matches the credential used for the cache key — same value, no second default applied.

- [ ] **Step 3: Build the client package**

Run: `go build ./internal/client/...`
Expected: success (package compiles again).

- [ ] **Step 4: Commit**

```bash
git add internal/client/client.go
git commit -m "refactor(client): key analytics identity cache by credential"
```

---

### Task 5: Cache clients by URL in the handler

**Files:**
- Modify: `internal/handler/tools/handler.go:81,96`

- [ ] **Step 1: Replace the cache key and `NewClient` call**

In `GetClient`, replace line 81:

```go
	cacheKey := strings.ToLower(signozURL)
```

And the `NewClient` call (line 96):

```go
	newClient := signozclient.NewClient(h.logger, signozURL, headers)
```

Leave the `apiKey == "" || signozURL == ""` guard, the `authHeader` default, and the custom-header URL gate untouched. (`authHeader` is now unused locally after this change — remove the `authHeader, _ := util.GetAuthHeader(ctx)` line and the `if authHeader == "" {...}` block, since the client reads it from ctx itself. Keep `apiKey` for the guard.)

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/handler/tools/handler.go
git commit -m "feat(handler): cache SigNoz clients by URL for connection reuse"
```

---

### Task 6: Seed context in the OAuth validation path

**Files:**
- Modify: `internal/oauth/handlers.go:297-308`

- [ ] **Step 1: Seed apiKey + auth header into ctx before validation**

Replace the body of `validateSigNozCredentials`:

```go
func (h *Handler) validateSigNozCredentials(ctx context.Context, signozURL, apiKey string) error {
	// Only forward custom headers when the user-supplied URL matches the
	// configured SIGNOZ_URL to prevent leaking proxy-auth credentials to
	// attacker-controlled hosts.
	var headers map[string]string
	configNormalized, _ := util.NormalizeSigNozURL(h.config.URL)
	if strings.EqualFold(signozURL, configNormalized) {
		headers = h.config.CustomHeaders
	}

	// The client reads credentials from context, so seed them here — the OAuth
	// flow received them as form fields / decrypted grants, not on the context.
	ctx = util.SetAPIKey(ctx, apiKey)
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")

	signozClient := client.NewClient(h.logger, signozURL, headers)
	return signozClient.ValidateCredentials(ctx)
}
```

- [ ] **Step 2: Confirm `util` is imported in handlers.go**

Run: `grep -n '"github.com/SigNoz/signoz-mcp-server/pkg/util"' internal/oauth/handlers.go`
Expected: a match (it is already imported — used by `NormalizeSigNozURL`).

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add internal/oauth/handlers.go
git commit -m "fix(oauth): seed request context credentials for validation"
```

---

### Task 7: Add a credentialed-context test helper and migrate client tests

**Files:**
- Modify: `internal/client/client_test.go` (all ~40 `NewClient` call sites + ctx usage)

- [ ] **Step 1: Add a test helper for a credentialed context**

Add near the top of `client_test.go` (after imports):

```go
// credCtx returns a context carrying the credentials the client now reads at
// request time. Defaults match the old baked-in test values.
func credCtx() context.Context {
	ctx := util.SetAPIKey(context.Background(), "test-api-key")
	return util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
}

// credCtxFor returns a credentialed context for a specific apiKey/authHeader,
// used by tests that exercise the Authorization (user-token) path.
func credCtxFor(apiKey, authHeader string) context.Context {
	ctx := util.SetAPIKey(context.Background(), apiKey)
	return util.SetAuthHeader(ctx, authHeader)
}
```

Add `"github.com/SigNoz/signoz-mcp-server/pkg/util"` to the test imports.

- [ ] **Step 2: Migrate all `NewClient` calls to the 3-arg form**

Run a mechanical replacement, then fix the credential-bearing call sites:

```bash
# Drop the apiKey + authHeader args from every NewClient(...) in the test file.
# Do this by hand or with gofmt-safe sed; verify with the build afterward.
grep -n "NewClient(" internal/client/client_test.go
```

For each call `NewClient(logger, server.URL, "test-api-key", "SIGNOZ-API-KEY", nil)` →
`NewClient(logger, server.URL, nil)`: drop the 3rd (apiKey) and 4th (authHeader) args, keep
the final headers arg as-is. The three call sites that pass a non-nil headers value — lines
**1667** and **1729** (`customHeaders`) and **1702** (`map[string]string{}`) — must keep that
final arg: e.g. `NewClient(logger, server.URL, customHeaders)`.

- [ ] **Step 3: Replace `context.Background()` with credentialed ctx at request call sites**

Every `client.<Method>(context.Background(), ...)` and `c.doRequest(context.Background(), ...)` becomes `...(credCtx(), ...)`. For the `ctx := context.Background()` locals (lines 104, 621, 741, 870, 1056, 1221, 1283, 1426, 1527), change to `ctx := credCtx()`.

For the cancellation test (line 1598, `ctx, cancel := context.WithCancel(context.Background())`), wrap the credentialed base: `ctx, cancel := context.WithCancel(credCtx())`.

- [ ] **Step 4: Fix the auth-header-varying identity tests to set creds on ctx**

In `TestGetAnalyticsIdentity` (lines ~355-377), the `NewClient(logger, server.URL, apiKey, tt.authHeaderName, nil)` becomes `NewClient(logger, server.URL, nil)`, and the call becomes:

```go
			client := NewClient(logger, server.URL, nil)
			apiKey := "test-api-key"
			if tt.authHeaderName == "Authorization" {
				apiKey = "Bearer jwt-token"
			}
			identity, err := client.GetAnalyticsIdentity(credCtxFor(apiKey, tt.authHeaderName))
```

In `TestGetAnalyticsIdentity_CachesResult` and `..._ConcurrentCallsDedupe`, replace `NewClient(logger, server.URL, "Bearer jwt", "Authorization", nil)` with `NewClient(logger, server.URL, nil)` and pass `credCtxFor("Bearer jwt", "Authorization")` to each `GetAnalyticsIdentity` call.

- [ ] **Step 5: Run the client tests**

Run: `go test ./internal/client/... -count=1`
Expected: PASS. (If a request test asserts `r.Header.Get("SIGNOZ-API-KEY") == "test-api-key"`, it still passes because `credCtx()` carries that value.)

- [ ] **Step 6: Commit**

```bash
git add internal/client/client_test.go
git commit -m "test(client): migrate tests to context-supplied credentials"
```

---

### Task 8: New test — missing-credential request fails closed

**Files:**
- Modify: `internal/client/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDoRequest_MissingCredentialFailsClosed(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(logpkg.New("error"), server.URL, nil)

	// Background ctx has no API key -> must error before any HTTP call.
	_, err := client.doRequest(context.Background(), http.MethodGet, server.URL, nil, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing API key")
	assert.False(t, called, "no HTTP request should be sent without credentials")
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/client/ -run TestDoRequest_MissingCredentialFailsClosed -v -count=1`
Expected: PASS (implementation from Task 3 already enforces this).

- [ ] **Step 3: Commit**

```bash
git add internal/client/client_test.go
git commit -m "test(client): assert requests fail closed without credentials"
```

---

### Task 9: New test — identity cache is per-credential

**Files:**
- Modify: `internal/client/client_test.go`

- [ ] **Step 1: Write the test**

```go
func TestGetAnalyticsIdentity_PerCredentialCache(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		// service_accounts/me shape — id echoed from the API key isn't needed;
		// we only assert request counts differ per credential.
		_, _ = w.Write([]byte(`{"status":"success","data":{"id":"sa-1","email":"a@x.io","orgId":"org-1"}}`))
	}))
	defer server.Close()

	client := NewClient(logpkg.New("error"), server.URL, nil)

	// Two distinct credentials -> two fetches.
	_, err := client.GetAnalyticsIdentity(credCtxFor("key-a", "SIGNOZ-API-KEY"))
	require.NoError(t, err)
	_, err = client.GetAnalyticsIdentity(credCtxFor("key-b", "SIGNOZ-API-KEY"))
	require.NoError(t, err)
	assert.Equal(t, int32(2), requests.Load(), "different credentials must not share a cached identity")

	// Repeating key-a hits the cache -> no new fetch.
	_, err = client.GetAnalyticsIdentity(credCtxFor("key-a", "SIGNOZ-API-KEY"))
	require.NoError(t, err)
	assert.Equal(t, int32(2), requests.Load(), "same credential must reuse the cached identity")
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/client/ -run TestGetAnalyticsIdentity_PerCredentialCache -v -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/client/client_test.go
git commit -m "test(client): verify per-credential identity cache isolation"
```

---

### Task 10: New test — handler shares one client across API keys per URL

**Files:**
- Create or Modify: `internal/handler/tools/handler_test.go` (no handler-level cache test exists today; create the file if absent)

- [ ] **Step 1: Write the test**

```go
package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

func ctxWith(apiKey, url string) context.Context {
	ctx := util.SetAPIKey(context.Background(), apiKey)
	ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
	return util.SetSigNozURL(ctx, url)
}

func TestGetClient_SharedAcrossAPIKeysPerURL(t *testing.T) {
	h := &Handler{
		logger:      logpkg.New("error"),
		clientCache: expirable.NewLRU[string, *signozclient.SigNoz](16, nil, 0),
	}

	c1, err := h.GetClient(ctxWith("key-a", "https://tenant.example.com"))
	require.NoError(t, err)
	c2, err := h.GetClient(ctxWith("key-b", "https://tenant.example.com"))
	require.NoError(t, err)
	assert.Same(t, c1, c2, "same URL must reuse one cached client regardless of API key")

	c3, err := h.GetClient(ctxWith("key-a", "https://other.example.com"))
	require.NoError(t, err)
	assert.NotSame(t, c1, c3, "different URLs must get different clients")
}

func TestGetClient_MissingCredentialsError(t *testing.T) {
	h := &Handler{
		logger:      logpkg.New("error"),
		clientCache: expirable.NewLRU[string, *signozclient.SigNoz](16, nil, 0),
	}
	_, err := h.GetClient(util.SetSigNozURL(context.Background(), "https://x.io")) // no apiKey
	assert.Error(t, err)
}
```

(`GetClient` returns the `signozclient.Client` interface; `assert.Same` compares the underlying pointers, which works because both are the same `*SigNoz`. If `assert.Same` rejects interface values, compare `c1 == c2` directly instead.)

- [ ] **Step 2: Run it**

Run: `go test ./internal/handler/tools/ -run TestGetClient -v -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/handler/tools/handler_test.go
git commit -m "test(handler): verify per-URL client cache sharing"
```

---

### Task 11: Update architecture docs

**Files:**
- Modify: `docs/architecture.md` (caching section / the `HANDLER` node + auth flow text)

- [ ] **Step 1: Update the caching description**

Find the text describing the cache key (currently per-tenant apiKey+URL). Replace the description with: the handler caches one `SigNoz` client per `signozURL` (case-insensitive), shared across API keys to maximize HTTP connection reuse; per-request credentials (`apiKey`, auth header) are read from the request context at send time, and the analytics identity cache is keyed per credential. Adjust the `Handler with LRU clientCache` node label/notes if they mention the key composition.

- [ ] **Step 2: Verify no other doc references the old key**

Run: `grep -rn -i "apiKey.*signozURL\|HashTenantKey\|per-tenant" docs/`
Expected: no stale references to apiKey-based client caching remain.

- [ ] **Step 3: Commit**

```bash
git add docs/architecture.md
git commit -m "docs: describe per-URL client caching"
```

---

### Task 12: Update the plans/ convention pair and full verification

**Files:**
- Create: `plans/handle-client-cache.context.md`, `plans/handle-client-cache.plan.md`

- [ ] **Step 1: Write the repo-convention plan pair**

Create `plans/handle-client-cache.context.md` (original prompt = issue #69 + the PR #63 review thread quoted in the task; reference links to #69 and #63; a dated decision-log entry summarizing the chosen approach) and `plans/handle-client-cache.plan.md` with `## Status\nDone` once shipped, pointing at the design spec and this plan.

- [ ] **Step 2: Run the full verification suite**

Run:
```bash
go build ./...
go vet ./...
go test ./... -count=1
```
Expected: all pass.

- [ ] **Step 3: Run the linter per repo CI**

Run: `golangci-lint run` (or the command in this repo's `CLAUDE.md`/Makefile).
Expected: no new findings.

- [ ] **Step 4: Commit**

```bash
git add plans/handle-client-cache.context.md plans/handle-client-cache.plan.md
git commit -m "docs(plans): record client-cache-by-url feature plan"
```

---

## Verification (end-to-end)

- `go build ./... && go vet ./... && go test ./... -count=1` all green.
- `grep -rn "HashTenantKey" --include="*.go" .` → empty (rename complete, no dead code).
- `grep -rn "s.apiKey\|s.authHeaderName" internal/client/client.go` → empty (no baked-in creds).
- Manual: two MCP requests with different API keys against the same `SIGNOZ_URL` reuse one client (observe a single client-creation debug log per URL, and connection reuse in OTel HTTP metrics).
- Manual: request with no API key in context returns the "missing API key"/"missing tenant credentials" error, never an anonymous upstream call.
