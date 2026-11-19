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

func TestSitemapFallbackRevalidatesPagesWhenSitemapChanges(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			verifyNoDocsLeaks(t)
			duplicate := SitemapEntry{URL: manifestEntries[1].URL, Title: "Another Section"}
			site, reg, refresher := newManifestSite(t, duplicate)
			refresher.fetcher = newRewriteFetcher(newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/docs/manifest.json" {
					site.set(func() { site.requests = append(site.requests, "GET "+r.URL.Path) })
					w.WriteHeader(status)
					if status == http.StatusOK {
						_, _ = w.Write([]byte("invalid manifest"))
					}
					return
				}
				site.ServeHTTP(w, r)
			})))

			const path = "/docs/install/docker.md"
			ctx := context.Background()
			site.set(func() {
				site.pages[path] = "# Install SigNoz Using Docker\n\nUpdated while the sitemap stayed the same.\n"
			})
			require.NoError(t, refresher.Trigger(ctx, false))
			require.ElementsMatch(t, []string{"GET /docs/manifest.json", "200 /docs/sitemap.md"}, site.take(),
				"fallback makes zero page requests when the sitemap hash matches")

			const newPath = "/docs/new.md"
			site.set(func() {
				site.sitemap += "- [New page](https://signoz.io" + newPath + ")\n"
				site.pages[newPath] = "# New page\n"
			})
			require.NoError(t, refresher.Trigger(ctx, false))
			require.ElementsMatch(t, []string{
				"GET /docs/manifest.json", "200 /docs/sitemap.md", "200 " + path, "304 /docs/logs/overview.md", "200 " + newPath,
			}, site.take(), "a changed sitemap revalidates each unique page once")
			doc, code, err := reg.FetchDoc(ctx, "https://signoz.io"+path, "")
			require.NoError(t, err)
			require.Empty(t, code)
			require.Contains(t, doc.Content, "Updated while the sitemap stayed the same.")

			require.NoError(t, refresher.Trigger(ctx, false))
			require.ElementsMatch(t, []string{"GET /docs/manifest.json", "200 /docs/sitemap.md"}, site.take(),
				"the next fallback uses the newly indexed sitemap hash")
		})
	}
}
