//go:build e2e

// Package tools — Family B (#364) live end-to-end verification.
//
// This file exercises the error/validation flows converged onto the shared
// helpers in errs.go (validationError, requireStringArg, notAJSONObjectError,
// notAConfigObjectError, upstreamError) against a REAL SigNoz instance, so that
// upstream contract drift (a renamed field, a changed error envelope) fails a
// test rather than a user — per CLAUDE.md's cross-contract testing mandate.
//
// CREDENTIAL HYGIENE: this test reads the instance URL and a session token
// ONLY from the environment and SKIPS when either is absent. It NEVER hardcodes
// a secret, NEVER logs the token, and NEVER persists it. Run it like:
//
//	SIGNOZ_E2E_URL="https://<your-instance>" \
//	SIGNOZ_E2E_TOKEN="<your-session-jwt-or-api-key>" \
//	go test -tags=e2e -run TestE2EFamilyB ./internal/handler/tools/...
//
// By default SIGNOZ_E2E_AUTH_HEADER is "Authorization" and the token is sent as
// a "Bearer <token>" value (session JWT). Set SIGNOZ_E2E_AUTH_HEADER to
// "SIGNOZ-API-KEY" and SIGNOZ_E2E_RAW_TOKEN=1 to use a raw API key instead.
package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// e2eHandler builds a Handler backed by a real SigNoz client, or skips the test
// when the required env vars are absent. The token is read from the environment
// only and never logged.
func e2eHandler(t *testing.T) (*Handler, context.Context) {
	t.Helper()

	baseURL := strings.TrimRight(os.Getenv("SIGNOZ_E2E_URL"), "/")
	token := os.Getenv("SIGNOZ_E2E_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("SIGNOZ_E2E_URL and SIGNOZ_E2E_TOKEN must be set to run Family B e2e tests")
	}

	authHeader := os.Getenv("SIGNOZ_E2E_AUTH_HEADER")
	if authHeader == "" {
		authHeader = "Authorization"
	}

	// A session JWT is sent as "Bearer <token>" on the Authorization header.
	// A raw API key (SIGNOZ_E2E_RAW_TOKEN=1) is sent verbatim.
	apiKey := token
	if os.Getenv("SIGNOZ_E2E_RAW_TOKEN") != "1" && strings.EqualFold(authHeader, "Authorization") {
		apiKey = "Bearer " + token
	}

	client := signozclient.NewClient(logpkg.New("error"), baseURL, apiKey, authHeader, nil)
	h := &Handler{logger: logpkg.New("error"), clientOverride: client}
	return h, context.Background()
}

// errResultText asserts the result is an error and returns its first text block.
func errResultText(t *testing.T, body string, isErr bool) string {
	t.Helper()
	if !isErr {
		t.Fatalf("expected an error result, got success: %s", body)
	}
	return body
}

// --- Pure validation flows (no upstream call; the helper rejects before the
// client is ever reached). These pin the canonical capital-P strings. ---

func TestE2EFamilyB_ValidationStrings(t *testing.T) {
	h, ctx := e2eHandler(t)

	cases := []struct {
		name     string
		run      func() (string, bool)
		wantSub  string
		wantNoIn string // substring that must NOT appear
	}{
		{
			// K5: id is canonical; ruleId is a legacy alias read via readResourceID.
			// A missing id/ruleId yields the coded "id is required" message.
			name: "get_alert missing id/ruleId -> id required",
			run: func() (string, bool) {
				r, _ := h.handleGetAlert(ctx, makeToolRequest("signoz_get_alert", map[string]any{}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "id" is required (the legacy parameter name "ruleId" is also accepted)`,
		},
		{
			// readResourceID treats a non-string legacy value as absent, so a
			// wrong-typed ruleId falls through to the same "id is required" error.
			name: "get_alert wrong-typed ruleId -> treated as absent, id required",
			run: func() (string, bool) {
				r, _ := h.handleGetAlert(ctx, makeToolRequest("signoz_get_alert", map[string]any{"ruleId": 12345}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "id" is required (the legacy parameter name "ruleId" is also accepted)`,
		},
		{
			name: "get_dashboard missing id/uuid -> id required",
			run: func() (string, bool) {
				r, _ := h.handleGetDashboard(ctx, makeToolRequest("signoz_get_dashboard", map[string]any{}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "id" is required (the legacy parameter name "uuid" is also accepted)`,
		},
		{
			name: "get_dashboard wrong-typed uuid -> treated as absent, id required",
			run: func() (string, bool) {
				r, _ := h.handleGetDashboard(ctx, makeToolRequest("signoz_get_dashboard", map[string]any{"uuid": true}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "id" is required (the legacy parameter name "uuid" is also accepted)`,
		},
		{
			name: "get_view missing id/viewId -> id required",
			run: func() (string, bool) {
				r, _ := h.handleGetView(ctx, makeToolRequest("signoz_get_view", map[string]any{}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "id" is required (the legacy parameter name "viewId" is also accepted)`,
		},
		{
			name: "get_notification_channel missing id -> cannot be empty",
			run: func() (string, bool) {
				r, _ := h.handleGetNotificationChannel(ctx, makeToolRequest("signoz_get_notification_channel", map[string]any{}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "id" cannot be empty`,
		},
		{
			name: "execute_builder_query missing query -> must be a JSON object",
			run: func() (string, bool) {
				r, _ := h.handleExecuteBuilderQuery(ctx, makeToolRequest("signoz_execute_builder_query", map[string]any{}))
				return textContent(t, r), r.IsError
			},
			wantSub: `Parameter validation failed: "query" must be a JSON object`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, isErr := tc.run()
			errResultText(t, body, isErr)
			if !strings.Contains(body, tc.wantSub) {
				t.Fatalf("error text = %q, want substring %q", body, tc.wantSub)
			}
			if !strings.HasPrefix(body, "Parameter validation failed:") {
				t.Fatalf("error text = %q, want canonical capital-P prefix", body)
			}
		})
	}
}

// N23: get_trace_details with a malformed start/end must be reframed as a
// user/parameter error — NOT the old "Internal error:" prefix.
func TestE2EFamilyB_TraceTimestampIsParameterError(t *testing.T) {
	h, ctx := e2eHandler(t)
	r, _ := h.handleGetTraceDetails(ctx, makeToolRequest("signoz_get_trace_details", map[string]any{
		"traceId": "deadbeefdeadbeefdeadbeefdeadbeef",
		"start":   "not-a-timestamp",
		"end":     "also-bad",
	}))
	body := textContent(t, r)
	if !r.IsError {
		t.Fatalf("expected an error result, got success: %s", body)
	}
	if strings.Contains(body, "Internal error:") {
		t.Fatalf("trace timestamp parse must not be labeled Internal error; got %q", body)
	}
	if !strings.HasPrefix(body, "Parameter validation failed:") {
		t.Fatalf("trace timestamp parse should be a parameter error; got %q", body)
	}
}

// --- Upstream-error flow: a well-formed-but-nonexistent id reaches the backend,
// which rejects it; the result must carry the uniform "SigNoz API error:"
// prefix. This is the flow that detects upstream contract drift. ---

func TestE2EFamilyB_UpstreamErrorPrefix(t *testing.T) {
	h, ctx := e2eHandler(t)

	// A syntactically-valid UUIDv7 that (almost certainly) does not exist.
	const ghostRuleID = "01900000-0000-7000-8000-000000000000"

	r, transportErr := h.handleGetAlert(ctx, makeToolRequest("signoz_get_alert", map[string]any{"ruleId": ghostRuleID}))
	if transportErr != nil {
		t.Fatalf("unexpected transport error: %v", transportErr)
	}
	body := textContent(t, r)
	if !r.IsError {
		// Some deployments return an empty 200 rather than a 4xx for an unknown
		// rule. That's a backend behavior, not a Family B regression — log and
		// skip the prefix assertion rather than fail spuriously.
		t.Skipf("backend returned success for a nonexistent ruleId (no upstream error to assert); body=%s", body)
	}
	if !strings.HasPrefix(body, "SigNoz API error:") {
		t.Fatalf("upstream error should carry the uniform prefix; got %q", body)
	}
}

// --- Happy-path sanity: confirm the refactor did not break successful
// get/list calls. Uses read-only list endpoints so nothing is created. ---

func TestE2EFamilyB_HappyPathStillSucceeds(t *testing.T) {
	h, ctx := e2eHandler(t)

	cases := []struct {
		name string
		args map[string]any
		run  func(map[string]any) (isErr bool, body string)
	}{
		{
			name: "list_dashboards",
			args: map[string]any{},
			run: func(a map[string]any) (bool, string) {
				r, err := h.handleListDashboards(ctx, makeToolRequest("signoz_list_dashboards", a))
				if err != nil {
					t.Fatalf("transport error: %v", err)
				}
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "list_alert_rules",
			args: map[string]any{},
			run: func(a map[string]any) (bool, string) {
				r, err := h.handleListAlertRules(ctx, makeToolRequest("signoz_list_alert_rules", a))
				if err != nil {
					t.Fatalf("transport error: %v", err)
				}
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "list_views_traces",
			args: map[string]any{"sourcePage": "traces"},
			run: func(a map[string]any) (bool, string) {
				r, err := h.handleListViews(ctx, makeToolRequest("signoz_list_views", a))
				if err != nil {
					t.Fatalf("transport error: %v", err)
				}
				return r.IsError, textContent(t, r)
			},
		},
		{
			name: "list_notification_channels",
			args: map[string]any{},
			run: func(a map[string]any) (bool, string) {
				r, err := h.handleListNotificationChannels(ctx, makeToolRequest("signoz_list_notification_channels", a))
				if err != nil {
					t.Fatalf("transport error: %v", err)
				}
				return r.IsError, textContent(t, r)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isErr, body := tc.run(tc.args)
			if isErr {
				t.Fatalf("happy-path %s returned an error result: %s", tc.name, body)
			}
		})
	}
}
