package tools

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
)

// TestTimeWindowedTools_SendTheResolvedWindowUpstream pins that every
// time-windowed tool sends SigNoz the window it was given as its start and end
// bounds, in the unit its route takes (milliseconds, or nanoseconds for the
// service routes), reports that window in its result, and without bounds
// queries the timeRange default its schema advertises.
func TestTimeWindowedTools_SendTheResolvedWindowUpstream(t *testing.T) {
	const (
		window = `"start":"2026-11-19T14:30:00+05:30","end":"2026-11-19T10:00:00Z"`
		note   = "window queried: 2026-11-19T09:00:00Z to 2026-11-19T10:00:00Z (Unix ms 1795078800000 to 1795082400000)"
		// The structured window, written as JSON so a renamed field fails.
		structuredWindow = `{"end":"2026-11-19T10:00:00Z","endMs":1795082400000,"start":"2026-11-19T09:00:00Z","startMs":1795078800000}`
	)

	var mu sync.Mutex
	var sent []sentBounds
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bounds, ok := requestBounds(r)
		if ok {
			mu.Lock()
			sent = append(sent, bounds)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	t.Cleanup(upstream.Close)
	takeSent := func() []sentBounds {
		mu.Lock()
		defer mu.Unlock()
		got := sent
		sent = nil
		return got
	}
	h := &Handler{
		logger:         logpkg.New("error"),
		clientOverride: signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil),
	}
	server := newMCPTestServer()
	h.RegisterAllToolHandlers(server)
	registered := listTestTools(t, server)

	tests := []struct {
		tool     string
		required string // arguments the tool needs besides its window
		nanos    bool   // the route takes Unix nanoseconds
	}{
		{tool: "signoz_search_logs"},
		{tool: "signoz_aggregate_logs", required: `"aggregations":"count()"`},
		{tool: "signoz_search_traces"},
		{tool: "signoz_aggregate_traces", required: `"aggregations":"count()"`},
		{tool: "signoz_query_metrics", required: `"metricName":"m","metricType":"gauge"`},
		{tool: "signoz_list_metrics"},
		{tool: "signoz_get_top_metrics"},
		{tool: "signoz_check_metric_cardinality", required: `"metricName":"m"`},
		{tool: "signoz_get_alert_history", required: `"id":"0196634d-5d66-75c4-b778-e317f49dab7a"`},
		{tool: "signoz_list_services", nanos: true},
		{tool: "signoz_get_service_top_operations", required: `"service":"frontend"`, nanos: true},
		{tool: "signoz_get_service_map", nanos: true},
	}
	// Structured results carry the window as a field, because a client may
	// show the model only StructuredContent.
	structuredTools := map[string]bool{"signoz_list_services": true, "signoz_get_service_map": true}
	for _, tt := range tests {
		t.Run(tt.tool+" given window", func(t *testing.T) {
			want := sentBounds{start: "1795078800000", end: "1795082400000"}
			if tt.nanos {
				want = sentBounds{start: "1795078800000000000", end: "1795082400000000000"}
			}
			takeSent()
			result := callRegisteredTool(t, server, tt.tool, toolArguments(tt.required, window))
			if result.IsError {
				t.Fatalf("result is an error: %s", allTextBlocks(result))
			}
			if structuredTools[tt.tool] {
				var structured struct {
					Window json.RawMessage `json:"window"`
				}
				raw, _ := json.Marshal(result.StructuredContent)
				_ = json.Unmarshal(raw, &structured)
				if string(structured.Window) != structuredWindow {
					t.Fatalf("structured window = %s, want %s", structured.Window, structuredWindow)
				}
			} else if !hasNoteBlock(result, note) {
				t.Fatalf("result has no note block %q; blocks: %s", note, allTextBlocks(result))
			}
			got := takeSent()
			if len(got) == 0 {
				t.Fatalf("no upstream request carried start and end bounds")
			}
			for _, bounds := range got {
				if bounds != want {
					t.Fatalf("SigNoz received start %s and end %s, want start %s and end %s", bounds.start, bounds.end, want.start, want.end)
				}
			}
		})

		t.Run(tt.tool+" default window", func(t *testing.T) {
			advertised := advertisedTimeRangeDefault(t, registered[tt.tool])
			length, err := timeutil.ParseDuration(advertised)
			if err != nil {
				t.Fatalf("advertised timeRange default %q does not parse: %v", advertised, err)
			}
			want := length.Milliseconds()
			if tt.nanos {
				want = length.Nanoseconds()
			}
			takeSent()
			result := callRegisteredTool(t, server, tt.tool, toolArguments(tt.required, ""))
			if result.IsError {
				t.Fatalf("result is an error: %s", allTextBlocks(result))
			}
			got := takeSent()
			if len(got) == 0 {
				t.Fatalf("no upstream request carried start and end bounds")
			}
			for _, bounds := range got {
				start, errStart := strconv.ParseInt(bounds.start, 10, 64)
				end, errEnd := strconv.ParseInt(bounds.end, 10, 64)
				if errStart != nil || errEnd != nil || end-start != want {
					t.Fatalf("SigNoz received start %s and end %s, a window of %d; want the advertised default %q, %d", bounds.start, bounds.end, end-start, advertised, want)
				}
			}
		})
	}
}

// sentBounds is the start and end a request sent SigNoz, as written.
type sentBounds struct{ start, end string }

// requestBounds reads start and end from a request's query parameters or from
// the top level of its JSON body, the two places time-windowed routes put them.
func requestBounds(r *http.Request) (sentBounds, bool) {
	query := r.URL.Query()
	if query.Has("start") && query.Has("end") {
		return sentBounds{start: query.Get("start"), end: query.Get("end")}, true
	}
	body, _ := io.ReadAll(r.Body)
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return sentBounds{}, false
	}
	start, hasStart := boundText(fields["start"])
	end, hasEnd := boundText(fields["end"])
	return sentBounds{start: start, end: end}, hasStart && hasEnd
}

func boundText(v any) (string, bool) {
	switch typed := v.(type) {
	case json.Number:
		return typed.String(), true
	case string:
		return typed, true
	default:
		return "", false
	}
}

// toolArguments joins a tool's required arguments and a window into one JSON object.
func toolArguments(required, window string) string {
	switch {
	case required == "":
		return "{" + window + "}"
	case window == "":
		return "{" + required + "}"
	default:
		return "{" + required + "," + window + "}"
	}
}

// advertisedTimeRangeDefault returns the default a tool's schema advertises for timeRange.
func advertisedTimeRangeDefault(t *testing.T, tool *registeredTool) string {
	t.Helper()
	if tool == nil {
		t.Fatal("tool is not registered")
	}
	var schema struct {
		Properties struct {
			TimeRange struct {
				Default string `json:"default"`
			} `json:"timeRange"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(inputSchemaJSON(tool.Tool), &schema); err != nil {
		t.Fatalf("decode input schema: %v", err)
	}
	if schema.Properties.TimeRange.Default == "" {
		t.Fatal("schema advertises no timeRange default")
	}
	return schema.Properties.TimeRange.Default
}

// hasNoteBlock reports whether a block after the result's first holds note.
func hasNoteBlock(result *mcp.CallToolResult, note string) bool {
	for _, block := range result.Content[1:] {
		if text, ok := mcp.AsTextContent(block); ok && text.Text == note {
			return true
		}
	}
	return false
}
