package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
)

// SigNoz serves GET /api/v3/rules in pages of at most 200, so a tool window
// larger than that is assembled from several upstream pages and the total
// comes from the server, not from counting what came back.
func TestListAlertRules_AssemblesWindowFromUpstreamPages(t *testing.T) {
	const upstreamTotal = 450
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v3/rules" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","error":{"code":"not_found","message":"no route"}}`))
			return
		}
		requested = append(requested, r.URL.Query().Encode())
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit = min(limit, 200)
		rules := make([]map[string]any, 0, limit)
		for i := offset; i < min(offset+limit, upstreamTotal); i++ {
			rules = append(rules, map[string]any{"id": fmt.Sprintf("rule-%03d", i), "alert": fmt.Sprintf("Rule %d", i), "state": "inactive", "labels": map[string]string{"severity": "warning"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"rules": rules, "total": upstreamTotal}})
	}))
	defer server.Close()

	h := newTestHandler(signozclient.NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil))
	result, err := h.handleListAlertRules(testCtx(), makeToolRequest("signoz_list_alert_rules", map[string]any{"limit": "300", "offset": "100"}))
	require.NoError(t, err)
	require.False(t, result.IsError, textContent(t, result))

	var envelope struct {
		Data []struct {
			RuleID   string `json:"ruleId"`
			Severity string `json:"severity"`
		} `json:"data"`
		Pagination struct {
			Total   int  `json:"total"`
			Offset  int  `json:"offset"`
			Limit   int  `json:"limit"`
			HasMore bool `json:"hasMore"`
		} `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal([]byte(textContent(t, result)), &envelope))

	assert.Equal(t, []string{"limit=200&offset=100", "limit=100&offset=300"}, requested)
	require.Len(t, envelope.Data, 300)
	assert.Equal(t, "rule-100", envelope.Data[0].RuleID)
	assert.Equal(t, "rule-399", envelope.Data[299].RuleID)
	assert.Equal(t, "warning", envelope.Data[0].Severity)
	assert.Equal(t, upstreamTotal, envelope.Pagination.Total)
	assert.Equal(t, 100, envelope.Pagination.Offset)
	assert.Equal(t, 300, envelope.Pagination.Limit)
	assert.True(t, envelope.Pagination.HasMore)
}
