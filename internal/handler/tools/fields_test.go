package tools

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

type fieldsRequest struct {
	path  string
	query url.Values
}

// newFieldsToolServer registers the field tools against an upstream that records
// each request and answers with status and body.
func newFieldsToolServer(t *testing.T, status int, body string) (*mcp.Server, *[]fieldsRequest) {
	t.Helper()
	var requests []fieldsRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, fieldsRequest{path: r.URL.Path, query: r.URL.Query()})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	h := newTestHandler(signozclient.NewClient(logpkg.New("error"), upstream.URL, "test-api-key", "SIGNOZ-API-KEY", nil))
	server := newMCPTestServer()
	h.RegisterFieldsHandlers(server)
	return server, &requests
}

func allTextBlocks(result *mcp.CallToolResult) string {
	var texts []string
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// builder_ai_query reaches the AI fields keys endpoint without signal, source, or
// metricName, which that endpoint does not take; any other queryType keeps the
// default endpoint, and an out-of-enum one also draws the validation notice.
func TestGetFieldKeys_QueryTypeSelectsTheEndpoint(t *testing.T) {
	const body = `{"status":"success","data":{"keys":{},"complete":true}}`
	tests := []struct {
		name      string
		arguments string
		wantPath  string
		wantQuery url.Values
		wantNote  bool
	}{
		{
			name:      "default",
			arguments: `{"signal":"traces","searchText":"http","fieldContext":"attribute"}`,
			wantPath:  "/api/v1/fields/keys",
			wantQuery: url.Values{"signal": {"traces"}, "searchText": {"http"}, "fieldContext": {"attribute"}},
		},
		{
			name:      "builder_ai_query",
			arguments: `{"signal":"logs","queryType":"builder_ai_query","searchText":"cost","fieldContext":"trace","fieldDataType":"float64","source":"meter","metricName":"m"}`,
			wantPath:  "/api/v1/ai_observability/fields/keys",
			wantQuery: url.Values{"searchText": {"cost"}, "fieldContext": {"trace"}, "fieldDataType": {"float64"}},
		},
		{
			name:      "out-of-enum queryType",
			arguments: `{"signal":"traces","queryType":"ai"}`,
			wantPath:  "/api/v1/fields/keys",
			wantQuery: url.Values{"signal": {"traces"}},
			wantNote:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, requests := newFieldsToolServer(t, http.StatusOK, body)
			result := callRegisteredTool(t, server, "signoz_get_field_keys", tt.arguments)

			require.False(t, result.IsError, allTextBlocks(result))
			require.Equal(t, body, textContent(t, result))
			require.Len(t, *requests, 1)
			require.Equal(t, tt.wantPath, (*requests)[0].path)
			require.Equal(t, tt.wantQuery, (*requests)[0].query)
			require.Equal(t, tt.wantNote, strings.Contains(allTextBlocks(result), inputValidationNoticePrefix+` parameter "queryType"`))
		})
	}
}

func TestGetFieldKeys_AIFieldsPermissionDeniedIsCoded(t *testing.T) {
	server, requests := newFieldsToolServer(t, http.StatusForbidden,
		`{"status":"error","error":{"type":"forbidden","code":"forbidden","message":"viewer role required","errors":[]}}`)
	result := callRegisteredTool(t, server, "signoz_get_field_keys", `{"signal":"traces","queryType":"builder_ai_query"}`)

	require.Len(t, *requests, 1)
	require.Equal(t, "/api/v1/ai_observability/fields/keys", (*requests)[0].path)
	require.Equal(t, CodePermissionDenied, resultCode(t, result))
	require.Contains(t, resultText(t, result), "viewer role required")
}
