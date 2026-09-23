package client

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	logpkg "github.com/SigNoz/signoz-mcp-server/pkg/log"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
	"github.com/stretchr/testify/require"
)

const clientNotificationID = "550e8400-e29b-41d4-a716-446655440000"

func TestListNotificationChannelsV2_QueryAndFilteredTotal(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"status":"success","data":{"channels":[{"id":"`+clientNotificationID+`","name":"channel","displayName":"Channel","kind":"webhook","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}],"total":9}}`)
	}))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	got, err := client.ListNotificationChannelsV2(context.Background(), types.NotificationChannelListParams{
		Query: "ops & prod", Kind: "webhook", Sort: "name", Order: "asc", Limit: 40, Offset: 3,
	})
	require.NoError(t, err)
	require.Equal(t, 9, got.Total)
	require.Len(t, got.Channels, 1)
	require.Equal(t, "kind=webhook&limit=40&offset=3&order=asc&query=ops+%26+prod&sort=name", query)
}

func TestListNotificationChannelsV2_RequiresTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"channels":[]}}`)
	}))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	_, err := client.ListNotificationChannelsV2(context.Background(), types.NotificationChannelListParams{})
	require.ErrorContains(t, err, "total is required")
}

func TestListNotificationChannelsV2_RejectsContradictoryPages(t *testing.T) {
	channel := `{"id":"` + clientNotificationID + `","name":"channel","displayName":"Channel","kind":"webhook","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}`
	for _, tc := range []struct {
		name   string
		offset int
		data   string
		want   string
	}{
		{"empty before total", 2, `{"channels":[],"total":5}`, "non-progressing page"},
		{"page past total", 4, `{"channels":[` + channel + `,` + channel + `],"total":5}`, "exceeds filtered total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"status":"success","data":`+tc.data+`}`)
			}))
			defer server.Close()
			client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
			_, err := client.ListNotificationChannelsV2(context.Background(), types.NotificationChannelListParams{Offset: tc.offset})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestListNotificationChannelsV2_AllowsTerminalAndShortProgressingPages(t *testing.T) {
	channel := `{"id":"` + clientNotificationID + `","name":"channel","displayName":"Channel","kind":"webhook","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}`
	for _, tc := range []struct {
		name   string
		offset int
		data   string
	}{
		{"empty at total", 5, `{"channels":[],"total":5}`},
		{"empty beyond total", 6, `{"channels":[],"total":5}`},
		{"short progressing", 2, `{"channels":[` + channel + `],"total":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"status":"success","data":`+tc.data+`}`)
			}))
			defer server.Close()
			client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
			_, err := client.ListNotificationChannelsV2(context.Background(), types.NotificationChannelListParams{Offset: tc.offset})
			require.NoError(t, err)
		})
	}
}

func TestListNotificationChannelsV2_PreservesLegacyEmptyKindAndWarns(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"channels":[{"id":"`+clientNotificationID+`","name":"legacy","displayName":"Legacy","kind":"","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}],"total":1}}`)
	}))
	defer server.Close()
	client := NewClient(logger, server.URL, "key", "SIGNOZ-API-KEY", nil)
	got, err := client.ListNotificationChannelsV2(context.Background(), types.NotificationChannelListParams{})
	require.NoError(t, err)
	require.Equal(t, "", got.Channels[0].Kind)
	require.Contains(t, logs.String(), "outside the pinned v0.142.0 contract")
}

func TestGetNotificationChannel_PreservesOrdinaryEmptyAndUnknownFields(t *testing.T) {
	const secret = "https://hooks.slack.test/secret"
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	response := `{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"slack","spec":{"sendResolved":false,"apiUrl":"` + secret + `","channel":"","title":"","text":"","futureSpec":"kept"},"futureConfig":true},"id":"` + clientNotificationID + `","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z","futureRoot":{"kept":true}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, response) }))
	defer server.Close()
	client := NewClient(logger, server.URL, "key", "SIGNOZ-API-KEY", nil)
	got, err := client.GetNotificationChannel(context.Background(), clientNotificationID)
	require.NoError(t, err)
	require.Contains(t, string(got), `"futureRoot":{"kept":true}`)
	require.Contains(t, string(got), `"futureSpec":"kept"`)
	require.Contains(t, logs.String(), "preserving the upstream object")
	require.NotContains(t, logs.String(), secret)
}

func TestValidateNotificationChannelData_AcceptsPinnedProviderResponseShapes(t *testing.T) {
	specs := map[string]string{
		"slack":      `{"sendResolved":true,"apiUrl":"https://hooks.slack.test/x","channel":"","title":"","text":""}`,
		"email":      `{"sendResolved":true,"to":"ops@example.test","html":"","headers":{}}`,
		"webhook":    `{"sendResolved":true,"url":"https://example.test/hook","username":"","password":"","bearerToken":""}`,
		"pagerduty":  `{"sendResolved":true,"routingKey":"key","url":"","source":"","client":"","clientUrl":"","description":"","severity":"","component":"","group":"","class":"","details":{}}`,
		"opsgenie":   `{"sendResolved":true,"apiKey":"key","apiUrl":"","message":"","description":"","source":"","details":{},"priority":""}`,
		"msteams":    `{"sendResolved":true,"webhookUrl":"https://teams.test/hook","title":"","text":""}`,
		"googlechat": `{"sendResolved":true,"webhookUrl":"https://chat.test/hook","title":"","text":""}`,
		"jira":       `{"sendResolved":false,"site":"https://site.atlassian.net","project":"OPS","issueType":"Incident","summary":"","description":"","priority":"","labels":[],"resolveTransition":"","reopenTransition":"","reopenDuration":"","wontFixResolution":"","customFields":{},"email":"ops@example.test","apiToken":"token"}`,
		"jsmops":     `{"sendResolved":true,"apiKey":"key","message":"","description":"","priority":"","tags":""}`,
		"incidentio": `{"sendResolved":true,"url":"https://incident.test","token":"token","title":"","description":"","metadata":{}}`,
	}
	for kind, spec := range specs {
		t.Run(kind, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
			client := NewClient(logger, "http://example.invalid", "key", "SIGNOZ-API-KEY", nil)
			data := []byte(`{"name":"channel","displayName":"Channel","config":{"kind":"` + kind + `","spec":` + spec + `},"id":"` + clientNotificationID + `","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}`)
			require.NoError(t, client.validateNotificationChannelData(data))
			require.Empty(t, logs.String(), "pinned response shapes must not report contract violations")
		})
	}
}

func TestGetNotificationChannel_WarnsWhenReadbackViolatesProviderRules(t *testing.T) {
	const secret = "never-log-this-token"
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	response := `{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":{"sendResolved":true,"url":"https://example.test/hook","username":"user","password":"` + secret + `","bearerToken":"` + secret + `"}},"id":"` + clientNotificationID + `","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, response) }))
	defer server.Close()
	client := NewClient(logger, server.URL, "key", "SIGNOZ-API-KEY", nil)
	got, err := client.GetNotificationChannel(context.Background(), clientNotificationID)
	require.NoError(t, err, "a readable channel stays readable")
	require.Contains(t, string(got), `"bearerToken"`)
	require.Contains(t, logs.String(), "violates its provider config rules")
	require.Contains(t, logs.String(), "bearerToken cannot be combined")
	require.NotContains(t, logs.String(), secret)
}

func TestGetNotificationChannel_RejectsMissingIDAndBadSpecShape(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":{"url":"https://example.test"}},"createdAt":"x","updatedAt":"x"}}`,
		`{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":[]},"id":"` + clientNotificationID + `","createdAt":"x","updatedAt":"x"}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
		client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
		_, err := client.GetNotificationChannel(context.Background(), clientNotificationID)
		server.Close()
		require.Error(t, err)
	}
}

func TestNotificationChannelMutations_AreSingleAttempt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		call   func(*SigNoz) error
	}{
		{"create", http.MethodPost, func(c *SigNoz) error {
			_, err := c.CreateNotificationChannel(context.Background(), []byte(`{"name":"channel","config":{"kind":"webhook","spec":{"url":"https://example.test"}}}`))
			return err
		}},
		{"update", http.MethodPut, func(c *SigNoz) error {
			return c.UpdateNotificationChannel(context.Background(), clientNotificationID, []byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test"}}}`))
		}},
		{"delete", http.MethodDelete, func(c *SigNoz) error {
			return c.DeleteNotificationChannel(context.Background(), clientNotificationID)
		}},
		{"test", http.MethodPost, func(c *SigNoz) error {
			return c.TestNotificationChannel(context.Background(), []byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test"}}}`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tc.method, r.Method)
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
			require.Error(t, tc.call(client))
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestTestNotificationChannel_StrictRootAndSpec(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	for _, body := range [][]byte{
		[]byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test"}},"extra":true}`),
		[]byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test","extra":true}}}`),
	} {
		require.Error(t, client.TestNotificationChannel(context.Background(), body))
	}
	require.Zero(t, calls.Load())
}

func TestCreateNotificationChannel_ParseFailurePreservesCommittedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"id":"`+clientNotificationID+`","name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":{"url":"https://example.test"}}}}`)
	}))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	_, err := client.CreateNotificationChannel(context.Background(), []byte(`{"name":"channel","config":{"kind":"webhook","spec":{"url":"https://example.test"}}}`))
	var committed *CommittedNotificationChannelError
	require.ErrorAs(t, err, &committed)
	require.Equal(t, clientNotificationID, committed.ID)
	require.Equal(t, "channel", committed.Name)
}

func TestNotificationResponseDriftWarningDoesNotContainConfig(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logpkg.NewContextHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	client := NewClient(logger, "http://example.invalid", "key", "SIGNOZ-API-KEY", nil)
	err := client.validateNotificationChannelData([]byte(`{"name":"channel","displayName":"Channel","config":{"kind":"new-provider","spec":{"token":"never-log-me"}},"id":"` + clientNotificationID + `","createdAt":"x","updatedAt":"x","newField":true}`))
	require.NoError(t, err)
	require.True(t, strings.Contains(logs.String(), "preserving"))
	require.NotContains(t, logs.String(), "never-log-me")
}
