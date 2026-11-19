package docs

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduledRefreshRevalidatesTheManifest(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, _, refresher := newManifestSite(t)
	require.NoError(t, refresher.Trigger(context.Background(), false))
	site.take()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		refresher.cfg.RefreshInterval = time.Hour
		refresher.cfg.JitterWindow = -1
		refresher.lastFallbackRevalidation = time.Now().Add(-48 * time.Hour)

		refresher.Start(ctx)
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		require.Equal(t, []string{"304 /docs/manifest.json", "304 /docs/manifest.json"}, site.take())
	})
}

func TestDisabledScheduleMakesNoRequests(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, _, refresher := newManifestSite(t)
	refresher.cfg.DisableScheduled = true
	refresher.cfg.RefreshInterval = time.Millisecond
	refresher.cfg.JitterWindow = -1

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	refresher.run(ctx)
	require.Empty(t, site.take())
}

func TestSitemapFallbackRevalidatesPagesOnSchedule(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			verifyNoDocsLeaks(t)
			site, reg, refresher := newManifestSite(t)
			refresher.cfg.FallbackRevalidationInterval = time.Hour
			refresher.fetcher = newRewriteFetcher(newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/docs/manifest.json" {
					w.WriteHeader(status)
					if status == http.StatusOK {
						_, _ = w.Write([]byte("invalid manifest"))
					}
					return
				}
				site.ServeHTTP(w, r)
			})))

			const path = "/docs/install/docker.md"
			site.set(func() {
				site.pages[path] = "# Install SigNoz Using Docker\n\nUpdated while the sitemap stayed the same.\n"
			})
			ctx := context.Background()
			require.NoError(t, refresher.Trigger(ctx, false))
			require.Equal(t, []string{"200 /docs/sitemap.md"}, site.take())

			refresher.lastFallbackRevalidation = time.Now().Add(-2 * time.Hour)
			require.NoError(t, refresher.Trigger(ctx, false))
			require.ElementsMatch(t, []string{"200 /docs/sitemap.md", "200 " + path, "304 /docs/logs/overview.md"}, site.take())
			doc, code, err := reg.FetchDoc(ctx, "https://signoz.io"+path, "")
			require.NoError(t, err)
			require.Empty(t, code)
			require.Contains(t, doc.Content, "Updated while the sitemap stayed the same.")

			require.NoError(t, refresher.Trigger(ctx, false))
			require.Equal(t, []string{"200 /docs/sitemap.md"}, site.take())

			// A tick on the legacy-only schedule may land before the configured period.
			refresher.cfg.RevalidateFallbackEveryTick = true
			refresher.lastFallbackRevalidation = time.Now().Add(-55 * time.Minute)
			require.NoError(t, refresher.Trigger(ctx, false))
			require.ElementsMatch(t, []string{"200 /docs/sitemap.md", "304 " + path, "304 /docs/logs/overview.md"}, site.take())
		})
	}
}
