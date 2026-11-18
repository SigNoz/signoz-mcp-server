package client

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		query = r.URL.Query().Encode()
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

func TestListNotificationChannelsV2_PreservesEmptyKind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"channels":[{"id":"`+clientNotificationID+`","name":"legacy","displayName":"Legacy","kind":"","createdAt":"2026-09-18T00:00:00Z","updatedAt":"2026-09-18T00:00:00Z"}],"total":1}}`)
	}))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	got, err := client.ListNotificationChannelsV2(context.Background(), types.NotificationChannelListParams{})
	require.NoError(t, err)
	require.Equal(t, "", got.Channels[0].Kind)
}

// The provider config is SigNoz's contract: a readback passes through verbatim,
// unknown and empty fields included, with no drift log that could leak secrets.
func TestGetNotificationChannel_PreservesEmptyAndUnknownFieldsSilently(t *testing.T) {
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
	require.Empty(t, logs.String())
}

func TestGetNotificationChannel_RejectsMissingID(t *testing.T) {
	body := `{"status":"success","data":{"name":"channel","displayName":"Channel","config":{"kind":"webhook","spec":{"url":"https://example.test"}},"createdAt":"x","updatedAt":"x"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	_, err := client.GetNotificationChannel(context.Background(), clientNotificationID)
	require.ErrorContains(t, err, "missing id")
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
			_, err := c.UpdateNotificationChannel(context.Background(), clientNotificationID, []byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test"}}}`))
			return err
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

// A write whose response cannot be decoded must still surface as committed,
// carrying the id the caller supplied so recovery can target the channel.
func TestUpdateNotificationChannel_ParseFailureSurfacesCommittedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `not-json`)
	}))
	defer server.Close()
	client := NewClient(logpkg.New("error"), server.URL, "key", "SIGNOZ-API-KEY", nil)
	_, err := client.UpdateNotificationChannel(context.Background(), clientNotificationID, []byte(`{"config":{"kind":"webhook","spec":{"url":"https://example.test"}}}`))
	var committed *CommittedNotificationChannelError
	require.ErrorAs(t, err, &committed)
	require.Equal(t, clientNotificationID, committed.ID)
	require.Equal(t, "update", committed.Operation)
}
