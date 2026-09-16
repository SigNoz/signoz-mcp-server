package docs

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingFetcher records how many sitemap fetches the scheduler triggers.
// It always reports the sitemap unchanged so no index rebuild happens.
type countingFetcher struct {
	sitemap string
	calls   atomic.Int64
}

func (c *countingFetcher) Fetch(_ context.Context, rawURL string) PageFetch {
	c.calls.Add(1)
	return PageFetch{Status: FetchStatusOK, URL: rawURL, Body: c.sitemap, FetchedAt: time.Now()}
}

func TestScheduledRefreshCanBeDisabledIndependently(t *testing.T) {
	verifyNoDocsLeaks(t)
	snapshot := testSnapshot()
	snapshot.SitemapHash = SitemapHash(snapshot.SitemapRaw)

	cases := []struct {
		name       string
		cfg        RefreshConfig
		wantCalls  bool
		wantForced bool
	}{
		{"both disabled", RefreshConfig{DisableScheduled: true, DisableFullRefresh: true}, false, false},
		{"incremental disabled", RefreshConfig{DisableScheduled: true}, true, true},
		{"full disabled", RefreshConfig{DisableFullRefresh: true}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newTestRegistry(t, snapshot)
			fetcher := &countingFetcher{sitemap: snapshot.SitemapRaw}
			cfg := tc.cfg
			cfg.RefreshInterval = 5 * time.Millisecond
			cfg.FullRefreshInterval = 10 * time.Millisecond
			cfg.JitterWindow = -1
			refresher := NewRefresher(slog.Default(), reg, nil, cfg)
			refresher.fetcher = fetcher
			reader, meters := newDocsTestMeters(t)
			refresher.SetMeters(meters)

			ctx, cancel := context.WithCancel(context.Background())
			refresher.Start(ctx)
			time.Sleep(80 * time.Millisecond)
			cancel()
			// Let run() observe cancellation before goleak inspects goroutines.
			require.Eventually(t, func() bool {
				before := fetcher.calls.Load()
				time.Sleep(15 * time.Millisecond)
				return fetcher.calls.Load() == before
			}, time.Second, 5*time.Millisecond)

			if !tc.wantCalls {
				require.Zero(t, fetcher.calls.Load(), "disabled scheduler must never fetch")
				return
			}
			require.Positive(t, fetcher.calls.Load())
			forced := docsRefreshMetricValue(t, reader, "forced-full-rebuilt")
			if tc.wantForced {
				require.Positive(t, forced, "full refresh tick should still fire")
				require.Zero(t, docsRefreshMetricValue(t, reader, "no-op"), "incremental tick must not fire")
			} else {
				require.Zero(t, forced, "full refresh tick must not fire")
				require.Positive(t, docsRefreshMetricValue(t, reader, "no-op"), "incremental tick should still fire")
			}
		})
	}
}
