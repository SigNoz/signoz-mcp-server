package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
)

// registeredToolProps registers every tool handler on a fresh MCP server and
// returns the input-schema `properties` map for the named tool, exactly as it
// is advertised to clients (post-normalization). This pins what the registered
// schema actually carries, not a hand-rebuilt copy of it.
func registeredToolProps(t *testing.T, toolName string) map[string]any {
	t.Helper()

	h := newTestHandler(&signozclient.MockClient{})
	s := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(false))

	h.RegisterLogsHandlers(s)
	h.RegisterTracesHandlers(s)
	h.RegisterMetricsHandlers(s)
	h.RegisterTopMetricsHandlers(s)
	h.RegisterAlertsHandlers(s)
	h.RegisterFieldsHandlers(s)
	h.RegisterServiceHandlers(s)
	h.RegisterViewHandlers(s)
	h.RegisterDocsHandlers(s)

	tools := s.ListTools()
	st, ok := tools[toolName]
	if !ok {
		t.Fatalf("tool %q not registered", toolName)
	}

	b, err := json.Marshal(st.Tool)
	if err != nil {
		t.Fatalf("marshal tool %q: %v", toolName, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal tool %q JSON: %v", toolName, err)
	}
	inputSchema, ok := doc["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q inputSchema = %#v, want object", toolName, doc["inputSchema"])
	}
	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q inputSchema.properties = %#v, want object", toolName, inputSchema["properties"])
	}
	return props
}

// propEnum returns the sorted enum values declared on a property, or nil if the
// property carries no enum.
func propEnum(t *testing.T, props map[string]any, name string) []string {
	t.Helper()
	prop, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("property %q = %#v, want object", name, props[name])
	}
	rawEnum, ok := prop["enum"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rawEnum))
	for _, v := range rawEnum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("enum value %#v on %q is not a string", v, name)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestStableSetEnumsArePresent pins the hard schema enums on the STABLE /
// MCP-owned parameter sets (N15). These must carry an explicit JSON-Schema
// enum so clients/LLMs get a typed, closed set.
func TestStableSetEnumsArePresent(t *testing.T) {
	cases := []struct {
		tool string
		prop string
		want []string
	}{
		{"signoz_aggregate_logs", "requestType", []string{"scalar", "time_series"}},
		{"signoz_aggregate_traces", "requestType", []string{"scalar", "time_series"}},
		{"signoz_query_metrics", "requestType", []string{"scalar", "time_series"}},
		{"signoz_get_alert_history", "order", []string{"asc", "desc"}},
		{"signoz_get_alert_history", "state", []string{"firing", "inactive"}},
		{"signoz_get_field_keys", "signal", []string{"logs", "metrics", "traces"}},
		{"signoz_get_field_values", "signal", []string{"logs", "metrics", "traces"}},
		// sourcePage already carried an enum before this change; pin it so a
		// regression that drops it fails here too.
		{"signoz_create_view", "sourcePage", []string{"logs", "meter", "metrics", "traces"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.prop, func(t *testing.T) {
			props := registeredToolProps(t, tc.tool)
			got := propEnum(t, props, tc.prop)
			if len(got) == 0 {
				t.Fatalf("%s.%s has no enum; expected %v", tc.tool, tc.prop, tc.want)
			}
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s.%s enum = %v, want %v", tc.tool, tc.prop, got, tc.want)
			}
		})
	}
}

// TestEvolvingSetsAreFreeStrings asserts the backend-owned / evolving sets are
// NOT hard-enumed at the schema layer (N15 split): pinning them would drift
// out from under the backend the moment it adds a value. They stay documented
// free-strings, validated by the backend (or a soft handler check), and are
// covered instead by the drift test below.
func TestEvolvingSetsAreFreeStrings(t *testing.T) {
	cases := []struct {
		tool string
		prop string
	}{
		{"signoz_aggregate_logs", "aggregation"},
		{"signoz_aggregate_traces", "aggregation"},
		{"signoz_create_notification_channel", "type"},
	}
	// notification-channel type lives on a handler we also need registered.
	h := newTestHandler(&signozclient.MockClient{})
	s := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(false))
	h.RegisterLogsHandlers(s)
	h.RegisterTracesHandlers(s)
	h.RegisterNotificationChannelHandlers(s)
	registered := s.ListTools()

	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.prop, func(t *testing.T) {
			st, ok := registered[tc.tool]
			if !ok {
				t.Fatalf("tool %q not registered", tc.tool)
			}
			b, _ := json.Marshal(st.Tool)
			var doc map[string]any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			props := doc["inputSchema"].(map[string]any)["properties"].(map[string]any)
			prop, ok := props[tc.prop].(map[string]any)
			if !ok {
				t.Fatalf("property %q missing on %q", tc.prop, tc.tool)
			}
			if _, hasEnum := prop["enum"]; hasEnum {
				t.Fatalf("%s.%s carries a schema enum but is a backend-owned/evolving set; it must stay a free-string (see N15)", tc.tool, tc.prop)
			}
		})
	}
}

// TestAggregationDescriptionDriftGuard pins the in-code valid-aggregation set
// against the documented list in the param description. The aggregation
// operators are a backend-owned/evolving set kept as a free-string at the
// schema layer (TestEvolvingSetsAreFreeStrings), so nothing else guards that
// the value we advertise in the description matches the set we actually
// accept. If someone adds/removes an operator in validAggregations without
// updating allowedAggregations (or vice-versa), this fails — drift becomes a
// failing test, not a confused user (CLAUDE.md cross-contract mandate).
//
// TODO(live-backend): this pins the MCP-advertised set against itself. The
// authoritative set is the SigNoz query-builder backend's accepted aggregation
// operators. Add a periodic/integration check (guarded/skippable, see
// liveBackendDriftCheckSkipped) that diffs validAggregations against what a
// real instance accepts, so a backend-side addition is also surfaced.
func TestAggregationDescriptionDriftGuard(t *testing.T) {
	// Derive the documented set from allowedAggregations.
	documented := splitCSV(allowedAggregations)
	sort.Strings(documented)

	inCode := make([]string, 0, len(validAggregations))
	for k := range validAggregations {
		inCode = append(inCode, k)
	}
	sort.Strings(inCode)

	if !reflect.DeepEqual(documented, inCode) {
		t.Fatalf("aggregation set drift: description advertises %v but validAggregations accepts %v; keep allowedAggregations and validAggregations in sync", documented, inCode)
	}
}

// TestChannelTypeDriftGuard pins the in-code validChannelTypes set (the single
// source of truth) against the types each notification-channel tool actually
// advertises in its registered description. channel `type` is a backend-owned/
// evolving set kept as a free-string, so this is the only guard that the
// advertised list matches what we accept.
//
// The advertised set is DERIVED from the registered tool descriptions (the
// "SUPPORTED TYPES: ..." line) rather than hardcoded here — so a drift in
// either the description OR the validChannelTypes map fails the test. Both the
// create AND update channel tools are covered.
//
// TODO(live-backend): the authoritative set is what the SigNoz backend's
// notification-channel API accepts. Add a periodic/integration check
// (guarded/skippable) diffing validChannelTypes against a real instance.
func TestChannelTypeDriftGuard(t *testing.T) {
	h := newTestHandler(&signozclient.MockClient{})
	s := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(false))
	h.RegisterNotificationChannelHandlers(s)
	registered := s.ListTools()

	// Single in-code source of truth.
	inCode := make([]string, 0, len(validChannelTypes))
	for k := range validChannelTypes {
		inCode = append(inCode, k)
	}
	sort.Strings(inCode)

	for _, toolName := range []string{
		"signoz_create_notification_channel",
		"signoz_update_notification_channel",
	} {
		t.Run(toolName, func(t *testing.T) {
			st, ok := registered[toolName]
			if !ok {
				t.Fatalf("tool %q not registered", toolName)
			}
			advertised := supportedTypesFromDescription(t, toolName, st.Tool.Description)
			if !reflect.DeepEqual(advertised, inCode) {
				t.Fatalf("channel-type set drift: %s advertises %v but validChannelTypes accepts %v; keep them in sync", toolName, advertised, inCode)
			}
		})
	}
}

// supportedTypesFromDescription extracts the sorted channel-type list from the
// "SUPPORTED TYPES: a, b, c" line of a tool description, failing if the marker
// is absent. This keeps the test from hardcoding the advertised set.
func supportedTypesFromDescription(t *testing.T, toolName, desc string) []string {
	t.Helper()
	const marker = "SUPPORTED TYPES:"
	idx := strings.Index(desc, marker)
	if idx < 0 {
		t.Fatalf("%s description missing %q marker; cannot derive advertised channel types", toolName, marker)
	}
	rest := desc[idx+len(marker):]
	// The list runs to the end of that line.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	types := splitCSV(rest)
	sort.Strings(types)
	return types
}

// liveBackendDriftCheckSkipped is the shared skip hook for the live-backend
// drift checks referenced by the TODO(live-backend) notes above. A real
// implementation would hit a live SigNoz instance; until that is wired up (and
// gated behind credentials), the check is skipped so unit runs stay hermetic.
func liveBackendDriftCheckSkipped(t *testing.T) bool {
	t.Helper()
	t.Skip("live-backend drift check not wired up; see TODO(live-backend) in param_schema_test.go")
	return true
}

// TestLiveAggregationSetMatchesBackend is the guarded/skippable live-backend
// counterpart to TestAggregationDescriptionDriftGuard. It is intentionally a
// no-op skip today; it exists as the named hook the cross-contract mandate
// asks for so a future periodic job can fill it in.
func TestLiveAggregationSetMatchesBackend(t *testing.T) {
	liveBackendDriftCheckSkipped(t)
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = appendTrimmed(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = appendTrimmed(out, cur)
	return out
}

func appendTrimmed(out []string, s string) []string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	if start == end {
		return out
	}
	return append(out, s[start:end])
}

// TestTagsParamPassthrough verifies the reverted (pre-N14) tags behavior on
// get_service_top_operations: a "tags" string is forwarded to the backend
// verbatim (the backend's /api/v1/service/top_operations expects a structured
// []TagQueryParam array, which the caller supplies as raw JSON), and an absent
// or non-string value defaults to an empty "[]" filter. Friendlier typed tags
// are tracked as a nerve-pod follow-up.
func TestTagsParamPassthrough(t *testing.T) {
	cases := []struct {
		name string
		tags any
		want string
	}{
		{"absent (nil)", nil, `[]`},
		{"empty string", "", `[]`},
		{"non-string defaults to empty", []any{"ignored"}, `[]`},
		{"structured array forwarded verbatim", `[{"key":"http.method","operator":"In","stringValues":["GET"]}]`, `[{"key":"http.method","operator":"In","stringValues":["GET"]}]`},
		{"arbitrary string forwarded verbatim", `["env=prod"]`, `["env=prod"]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured json.RawMessage
			mock := &signozclient.MockClient{
				GetServiceTopOperationsFn: func(_ context.Context, _, _, _ string, tags json.RawMessage) (json.RawMessage, error) {
					captured = tags
					return json.RawMessage(`{"status":"success","data":[]}`), nil
				},
			}
			h := newTestHandler(mock)
			args := map[string]any{"service": "frontend"}
			if tc.tags != nil {
				args["tags"] = tc.tags
			}
			req := makeToolRequest("signoz_get_service_top_operations", args)
			res, err := h.handleGetServiceTopOperations(testCtx(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.IsError {
				t.Fatalf("unexpected tool error: %s", textContent(t, res))
			}
			if string(captured) != tc.want {
				t.Fatalf("tags forwarded as %q, want %q", string(captured), tc.want)
			}
		})
	}
}

// TestTagsSchemaIsString pins the reverted "tags" param to a plain string schema
// (raw JSON passthrough), not the array-of-strings form (N14 reverted).
func TestTagsSchemaIsString(t *testing.T) {
	props := registeredToolProps(t, "signoz_get_service_top_operations")
	prop, ok := props["tags"].(map[string]any)
	if !ok {
		t.Fatalf("tags property = %#v, want object", props["tags"])
	}
	if prop["type"] != "string" {
		t.Fatalf("tags.type = %v, want \"string\"", prop["type"])
	}
}

// TestSearchDocsParamRenamedWithLegacyAlias pins N12: the canonical param is
// "searchText" (schema), and the handler permanently accepts the legacy "query"
// key as a silent alias.
func TestSearchDocsParamRenamedWithLegacyAlias(t *testing.T) {
	props := registeredToolProps(t, "signoz_search_docs")
	if _, ok := props["searchText"]; !ok {
		t.Fatalf("signoz_search_docs schema missing canonical \"searchText\" param: %#v", props)
	}
	if _, ok := props["query"]; ok {
		t.Fatalf("signoz_search_docs schema should NOT advertise the legacy \"query\" param (alias is handler-only)")
	}
}
