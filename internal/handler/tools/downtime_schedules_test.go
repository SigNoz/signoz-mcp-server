package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/internal/client"
)

const testScheduleID = "0193e3a1-1d7a-7c3b-9f2a-4b8c1d2e3f40"

func wrappedSchedules() json.RawMessage {
	return json.RawMessage(`{"status":"success","data":[
		{"id":"0193e3a1-1d7a-7c3b-9f2a-4b8c1d2e3f40","name":"DB upgrade window","status":"upcoming","kind":"recurring"},
		{"id":"0193e3a1-1d7a-7c3b-9f2a-4b8c1d2e3f41","name":"Cache flush","status":"expired","kind":"fixed"}
	]}`)
}

func decodeListPayload(t *testing.T, payload string) (data []any, pagination map[string]any) {
	t.Helper()
	var parsed struct {
		Data       []any          `json:"data"`
		Pagination map[string]any `json:"pagination"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("failed to parse list payload %q: %v", payload, err)
	}
	return parsed.Data, parsed.Pagination
}

func TestListDowntimeSchedules_Success(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
			return wrappedSchedules(), nil
		},
	})

	result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", textContent(t, result))
	}
	data, pagination := decodeListPayload(t, textContent(t, result))
	if len(data) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(data))
	}
	first, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("first item is not an object: %T", data[0])
	}
	if first["name"] != "DB upgrade window" {
		t.Errorf("name = %v, want DB upgrade window", first["name"])
	}
	// Unknown/server-derived fields must survive the passthrough.
	if first["status"] != "upcoming" || first["kind"] != "recurring" {
		t.Errorf("status/kind not preserved: %v/%v", first["status"], first["kind"])
	}
	if pagination["total"] != float64(2) {
		t.Errorf("pagination.total = %v, want 2", pagination["total"])
	}
}

func TestListDowntimeSchedules_Paginates(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
			return wrappedSchedules(), nil
		},
	})

	result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{
		"limit":  "1",
		"offset": "1",
	}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	data, pagination := decodeListPayload(t, textContent(t, result))
	if len(data) != 1 {
		t.Fatalf("expected 1 schedule on page, got %d", len(data))
	}
	item := data[0].(map[string]any)
	if item["name"] != "Cache flush" {
		t.Errorf("paged item = %v, want Cache flush", item["name"])
	}
	if pagination["total"] != float64(2) {
		t.Errorf("pagination.total = %v, want 2", pagination["total"])
	}
	if pagination["hasMore"] != false {
		t.Errorf("pagination.hasMore = %v, want false", pagination["hasMore"])
	}
}

func TestListDowntimeSchedules_TriStateForwarding(t *testing.T) {
	cases := []struct {
		name          string
		args          map[string]any
		wantActive    *bool
		wantRecurring *bool
	}{
		{"absent", map[string]any{}, nil, nil},
		{"present true", map[string]any{"active": true, "recurring": true}, boolPtr(true), boolPtr(true)},
		{"present false", map[string]any{"active": false, "recurring": false}, boolPtr(false), boolPtr(false)},
		{"string true", map[string]any{"active": "true"}, boolPtr(true), nil},
		{"mixed", map[string]any{"active": true}, boolPtr(true), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotActive, gotRecurring *bool
			h := newTestHandler(&client.MockClient{
				ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
					gotActive, gotRecurring = active, recurring
					return json.RawMessage(`{"status":"success","data":[]}`), nil
				},
			})

			result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", tc.args))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected error result: %s", textContent(t, result))
			}
			assertBoolPtr(t, "active", gotActive, tc.wantActive)
			assertBoolPtr(t, "recurring", gotRecurring, tc.wantRecurring)
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func assertBoolPtr(t *testing.T, name string, got, want *bool) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %v, want nil pointer (param must be omitted upstream)", name, *got)
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %v", name, *want)
	case want != nil && got != nil && *want != *got:
		t.Errorf("%s = %v, want %v", name, *got, *want)
	}
}

func TestListDowntimeSchedules_InvalidTriStateValue(t *testing.T) {
	for _, key := range []string{"active", "recurring"} {
		t.Run(key, func(t *testing.T) {
			called := false
			h := newTestHandler(&client.MockClient{
				ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
					called = true
					return json.RawMessage(`{"data":[]}`), nil
				},
			})

			result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{key: "maybe"}))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected a validation error result, got: %s", textContent(t, result))
			}
			if !strings.Contains(textContent(t, result), "Parameter validation failed") {
				t.Errorf("unexpected message: %s", textContent(t, result))
			}
			if called {
				t.Error("upstream must not be called for an invalid tri-state value")
			}
		})
	}
}

// TestListDowntimeSchedules_LegacyUnwrappedEnvelope pins the defensive parse:
// older SigNoz builds served this path from the legacy ee/query-service router
// without the {"status":"success","data":...} render envelope.
func TestListDowntimeSchedules_LegacyUnwrappedEnvelope(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
			return json.RawMessage(`[{"id":"s1","name":"Legacy window"}]`), nil
		},
	})

	result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", textContent(t, result))
	}
	data, _ := decodeListPayload(t, textContent(t, result))
	if len(data) != 1 {
		t.Fatalf("expected 1 schedule from the unwrapped body, got %d", len(data))
	}
	if data[0].(map[string]any)["name"] != "Legacy window" {
		t.Errorf("unexpected item: %v", data[0])
	}
}

// A shape with no recognizable data must fail open to an empty list rather than
// erroring, while still being distinguishable from a genuinely empty workspace
// via the WARN log emitted by decodeDowntimeScheduleList.
func TestListDowntimeSchedules_UnknownShapeFailsOpen(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"success","schedules":[{"id":"s1"}]}`), nil
		},
	})

	result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected fail-open success result, got error: %s", textContent(t, result))
	}
	data, _ := decodeListPayload(t, textContent(t, result))
	if len(data) != 0 {
		t.Fatalf("expected an empty list for an unknown shape, got %d items", len(data))
	}
}

func TestListDowntimeSchedules_UnparseableBody(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
			return json.RawMessage(`not json at all`), nil
		},
	})

	result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result for an unparseable body, got: %s", textContent(t, result))
	}
	if !strings.Contains(textContent(t, result), "failed to parse downtime schedules response") {
		t.Errorf("unexpected message: %s", textContent(t, result))
	}
}

func TestListDowntimeSchedules_UpstreamError(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		ListDowntimeSchedulesFn: func(ctx context.Context, active, recurring *bool) (json.RawMessage, error) {
			return nil, errors.New("boom")
		},
	})

	result, err := h.handleListDowntimeSchedules(testCtx(), makeToolRequest("signoz_list_downtime_schedules", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result for an upstream failure")
	}
	if !strings.Contains(textContent(t, result), upstreamErrorPrefix) {
		t.Errorf("unexpected message: %s", textContent(t, result))
	}
}

func TestGetDowntimeSchedule_Success(t *testing.T) {
	var gotID string
	h := newTestHandler(&client.MockClient{
		GetDowntimeScheduleFn: func(ctx context.Context, id string) (json.RawMessage, error) {
			gotID = id
			return json.RawMessage(`{"status":"success","data":{"id":"` + testScheduleID + `","name":"DB upgrade window"}}`), nil
		},
	})

	result, err := h.handleGetDowntimeSchedule(testCtx(), makeToolRequest("signoz_get_downtime_schedule", map[string]any{"id": testScheduleID}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", textContent(t, result))
	}
	if gotID != testScheduleID {
		t.Errorf("upstream id = %q, want %q", gotID, testScheduleID)
	}
	if !strings.Contains(textContent(t, result), "DB upgrade window") {
		t.Errorf("unexpected payload: %s", textContent(t, result))
	}
}

func TestGetDowntimeSchedule_MissingID(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	result, err := h.handleGetDowntimeSchedule(testCtx(), makeToolRequest("signoz_get_downtime_schedule", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a validation error result for a missing id")
	}
	if !strings.Contains(textContent(t, result), `"id" is required`) {
		t.Errorf("unexpected message: %s", textContent(t, result))
	}
}

func TestDowntimeSchedule_NonUUIDv7ID(t *testing.T) {
	cases := []struct {
		tool   string
		invoke func(*Handler, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{"signoz_get_downtime_schedule", (*Handler).handleGetDowntimeScheduleForTest},
		{"signoz_delete_downtime_schedule", (*Handler).handleDeleteDowntimeScheduleForTest},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			upstreamCalled := false
			h := newTestHandler(&client.MockClient{
				GetDowntimeScheduleFn: func(ctx context.Context, id string) (json.RawMessage, error) {
					upstreamCalled = true
					return json.RawMessage(`{}`), nil
				},
				DeleteDowntimeScheduleFn: func(ctx context.Context, id string) error {
					upstreamCalled = true
					return nil
				},
			})

			result, err := tc.invoke(h, makeToolRequest(tc.tool, map[string]any{"id": "not-a-uuid"}))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected a validation error result, got: %s", textContent(t, result))
			}
			if !strings.Contains(textContent(t, result), "is not a UUIDv7") {
				t.Errorf("unexpected message: %s", textContent(t, result))
			}
			if upstreamCalled {
				t.Error("upstream must not be called for a non-UUIDv7 id")
			}
		})
	}
}

func (h *Handler) handleGetDowntimeScheduleForTest(req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return h.handleGetDowntimeSchedule(testCtx(), req)
}

func (h *Handler) handleDeleteDowntimeScheduleForTest(req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return h.handleDeleteDowntimeSchedule(testCtx(), req)
}

func TestCreateDowntimeSchedule_StripsSearchContext(t *testing.T) {
	var captured []byte
	h := newTestHandler(&client.MockClient{
		CreateDowntimeScheduleFn: func(ctx context.Context, scheduleJSON []byte) (json.RawMessage, error) {
			captured = scheduleJSON
			return json.RawMessage(`{"status":"success","data":{"id":"` + testScheduleID + `"}}`), nil
		},
	})

	args := map[string]any{
		"name":          "DB upgrade window",
		"searchContext": "silence checkout alerts during the DB upgrade tonight",
		"status":        "active",
		"kind":          "fixed",
		"id":            "should-not-be-sent",
		"createdBy":     "someone",
		"schedule": map[string]any{
			"timezone":  "America/New_York",
			"startTime": "2026-08-04T22:00:00-04:00",
			"endTime":   "2026-08-05T02:00:00-04:00",
		},
		"scope": "service_name == 'checkout'",
	}

	result, err := h.handleCreateDowntimeSchedule(testCtx(), makeToolRequest("signoz_create_downtime_schedule", args))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", textContent(t, result))
	}

	var body map[string]any
	if err := json.Unmarshal(captured, &body); err != nil {
		t.Fatalf("captured payload is not JSON: %v (%s)", err, captured)
	}
	for _, forbidden := range []string{"searchContext", "status", "kind", "id", "createdBy"} {
		if _, present := body[forbidden]; present {
			t.Errorf("%q must be stripped from the forwarded body, got %v", forbidden, body[forbidden])
		}
	}
	if body["name"] != "DB upgrade window" {
		t.Errorf("name = %v, want DB upgrade window", body["name"])
	}
	if body["scope"] != "service_name == 'checkout'" {
		t.Errorf("scope = %v, want the expr-lang expression forwarded verbatim", body["scope"])
	}
	if _, ok := body["schedule"].(map[string]any); !ok {
		t.Errorf("schedule missing from forwarded body: %v", body["schedule"])
	}
}

func TestCreateDowntimeSchedule_EmptyPayload(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	result, err := h.handleCreateDowntimeSchedule(testCtx(), makeToolRequest("signoz_create_downtime_schedule", map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a validation error result for an empty payload")
	}
	if !strings.Contains(textContent(t, result), "configuration object is empty") {
		t.Errorf("unexpected message: %s", textContent(t, result))
	}
}

func TestCreateDowntimeSchedule_NonObjectPayload(t *testing.T) {
	h := newTestHandler(&client.MockClient{})

	req := makeToolRequest("signoz_create_downtime_schedule", nil)
	req.Params.Arguments = []any{"nope"}
	result, err := h.handleCreateDowntimeSchedule(testCtx(), req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a validation error result for a non-object payload")
	}
}

func TestCreateDowntimeSchedule_RequiresNameAndSchedule(t *testing.T) {
	cases := []struct {
		name     string
		args     map[string]any
		wantWord string
	}{
		{"missing name", map[string]any{"schedule": map[string]any{"startTime": "a", "endTime": "b"}}, `"name"`},
		{"missing schedule", map[string]any{"name": "window"}, `"schedule"`},
		{"schedule not an object", map[string]any{"name": "window", "schedule": "tonight"}, `"schedule"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := newTestHandler(&client.MockClient{
				CreateDowntimeScheduleFn: func(ctx context.Context, scheduleJSON []byte) (json.RawMessage, error) {
					called = true
					return json.RawMessage(`{}`), nil
				},
			})

			result, err := h.handleCreateDowntimeSchedule(testCtx(), makeToolRequest("signoz_create_downtime_schedule", tc.args))
			if err != nil {
				t.Fatalf("unexpected transport error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected a validation error result, got: %s", textContent(t, result))
			}
			if !strings.Contains(textContent(t, result), tc.wantWord) {
				t.Errorf("message %q does not mention %s", textContent(t, result), tc.wantWord)
			}
			if called {
				t.Error("upstream must not be called when local validation fails")
			}
		})
	}
}

func TestDeleteDowntimeSchedule_Success(t *testing.T) {
	var gotID string
	h := newTestHandler(&client.MockClient{
		DeleteDowntimeScheduleFn: func(ctx context.Context, id string) error {
			gotID = id
			return nil
		},
	})

	result, err := h.handleDeleteDowntimeSchedule(testCtx(), makeToolRequest("signoz_delete_downtime_schedule", map[string]any{"id": testScheduleID}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", textContent(t, result))
	}
	if gotID != testScheduleID {
		t.Errorf("upstream id = %q, want %q", gotID, testScheduleID)
	}
	want := `{"status":"success","id":"` + testScheduleID + `"}`
	if got := textContent(t, result); got != want {
		t.Errorf("payload = %s, want %s", got, want)
	}
}

func TestDeleteDowntimeSchedule_UpstreamError(t *testing.T) {
	h := newTestHandler(&client.MockClient{
		DeleteDowntimeScheduleFn: func(ctx context.Context, id string) error {
			return errors.New("boom")
		},
	})

	result, err := h.handleDeleteDowntimeSchedule(testCtx(), makeToolRequest("signoz_delete_downtime_schedule", map[string]any{"id": testScheduleID}))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result for an upstream failure")
	}
	if !strings.Contains(textContent(t, result), upstreamErrorPrefix) {
		t.Errorf("unexpected message: %s", textContent(t, result))
	}
}
