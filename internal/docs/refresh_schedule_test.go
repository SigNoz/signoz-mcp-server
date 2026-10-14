package docs

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recordingFetcher records every URL the scheduler fetches, in order. It serves
// the sitemap unchanged, so an incremental tick stops at the sitemap hash and
// only a forced full tick goes on to fetch pages.
type recordingFetcher struct {
	sitemap string
	mu      sync.Mutex
	urls    []string
}

func (f *recordingFetcher) Fetch(_ context.Context, rawURL string) PageFetch {
	f.mu.Lock()
	f.urls = append(f.urls, rawURL)
	f.mu.Unlock()
	return PageFetch{Status: FetchStatusOK, URL: rawURL, Body: f.sitemap, FetchedAt: time.Now()}
}

// fetches counts sitemap and page fetches. A sitemap fetch followed directly by
// another sitemap fetch marks an incremental tick, because a forced tick fetches
// its pages before the next sitemap.
func (f *recordingFetcher) fetches() (sitemaps, pages int, incrementalTick bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, rawURL := range f.urls {
		if rawURL != DefaultSitemapURL {
			pages++
			continue
		}
		sitemaps++
		if i+1 < len(f.urls) && f.urls[i+1] == DefaultSitemapURL {
			incrementalTick = true
		}
	}
	return sitemaps, pages, incrementalTick
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
			fetcher := &recordingFetcher{sitemap: snapshot.SitemapRaw}
			cfg := tc.cfg
			cfg.RefreshInterval = 5 * time.Millisecond
			cfg.FullRefreshInterval = 10 * time.Millisecond
			cfg.JitterWindow = -1
			refresher := NewRefresher(slog.Default(), reg, nil, cfg)
			refresher.fetcher = fetcher

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// run is what Start launches; driving it directly shows when it exits.
			done := make(chan struct{})
			go func() {
				defer close(done)
				refresher.run(ctx)
			}()

			if !tc.wantCalls {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("a fully disabled scheduler must return instead of arming timers")
				}
				sitemaps, pages, _ := fetcher.fetches()
				require.Zero(t, sitemaps+pages, "disabled scheduler must never fetch")
				return
			}
			// A few ticks of the enabled schedule also give a wrongly enabled one time to fire.
			require.Eventually(t, func() bool {
				sitemaps, pages, _ := fetcher.fetches()
				if tc.wantForced {
					return pages >= 2*len(snapshot.Pages)
				}
				return sitemaps >= 4
			}, time.Second, time.Millisecond)
			cancel()
			<-done

			_, pages, incrementalTick := fetcher.fetches()
			if tc.wantForced {
				require.False(t, incrementalTick, "incremental tick must not fire")
			} else {
				require.Zero(t, pages, "full refresh tick must not fire")
			}
		})
	}
}
