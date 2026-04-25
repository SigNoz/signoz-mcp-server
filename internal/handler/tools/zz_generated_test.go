package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	gentypes "github.com/SigNoz/signoz-mcp-server/pkg/types/gentools"
)

// Round-trip test for a simple generated GET handler. Verifies that the
// generator correctly wires path, query params, and method into client.Do.
func TestGenerated_GET_ListChannels(t *testing.T) {
	var gotMethod, gotPath string
	mock := &signozclient.MockClient{
		DoFn: func(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
			gotMethod = method
			gotPath = path
			return json.RawMessage(`{"status":"success","data":[]}`), nil
		},
	}
	h := newTestHandler(mock)

	res, err := h.genHandleListChannels(testCtx(), makeToolRequest("signoz_list_channels", nil), gentypes.ListChannelsInput{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %v", res)
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v1/channels" {
		t.Errorf("path = %q, want /api/v1/channels", gotPath)
	}
}

// Round-trip test for a generated POST with a body. Verifies the handler
// marshals the Body map into JSON bytes before calling client.Do.
func TestGenerated_POST_CreateChannel(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	mock := &signozclient.MockClient{
		DoFn: func(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
			gotMethod = method
			gotPath = path
			gotBody = body
			return json.RawMessage(`{"status":"success"}`), nil
		},
	}
	h := newTestHandler(mock)

	in := gentypes.CreateChannelInput{
		Body: json.RawMessage(`{"name":"slack-ops"}`),
	}
	res, err := h.genHandleCreateChannel(testCtx(), makeToolRequest("signoz_create_channel", nil), in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %v", res)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/channels" {
		t.Errorf("path = %q, want /api/v1/channels", gotPath)
	}
	// Body must be valid JSON carrying the name we set — exercises the
	// marshal of a generated component type.
	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, string(gotBody))
	}
	if decoded["name"] != "slack-ops" {
		t.Errorf("body name mismatch: %v", decoded)
	}
}

// Round-trip test for a generated DELETE with a required path parameter.
// Verifies that the handler substitutes the path placeholder and URL-encodes
// the ID.
func TestGenerated_DELETE_DeleteChannelByID(t *testing.T) {
	var gotMethod, gotPath string
	mock := &signozclient.MockClient{
		DoFn: func(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
			gotMethod = method
			gotPath = path
			return json.RawMessage(`{}`), nil
		},
	}
	h := newTestHandler(mock)

	in := gentypes.DeleteChannelByIDInput{Id: "abc/def"}
	res, err := h.genHandleDeleteChannelByID(testCtx(), makeToolRequest("signoz_delete_channel_by_id", nil), in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %v", res)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	// The slash in "abc/def" must be URL-encoded so the templated path stays
	// shaped like /api/v1/channels/<id>.
	if !strings.HasPrefix(gotPath, "/api/v1/channels/") || strings.Count(gotPath, "/") != 4 {
		t.Errorf("path = %q; expected a single path segment for the id", gotPath)
	}
	if !strings.Contains(gotPath, "abc%2Fdef") {
		t.Errorf("path = %q; expected id slash to be URL-encoded", gotPath)
	}
}

// TestGenerated_RawBody_PassesThroughUnchanged asserts that the body passed
// in via json.RawMessage reaches client.Do byte-for-byte — the handler does
// not re-serialize it. This is the core property of the direct-translation
// refactor: bodies are opaque JSON that the LLM authors via the schema
// gentools.Schemas["..."], not a Go struct the handler re-marshals.
func TestGenerated_RawBody_PassesThroughUnchanged(t *testing.T) {
	var gotBody []byte
	mock := &signozclient.MockClient{
		DoFn: func(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
			gotBody = body
			return json.RawMessage(`{}`), nil
		},
	}
	h := newTestHandler(mock)

	raw := json.RawMessage(`{"name":"pager","type":"slack","data":"x","orgId":"o"}`)
	in := gentypes.CreateChannelInput{Body: raw}
	if _, err := h.genHandleCreateChannel(testCtx(), makeToolRequest("signoz_create_channel", nil), in); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if string(gotBody) != string(raw) {
		t.Errorf("body bytes changed:\n got: %s\nwant: %s", gotBody, raw)
	}
}

// Missing-required-path-param test: a required path parameter left as the
// zero value must cause the handler to return an error result rather than
// making an HTTP call with an unfilled template.
func TestGenerated_MissingRequiredPath(t *testing.T) {
	called := false
	mock := &signozclient.MockClient{
		DoFn: func(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`{}`), nil
		},
	}
	h := newTestHandler(mock)
	res, err := h.genHandleDeleteChannelByID(testCtx(), makeToolRequest("signoz_delete_channel_by_id", nil), gentypes.DeleteChannelByIDInput{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected tool result to be an error when id is empty")
	}
	if called {
		t.Error("client.Do should not be called when required path param is empty")
	}
}
