package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// A graph in the bare-array shape SigNoz returns, deliberately out of order.
const serviceMapGraph = `[
 {"parent":"orders","child":"redis","callCount":40,"callRate":0.6,"errorRate":0,"p99":3000000},
 {"parent":"frontend","child":"orders","callCount":10,"callRate":0.2,"errorRate":20,"p99":250000000},
 {"parent":"orders","child":"inventory","callCount":8,"callRate":0.1,"errorRate":0,"p99":90000000},
 {"parent":"frontend","child":"inventory","callCount":2,"callRate":0.03,"errorRate":0,"p99":40000000}]`

// serviceMapUpstream serves body for the dependency graph and records each
// request body it receives.
func serviceMapUpstream(t *testing.T, body string) (*Handler, *[]string) {
	t.Helper()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/dependency_graph", r.URL.Path)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requests = append(requests, string(raw))
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return newTestHandler(signozclient.NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil)), &requests
}

type serviceMapPage struct {
	Data []struct {
		Parent string `json:"parent"`
		Child  string `json:"child"`
	} `json:"data"`
	Pagination struct {
		Total   int  `json:"total"`
		HasMore bool `json:"hasMore"`
	} `json:"pagination"`
}

func callServiceMap(t *testing.T, h *Handler, args map[string]any) (*mcp.CallToolResult, serviceMapPage, []string) {
	t.Helper()
	res, err := h.handleGetServiceMap(testCtx(), makeToolRequest("signoz_get_service_map", args))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result: %s", textContent(t, res))
	var page serviceMapPage
	require.NoError(t, json.Unmarshal([]byte(textContent(t, res)), &page))
	var notes []string
	for _, block := range res.Content[1:] {
		if text, ok := mcp.AsTextContent(block); ok {
			notes = append(notes, text.Text)
		}
	}
	return res, page, notes
}

func serviceMapEdgeNames(page serviceMapPage) []string {
	names := []string{}
	for _, edge := range page.Data {
		names = append(names, edge.Parent+">"+edge.Child)
	}
	return names
}

func TestServiceMapKeepsOneServicesEdgesByDirectionInNameOrder(t *testing.T) {
	h, _ := serviceMapUpstream(t, serviceMapGraph)
	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"whole graph", map[string]any{}, []string{"frontend>inventory", "frontend>orders", "orders>inventory", "orders>redis"}},
		{"callees of orders", map[string]any{"service": "orders", "direction": "downstream"}, []string{"orders>inventory", "orders>redis"}},
		{"callers of orders", map[string]any{"service": "orders", "direction": "upstream"}, []string{"frontend>orders"}},
		{"both sides of orders", map[string]any{"service": "orders"}, []string{"frontend>orders", "orders>inventory", "orders>redis"}},
		{"callers of a database", map[string]any{"service": "redis", "direction": "upstream"}, []string{"orders>redis"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, page, notes := callServiceMap(t, h, tc.args)
			assert.Equal(t, tc.want, serviceMapEdgeNames(page))
			assert.Equal(t, len(tc.want), page.Pagination.Total)
			assert.Equal(t, []string{serviceMapUnitsNote}, notes, "every graph result labels the units of its numbers")
		})
	}
}

// environment, cluster, and namespace become SigNoz's tag filter on the three
// resource keys the graph keeps; an unset one adds no tag.
func TestServiceMapSendsWindowInNanosecondsWithFilters(t *testing.T) {
	h, requests := serviceMapUpstream(t, serviceMapGraph)

	callServiceMap(t, h, map[string]any{"start": "1762905600000", "end": "1762992000000", "environment": "production", "namespace": "shop"})
	callServiceMap(t, h, map[string]any{"start": "1762905600000", "end": "1762992000000", "cluster": ""})

	require.Len(t, *requests, 2)
	assert.JSONEq(t, `{"start":"1762905600000000000","end":"1762992000000000000","tags":[
		{"key":"deployment.environment","tagType":"ResourceAttribute","operator":"In","stringValues":["production"]},
		{"key":"k8s.namespace.name","tagType":"ResourceAttribute","operator":"In","stringValues":["shop"]}]}`, (*requests)[0])
	assert.JSONEq(t, `{"start":"1762905600000000000","end":"1762992000000000000","tags":[]}`, (*requests)[1])
}

func TestServiceMapPagesEdgesWithNextOffset(t *testing.T) {
	h, _ := serviceMapUpstream(t, serviceMapGraph)

	_, first, _ := callServiceMap(t, h, map[string]any{"limit": "3"})
	_, second, _ := callServiceMap(t, h, map[string]any{"limit": "3", "offset": "3"})

	assert.Equal(t, []string{"frontend>inventory", "frontend>orders", "orders>inventory"}, serviceMapEdgeNames(first))
	assert.True(t, first.Pagination.HasMore)
	assert.Equal(t, []string{"orders>redis"}, serviceMapEdgeNames(second))
	assert.False(t, second.Pagination.HasMore)
}

// An empty result for a misspelled service must not read as "no dependencies";
// a real leaf service with no callees gets no such note.
func TestServiceMapNotesWhenNoEdgeTouchesTheService(t *testing.T) {
	h, _ := serviceMapUpstream(t, serviceMapGraph)

	_, page, notes := callServiceMap(t, h, map[string]any{"service": "Orders", "direction": "upstream"})
	assert.Empty(t, page.Data)
	assert.Equal(t, []string{serviceMapNoSuchServiceNote, serviceMapUnitsNote}, notes)

	_, page, notes = callServiceMap(t, h, map[string]any{"service": "inventory", "direction": "downstream"})
	assert.Empty(t, page.Data)
	assert.Equal(t, []string{serviceMapUnitsNote}, notes)
}

// When the window or filters leave no edges at all, the note must not blame the
// service name.
func TestServiceMapNotesAnEmptyGraph(t *testing.T) {
	h, _ := serviceMapUpstream(t, `[]`)

	_, page, notes := callServiceMap(t, h, map[string]any{"service": "orders", "direction": "upstream"})

	assert.Empty(t, page.Data)
	assert.Equal(t, []string{serviceMapEmptyGraphNote, serviceMapUnitsNote}, notes)
}

// Each case is a filter the tool cannot apply as asked. None of them may reach
// SigNoz.
func TestServiceMapRejectsFiltersItCannotApply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  map[string]any
		field string
	}{
		{"direction without service", map[string]any{"direction": "upstream"}, `"direction"`},
		{"unknown direction", map[string]any{"service": "orders", "direction": "callers"}, `"direction"`},
		{"service that is not a string", map[string]any{"service": 42}, `"service"`},
		{"environment that is not a string", map[string]any{"environment": 42}, `"environment"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(serviceMapGraph))
			}))
			t.Cleanup(server.Close)
			h := newTestHandler(signozclient.NewClient(logpkg.New("error"), server.URL, "k", "SIGNOZ-API-KEY", nil))

			res, err := h.handleGetServiceMap(testCtx(), makeToolRequest("signoz_get_service_map", tc.args))
			require.NoError(t, err)
			require.True(t, res.IsError, "expected a validation error, got: %s", textContent(t, res))
			assert.Equal(t, CodeValidationFailed, resultCode(t, res))
			assert.True(t, strings.HasPrefix(textContent(t, res), "Parameter validation failed: "+tc.field), "error should name %s: %s", tc.field, textContent(t, res))
			assert.Zero(t, calls.Load(), "a rejected call must not reach SigNoz")
		})
	}
}

// SigNoz answers with a bare array today. A status/data envelope is accepted so
// a future upstream change does not empty the graph; any other body is an
// upstream error, never an empty graph.
func TestServiceMapReadsEnvelopedEdgesAndRejectsOtherBodies(t *testing.T) {
	h, _ := serviceMapUpstream(t, `{"status":"success","data":[{"parent":"frontend","child":"orders","callCount":10}]}`)
	_, page, _ := callServiceMap(t, h, map[string]any{})
	assert.Equal(t, []string{"frontend>orders"}, serviceMapEdgeNames(page))

	for _, body := range []string{`{"status":"error","error":"boom"}`, `<html>gateway</html>x`, `{"data":{"edges":[]}}`} {
		h, _ := serviceMapUpstream(t, body)
		res, err := h.handleGetServiceMap(testCtx(), makeToolRequest("signoz_get_service_map", map[string]any{}))
		require.NoError(t, err)
		require.True(t, res.IsError, "body %s should be an error, got: %s", body, textContent(t, res))
		assert.Equal(t, CodeUpstreamError, resultCode(t, res), "body %s", body)
	}
}
