# `mcp_` Test Token Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a testing-only bearer token format `mcp_<base64url(json)>` that carries SigNoz URL and API key in one token, gated by `MCP_TEST_TOKEN_ENABLED`.

**Architecture:** New package `internal/auth` with a single parser function. Config gains one bool field. HTTP auth middleware gets a new first branch for `Bearer mcp_...` that populates context and short-circuits the downstream URL-resolution block (same pattern as the existing `usedOAuthToken` fast-path).

**Tech Stack:** Go 1.22+, standard library only (`encoding/base64`, `encoding/json`), existing `pkg/util` helpers, `go.uber.org/zap` for logging.

**Spec:** `docs/superpowers/specs/2026-04-19-mcp-test-token-design.md`

---

## File Structure

**New files:**
- `internal/auth/mcptesttoken.go` — `ParseMCPTestToken(authHeader string) (signozURL, apiKey string, err error)`. Strips `Bearer ` prefix and the `mcp_` prefix, base64-decodes, JSON-unmarshals, validates, returns normalized URL + key.
- `internal/auth/mcptesttoken_test.go` — unit tests for `ParseMCPTestToken`.

**Modified files:**
- `internal/config/config.go` — add `MCPTestTokenEnabled bool` field, `MCPTestTokenEnabledEnv` const, load via `getEnvBool`.
- `internal/mcp-server/server.go` — add new branch in `authMiddleware` that calls `ParseMCPTestToken`, populates context, and returns early. Add startup warning log if flag is on.
- `internal/mcp-server/server_test.go` — integration tests for the new middleware branch.

**Why a new package (`internal/auth`) instead of colocating in `mcp-server`:** keeps the parser unit-testable in isolation without depending on the full MCP server package, and leaves room for additional auth helpers without bloating `server.go` (already 700+ lines).

---

## Task 1: Add `MCP_TEST_TOKEN_ENABLED` to config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add the env var constant and config field**

Open `internal/config/config.go`. Add `MCPTestTokenEnabled bool` to the `Config` struct after `CustomHeaders`:

```go
	CustomHeaders map[string]string

	// MCPTestTokenEnabled accepts `Bearer mcp_<base64url(json)>` tokens
	// that carry SigNoz URL and API key inline. Testing only.
	MCPTestTokenEnabled bool

	// Analytics settings
	AnalyticsEnabled bool
```

Add the env var constant to the `const` block near `SignozCustomHeaders`:

```go
	SignozCustomHeaders = "SIGNOZ_CUSTOM_HEADERS"
	ClientCacheSize     = "CLIENT_CACHE_SIZE"
	ClientCacheTTL      = "CLIENT_CACHE_TTL_MINUTES"

	MCPTestTokenEnabledEnv = "MCP_TEST_TOKEN_ENABLED"
```

Populate it in `LoadConfig` inside the returned `&Config{...}`. Place it after `CustomHeaders`:

```go
		CustomHeaders:       customHeaders,
		MCPTestTokenEnabled: getEnvBool(MCPTestTokenEnabledEnv, false),
		AnalyticsEnabled:    getEnvBool(AnalyticsEnabledEnv, false),
```

- [ ] **Step 2: Build the project to verify it compiles**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add MCP_TEST_TOKEN_ENABLED flag"
```

---

## Task 2: Create `ParseMCPTestToken` with the first test case

**Files:**
- Create: `internal/auth/mcptesttoken.go`
- Create: `internal/auth/mcptesttoken_test.go`

- [ ] **Step 1: Write the failing test — valid token, unpadded base64**

Create `internal/auth/mcptesttoken_test.go`:

```go
package auth

import (
	"encoding/base64"
	"testing"
)

func makeToken(t *testing.T, payload string) string {
	t.Helper()
	return "mcp_" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestParseMCPTestToken_ValidUnpadded(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}`
	token := makeToken(t, payload)

	url, key, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" {
		t.Errorf("url = %q, want %q", url, "https://tenant.signoz.cloud")
	}
	if key != "sk_xxx" {
		t.Errorf("key = %q, want %q", key, "sk_xxx")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/...`
Expected: FAIL — package `internal/auth` doesn't exist / `ParseMCPTestToken` undefined.

- [ ] **Step 3: Create the parser file with a minimal implementation**

Create `internal/auth/mcptesttoken.go`:

```go
// Package auth provides authentication helpers for the MCP server.
package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/util"
)

// MCPTestTokenPrefix is the literal prefix that marks a test token.
const MCPTestTokenPrefix = "mcp_"

// mcpTestTokenPayload is the JSON shape carried inside the base64-encoded body.
type mcpTestTokenPayload struct {
	Headers map[string]string `json:"headers"`
}

// ParseMCPTestToken parses a bearer token of the form `Bearer mcp_<base64url(json)>`
// and returns the SigNoz URL and API key carried inside it.
//
// The caller is responsible for deciding whether the token applies (prefix match
// after stripping `Bearer `). On any decode, parse, or validation failure it
// returns a non-nil error; the returned URL and key are empty in that case.
//
// This format is testing-only: the payload is neither signed nor encrypted.
func ParseMCPTestToken(authHeader string) (signozURL, apiKey string, err error) {
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, MCPTestTokenPrefix) {
		return "", "", errors.New("not an mcp_ token")
	}
	body := strings.TrimPrefix(raw, MCPTestTokenPrefix)

	decoded, err := decodeBase64(body)
	if err != nil {
		return "", "", errors.New("invalid mcp_ token: bad base64")
	}

	var payload mcpTestTokenPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return "", "", errors.New("invalid mcp_ token: bad json")
	}
	if payload.Headers == nil {
		return "", "", errors.New("invalid mcp_ token: missing headers object")
	}

	rawURL := strings.TrimSpace(payload.Headers["X-SigNoz-URL"])
	if rawURL == "" {
		return "", "", errors.New("invalid mcp_ token: missing or invalid X-SigNoz-URL")
	}
	normalized, nerr := util.NormalizeSigNozURL(strings.TrimSuffix(rawURL, "/"))
	if nerr != nil {
		return "", "", errors.New("invalid mcp_ token: missing or invalid X-SigNoz-URL")
	}

	key := strings.TrimSpace(payload.Headers["KEY"])
	if key == "" {
		return "", "", errors.New("invalid mcp_ token: missing KEY")
	}

	return normalized, key, nil
}

// decodeBase64 accepts both unpadded (RawURLEncoding) and padded (URLEncoding)
// base64url inputs.
func decodeBase64(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/... -run TestParseMCPTestToken_ValidUnpadded -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/mcptesttoken.go internal/auth/mcptesttoken_test.go
git commit -m "feat(auth): add ParseMCPTestToken for mcp_ test token format"
```

---

## Task 3: Cover the remaining parser cases

**Files:**
- Modify: `internal/auth/mcptesttoken_test.go`

- [ ] **Step 1: Add all remaining test cases**

Append to `internal/auth/mcptesttoken_test.go`:

```go
func TestParseMCPTestToken_ValidPadded(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx"}}`
	padded := base64.URLEncoding.EncodeToString([]byte(payload))
	token := "mcp_" + padded

	url, key, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" || key != "sk_xxx" {
		t.Errorf("got (%q, %q), want (https://tenant.signoz.cloud, sk_xxx)", url, key)
	}
}

func TestParseMCPTestToken_ExtraHeadersIgnored(t *testing.T) {
	payload := `{"headers":{"Authorization":"Bearer","X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"sk_xxx","X-Extra":"ignored"}}`
	token := makeToken(t, payload)

	url, key, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" || key != "sk_xxx" {
		t.Errorf("got (%q, %q), want (https://tenant.signoz.cloud, sk_xxx)", url, key)
	}
}

func TestParseMCPTestToken_TrailingSlashTrimmed(t *testing.T) {
	payload := `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud/","KEY":"sk_xxx"}}`
	token := makeToken(t, payload)

	url, _, err := ParseMCPTestToken("Bearer " + token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://tenant.signoz.cloud" {
		t.Errorf("url = %q, want trailing slash trimmed", url)
	}
}

func TestParseMCPTestToken_NotAnMCPToken(t *testing.T) {
	_, _, err := ParseMCPTestToken("Bearer eyJhbGciOi.aaa.bbb")
	if err == nil || !strings.Contains(err.Error(), "not an mcp_ token") {
		t.Errorf("err = %v, want 'not an mcp_ token'", err)
	}
}

func TestParseMCPTestToken_BadBase64(t *testing.T) {
	_, _, err := ParseMCPTestToken("Bearer mcp_!!!not-base64!!!")
	if err == nil || !strings.Contains(err.Error(), "bad base64") {
		t.Errorf("err = %v, want 'bad base64'", err)
	}
}

func TestParseMCPTestToken_BadJSON(t *testing.T) {
	token := "mcp_" + base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "bad json") {
		t.Errorf("err = %v, want 'bad json'", err)
	}
}

func TestParseMCPTestToken_MissingHeadersObject(t *testing.T) {
	token := makeToken(t, `{"something":"else"}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing headers object") {
		t.Errorf("err = %v, want 'missing headers object'", err)
	}
}

func TestParseMCPTestToken_MissingURL(t *testing.T) {
	token := makeToken(t, `{"headers":{"KEY":"sk_xxx"}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "X-SigNoz-URL") {
		t.Errorf("err = %v, want 'X-SigNoz-URL' error", err)
	}
}

func TestParseMCPTestToken_NonHTTPSchemeRejected(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"ftp://x.example.com","KEY":"sk_xxx"}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "X-SigNoz-URL") {
		t.Errorf("err = %v, want 'X-SigNoz-URL' error", err)
	}
}

func TestParseMCPTestToken_MissingKey(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud"}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing KEY") {
		t.Errorf("err = %v, want 'missing KEY'", err)
	}
}

func TestParseMCPTestToken_EmptyKey(t *testing.T) {
	token := makeToken(t, `{"headers":{"X-SigNoz-URL":"https://tenant.signoz.cloud","KEY":"   "}}`)
	_, _, err := ParseMCPTestToken("Bearer " + token)
	if err == nil || !strings.Contains(err.Error(), "missing KEY") {
		t.Errorf("err = %v, want 'missing KEY'", err)
	}
}
```

Add the `strings` import at the top of the test file:

```go
import (
	"encoding/base64"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run all parser tests**

Run: `go test ./internal/auth/... -v`
Expected: all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/auth/mcptesttoken_test.go
git commit -m "test(auth): cover ParseMCPTestToken error and edge cases"
```

---

## Task 4: Integrate the parser into `authMiddleware`

**Files:**
- Modify: `internal/mcp-server/server.go`

- [ ] **Step 1: Add the new auth import**

In `internal/mcp-server/server.go`, add the import:

```go
"github.com/SigNoz/signoz-mcp-server/internal/auth"
```

Place it with the other `SigNoz/signoz-mcp-server/internal/...` imports (near `internal/oauth`).

- [ ] **Step 2: Add the new branch as the first check in `authMiddleware`**

In `authMiddleware` (starts around `server.go:552`), insert the new branch **before** the `if signozAPIKey != ""` block (i.e. right after the `authHeader := r.Header.Get("Authorization")` line).

Replace:

```go
		signozAPIKey := r.Header.Get("SIGNOZ-API-KEY")
		authHeader := r.Header.Get("Authorization")

		var apiKey string
		var signozURL string
		var usedOAuthToken bool
```

with:

```go
		signozAPIKey := r.Header.Get("SIGNOZ-API-KEY")
		authHeader := r.Header.Get("Authorization")

		// mcp_ test token — self-contained token carrying URL + API key.
		// Gated by MCP_TEST_TOKEN_ENABLED; ignores any X-SigNoz-URL /
		// SIGNOZ-API-KEY headers on the request when it fires.
		if m.config.MCPTestTokenEnabled && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")), auth.MCPTestTokenPrefix) {
			tokenURL, tokenKey, err := auth.ParseMCPTestToken(authHeader)
			if err != nil {
				m.logger.Warn("invalid mcp_ test token", zap.Error(err))
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			ctx = util.SetAPIKey(ctx, tokenKey)
			ctx = util.SetAuthHeader(ctx, "SIGNOZ-API-KEY")
			ctx = util.SetSigNozURL(ctx, tokenURL)
			m.logger.Debug("authenticated via mcp_ test token", zap.String("mcp.tenant_url", tokenURL))
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		var apiKey string
		var signozURL string
		var usedOAuthToken bool
```

The branch returns early, so it bypasses the shared URL-resolution block at the bottom of the middleware (same control-flow pattern as the `usedOAuthToken` fast path at `server.go:645-650`).

- [ ] **Step 3: Build to verify the import and branch compile**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/mcp-server/server.go
git commit -m "feat(mcp-server): accept mcp_ test tokens in authMiddleware"
```

---

## Task 5: Emit a startup warning when the flag is on

**Files:**
- Modify: `internal/mcp-server/server.go`

- [ ] **Step 1: Add the warn log near the HTTP server startup**

In `startHTTP` (around `server.go:680`), immediately after `m.logger.Info("MCP Server running in HTTP mode")`, add:

```go
	m.logger.Info("MCP Server running in HTTP mode")

	if m.config.MCPTestTokenEnabled {
		m.logger.Warn("MCP_TEST_TOKEN_ENABLED is on — accepting unsigned mcp_ tokens; do not use in production")
	}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp-server/server.go
git commit -m "feat(mcp-server): warn at startup when mcp_ test tokens are enabled"
```

---

## Task 6: Integration test — flag off leaves `mcp_` bearer untouched

**Files:**
- Modify: `internal/mcp-server/server_test.go`

**Approach:** These tests exercise `authMiddleware` directly via `httptest`. Build an `MCPServer` with a minimal config and a recording `http.Handler` as `next`, then assert on the recorder response and on context values captured by the recording handler.

- [ ] **Step 1: Add a helper to build a test MCPServer at the bottom of the test file**

Append to `internal/mcp-server/server_test.go`:

```go
func newTestMCPServerForAuth(t *testing.T, cfg *config.Config) *MCPServer {
	t.Helper()
	return &MCPServer{
		config: cfg,
		logger: zap.NewNop(),
	}
}

type capturedCtx struct {
	apiKey     string
	authHeader string
	signozURL  string
	called     bool
}

func captureNext(captured *capturedCtx) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.called = true
		captured.apiKey, _ = util.GetAPIKey(r.Context())
		captured.authHeader, _ = util.GetAuthHeader(r.Context())
		captured.signozURL, _ = util.GetSigNozURL(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func mcpTestTokenForTest(t *testing.T, url, key string) string {
	t.Helper()
	payload := `{"headers":{"X-SigNoz-URL":"` + url + `","KEY":"` + key + `"}}`
	return "mcp_" + base64.RawURLEncoding.EncodeToString([]byte(payload))
}
```

Ensure these imports exist in the test file (add any missing):

```go
import (
	// ... existing
	"encoding/base64"
	"net/http"
	"net/http/httptest"
)
```

(Check the current import block before adding — `net/http` and `net/http/httptest` are already imported; `encoding/base64` likely is not.)

- [ ] **Step 2: Write the failing test — flag off, `mcp_` bearer falls through**

Append:

```go
func TestAuthMiddleware_MCPTestToken_FlagOff_FallsThrough(t *testing.T) {
	cfg := &config.Config{
		MCPTestTokenEnabled: false,
		URL:                 "https://configured.signoz.cloud",
	}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	token := mcpTestTokenForTest(t, "https://tenant.signoz.cloud", "sk_xxx")
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// With flag off, the mcp_ branch is skipped. The token falls into the
	// legacy "raw API key" path (no OAuth, no JWT, no customURL), which sets
	// the bearer body as the api key and uses the configured URL.
	if !captured.called {
		t.Fatalf("next handler was not called; status=%d body=%q", rec.Code, rec.Body.String())
	}
	if captured.apiKey != token {
		t.Errorf("apiKey = %q, want %q (raw token, untouched by mcp_ branch)", captured.apiKey, token)
	}
	if captured.signozURL != "https://configured.signoz.cloud" {
		t.Errorf("signozURL = %q, want configured URL", captured.signozURL)
	}
}
```

- [ ] **Step 3: Run it — should PASS immediately because the branch is guarded by the flag**

Run: `go test ./internal/mcp-server/... -run TestAuthMiddleware_MCPTestToken_FlagOff_FallsThrough -v`
Expected: PASS. (This confirms the flag-off guard works.)

- [ ] **Step 4: Commit**

```bash
git add internal/mcp-server/server_test.go
git commit -m "test(mcp-server): mcp_ token ignored when flag is off"
```

---

## Task 7: Integration test — flag on, valid `mcp_` bearer populates context

**Files:**
- Modify: `internal/mcp-server/server_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestAuthMiddleware_MCPTestToken_FlagOn_ValidToken(t *testing.T) {
	cfg := &config.Config{MCPTestTokenEnabled: true}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	token := mcpTestTokenForTest(t, "https://tenant.signoz.cloud", "sk_xxx")
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if !captured.called {
		t.Fatal("next handler was not called")
	}
	if captured.apiKey != "sk_xxx" {
		t.Errorf("apiKey = %q, want sk_xxx", captured.apiKey)
	}
	if captured.authHeader != "SIGNOZ-API-KEY" {
		t.Errorf("authHeader = %q, want SIGNOZ-API-KEY", captured.authHeader)
	}
	if captured.signozURL != "https://tenant.signoz.cloud" {
		t.Errorf("signozURL = %q, want https://tenant.signoz.cloud", captured.signozURL)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/mcp-server/... -run TestAuthMiddleware_MCPTestToken_FlagOn_ValidToken -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp-server/server_test.go
git commit -m "test(mcp-server): mcp_ token populates context when flag is on"
```

---

## Task 8: Integration test — token URL wins over `X-SigNoz-URL` header

**Files:**
- Modify: `internal/mcp-server/server_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestAuthMiddleware_MCPTestToken_FlagOn_TokenWinsOverHeader(t *testing.T) {
	cfg := &config.Config{MCPTestTokenEnabled: true}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	token := mcpTestTokenForTest(t, "https://tenant.signoz.cloud", "sk_xxx")
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-SigNoz-URL", "https://conflicting.signoz.cloud")
	req.Header.Set("SIGNOZ-API-KEY", "conflicting_key")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if captured.signozURL != "https://tenant.signoz.cloud" {
		t.Errorf("signozURL = %q, want token's URL https://tenant.signoz.cloud", captured.signozURL)
	}
	if captured.apiKey != "sk_xxx" {
		t.Errorf("apiKey = %q, want token's key sk_xxx", captured.apiKey)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/mcp-server/... -run TestAuthMiddleware_MCPTestToken_FlagOn_TokenWinsOverHeader -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp-server/server_test.go
git commit -m "test(mcp-server): mcp_ token URL takes precedence over headers"
```

---

## Task 9: Integration test — malformed `mcp_` token returns 401

**Files:**
- Modify: `internal/mcp-server/server_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestAuthMiddleware_MCPTestToken_FlagOn_MalformedReturns401(t *testing.T) {
	cfg := &config.Config{MCPTestTokenEnabled: true}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer mcp_!!!not-base64!!!")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %q", rec.Code, rec.Body.String())
	}
	if captured.called {
		t.Error("next handler should NOT be called on malformed mcp_ token")
	}
	if !strings.Contains(rec.Body.String(), "bad base64") {
		t.Errorf("body = %q, want 'bad base64' error", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/mcp-server/... -run TestAuthMiddleware_MCPTestToken_FlagOn_MalformedReturns401 -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp-server/server_test.go
git commit -m "test(mcp-server): malformed mcp_ token rejected with 401"
```

---

## Task 10: Integration test — non-`mcp_` bearer still works with flag on

**Files:**
- Modify: `internal/mcp-server/server_test.go`

- [ ] **Step 1: Write the test**

Append:

```go
func TestAuthMiddleware_MCPTestToken_FlagOn_NonMCPBearerUnaffected(t *testing.T) {
	cfg := &config.Config{
		MCPTestTokenEnabled: true,
		URL:                 "https://configured.signoz.cloud",
	}
	m := newTestMCPServerForAuth(t, cfg)

	captured := &capturedCtx{}
	handler := m.authMiddleware(captureNext(captured))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer plain_pat_token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if captured.apiKey != "plain_pat_token" {
		t.Errorf("apiKey = %q, want plain_pat_token (legacy path)", captured.apiKey)
	}
	if captured.signozURL != "https://configured.signoz.cloud" {
		t.Errorf("signozURL = %q, want configured URL", captured.signozURL)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/mcp-server/... -run TestAuthMiddleware_MCPTestToken_FlagOn_NonMCPBearerUnaffected -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp-server/server_test.go
git commit -m "test(mcp-server): non-mcp_ bearers unaffected when flag is on"
```

---

## Task 11: Full test suite + end-to-end smoke test

**Files:** (none modified)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: all tests PASS.

- [ ] **Step 2: Run `go vet`**

Run: `go vet ./...`
Expected: no issues.

- [ ] **Step 3: End-to-end smoke test**

Build and start the server with only the test flag set (no URL, no API key):

```bash
go build -o /tmp/signoz-mcp-server ./...
MCP_TEST_TOKEN_ENABLED=true TRANSPORT_MODE=http MCP_SERVER_PORT=8765 /tmp/signoz-mcp-server &
SERVER_PID=$!
sleep 1
```

Expected: the process starts and the logs contain `MCP_TEST_TOKEN_ENABLED is on — accepting unsigned mcp_ tokens; do not use in production`.

Forge a token pointing at a real SigNoz tenant you can test against (substitute real values):

```bash
PAYLOAD='{"headers":{"X-SigNoz-URL":"https://YOUR_TENANT.signoz.cloud","KEY":"YOUR_REAL_PAT"}}'
TOKEN="mcp_$(printf '%s' "$PAYLOAD" | base64 | tr -d '=' | tr '/+' '_-')"
echo "$TOKEN"
```

Hit the healthz endpoint (auth not required):

```bash
curl -s http://localhost:8765/healthz
# expected: ok
```

Hit an MCP endpoint that exercises auth — any request to `/mcp` will route through `authMiddleware`. A raw GET will fail for MCP protocol reasons, but the auth layer should accept the token first. Send a malformed token to confirm the rejection path:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer mcp_!!!not-base64!!!" \
  http://localhost:8765/mcp
# expected: 401
```

Send the valid token and confirm the server does NOT return 401:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8765/mcp
# expected: non-401 (the MCP handler may reject the request shape, but auth passed)
```

Stop the server:

```bash
kill $SERVER_PID
```

- [ ] **Step 4: Tag the feature commit (no commit needed — this task is verification only)**

No code changes in this task. If all checks pass, the feature is complete.

---

## Plan Self-Review

**Spec coverage:**
- Token format & parsing → Task 2 (parser) + Task 3 (all edge cases).
- `MCP_TEST_TOKEN_ENABLED` env var → Task 1.
- Middleware integration (first branch, early return) → Task 4.
- Token-wins precedence → Task 8.
- Auth header selection (`SIGNOZ-API-KEY`) → Task 7 assertion + Task 4 code.
- Client cache untouched → no task needed (no code change there).
- Behavior when disabled → Task 6.
- `ParseMCPTestToken` signature and location → Task 2.
- Config changes → Task 1.
- Logging (success debug, failure warn, startup warn) → Task 4 (success/failure logs), Task 5 (startup warn).
- Error messages → parser error strings in Task 2, asserted in Task 3 and Task 9.
- Unit tests for parser → Tasks 2 and 3.
- Integration tests for middleware → Tasks 6-10.
- End-to-end verification → Task 11.

No gaps.

**Placeholder scan:** No TBDs, no "add error handling", no "similar to task N". Every test case has the actual code. Every edit site has the exact before/after text or insertion point.

**Type consistency:** `ParseMCPTestToken(authHeader string) (signozURL, apiKey string, err error)` — same signature used in Task 2 (definition) and Task 4 (call site). `MCPTestTokenPrefix` constant defined in Task 2, referenced in Task 4. `MCPTestTokenEnabled` field name consistent across Tasks 1, 4, 5, 6, 7, 8, 10. Context helpers (`util.SetAPIKey`, `util.SetAuthHeader`, `util.SetSigNozURL`) match the existing signatures in `pkg/util/context.go`.

**One note on Task 6:** The test passes immediately without new code because the flag-off behavior is produced by the *guard* added in Task 4. This is intentional — it locks in that the guard keeps working, not that new code needs to be added. If Task 4's guard is ever removed or broken, this test will catch it.
