package docs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type manifestSite struct {
	mu              sync.Mutex
	sitemap         string
	manifestSitemap string
	pages           map[string]string
	published       map[string]string // Set an empty ETag to omit a page that the sitemap still lists.
	requests        []string
}

func weakETag(body string) string {
	return fmt.Sprintf(`W/"%x"`, sha256.Sum256([]byte(body)))
}

func (s *manifestSite) manifestBody() string {
	sitemap := s.sitemap
	if s.manifestSitemap != "" {
		sitemap = s.manifestSitemap
	}
	index := DocsETagIndex{
		Version: 1,
		Sitemap: DocsETagIndexEntry{URL: DefaultSitemapURL, ETag: weakETag(sitemap)},
	}
	for path, body := range s.pages {
		etag := weakETag(body)
		if override, ok := s.published[path]; ok {
			if override == "" {
				continue
			}
			etag = override
		}
		index.Pages = append(index.Pages, DocsETagIndexEntry{URL: "https://signoz.io" + path, ETag: etag})
	}
	sort.Slice(index.Pages, func(i, j int) bool { return index.Pages[i].URL < index.Pages[j].URL })
	raw, _ := json.Marshal(index)
	return string(raw)
}

func (s *manifestSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body string
	switch r.URL.Path {
	case "/docs/manifest.json":
		body = s.manifestBody()
	case "/docs/sitemap.md":
		body = s.sitemap
	default:
		page, ok := s.pages[r.URL.Path]
		if !ok {
			s.requests = append(s.requests, "404 "+r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body = page
	}
	etag := weakETag(body)
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		s.requests = append(s.requests, "304 "+r.URL.Path)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	s.requests = append(s.requests, "200 "+r.URL.Path)
	_, _ = w.Write([]byte(body))
}

func (s *manifestSite) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.requests
	s.requests = nil
	return out
}

func (s *manifestSite) set(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

var manifestEntries = []SitemapEntry{
	{URL: "https://signoz.io/docs/install/docker.md", Title: "Install SigNoz Using Docker"},
	{URL: "https://signoz.io/docs/logs/overview.md", Title: "Logs Overview"},
}

func newManifestSite(t *testing.T, additionalEntries ...SitemapEntry) (*manifestSite, *IndexRegistry, *Refresher) {
	t.Helper()
	entries := append(append([]SitemapEntry{}, manifestEntries...), additionalEntries...)
	site := &manifestSite{
		sitemap: sitemapForEntries(entries),
		pages: map[string]string{
			"/docs/install/docker.md": "# Install SigNoz Using Docker\n\nDocker Compose setup.\n",
			"/docs/logs/overview.md":  "# Logs Overview\n\nCollect logs.\n",
		},
		published: map[string]string{},
	}
	for _, entry := range additionalEntries {
		path := strings.TrimPrefix(entry.URL, "https://signoz.io")
		if _, ok := site.pages[path]; !ok {
			site.pages[path] = "# " + entry.Title + "\n\nReference content.\n"
		}
	}
	parsed, err := ParseSitemapMarkdown(site.sitemap)
	require.NoError(t, err)
	bodies := map[string]string{}
	for _, entry := range parsed {
		bodies[entry.URL] = site.pages[entry.URL[len("https://signoz.io"):]]
	}
	initial := snapshotForEntries(parsed, bodies)
	for i := range initial.Pages {
		initial.Pages[i].SourceETag = weakETag(initial.Pages[i].BodyMarkdown)
	}
	reg := newTestRegistry(t, initial)
	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(newHTTPTestServer(t, site)), RefreshConfig{})
	return site, reg, refresher
}

func TestManifestRefreshRevalidatesRecordedWebsiteResponse(t *testing.T) {
	verifyNoDocsLeaks(t)
	// Captured from signoz.io/docs/manifest.json on 2026-11-19, trimmed to one page.
	manifest, err := os.ReadFile("testdata/docs-manifest-v1.json")
	require.NoError(t, err)
	const sitemapETag = `W/"6437c44b6df8131291b371c37cc9a99f75589669"`
	const pageETag = `W/"baa912179162965e707d10242837e8677ae17743"`
	entry := SitemapEntry{URL: "https://signoz.io/docs/install/docker.md", Title: "Install SigNoz Using Docker"}
	entries := []SitemapEntry{entry}
	initial := snapshotForEntries(entries, map[string]string{entry.URL: "# Install SigNoz Using Docker\n\nDocker Compose setup."})
	initial.Pages[0].SourceETag = pageETag

	var requests []string
	var mu sync.Mutex
	manifestETag := weakETag(string(manifest))
	server := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/docs/manifest.json":
			w.Header().Set("ETag", manifestETag)
			if r.Header.Get("If-None-Match") == manifestETag {
				requests = append(requests, "304 /docs/manifest.json")
				w.WriteHeader(http.StatusNotModified)
				return
			}
			requests = append(requests, "200 /docs/manifest.json")
			_, _ = w.Write(manifest)
		case "/docs/sitemap.md":
			requests = append(requests, "200 /docs/sitemap.md")
			w.Header().Set("ETag", sitemapETag)
			_, _ = w.Write([]byte(sitemapForEntries(entries)))
		default:
			requests = append(requests, "404 "+r.URL.Path)
			http.NotFound(w, r)
		}
	}))

	refresher := NewRefresher(slog.Default(), newTestRegistry(t, initial), newRewriteFetcher(server), RefreshConfig{})
	ctx := context.Background()
	require.NoError(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md"}, requests,
		"the matching recorded page ETag should avoid downloading the page")
	requests = nil

	require.NoError(t, refresher.Trigger(ctx, false))
	require.Equal(t, []string{"304 /docs/manifest.json"}, requests,
		"a conditional manifest read should skip the sitemap and page requests")
}

func TestManifestRefreshFetchesChangedURLOnceAndKeepsItsSections(t *testing.T) {
	verifyNoDocsLeaks(t)
	duplicate := SitemapEntry{URL: manifestEntries[1].URL, Title: "Another Section"}
	site, reg, refresher := newManifestSite(t, duplicate)
	require.NoError(t, refresher.Trigger(context.Background(), false))
	site.take()

	site.set(func() {
		site.pages["/docs/logs/overview.md"] = "# Logs Overview\n\nCollect logs with the OpenTelemetry Collector.\n"
	})
	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 /docs/logs/overview.md"}, site.take())

	doc, code, err := reg.FetchDoc(context.Background(), "https://signoz.io/docs/logs/overview.md", "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "OpenTelemetry Collector")
	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	require.Len(t, snapshot.Pages, 2)
	var sections []string
	for _, page := range snapshot.Pages {
		if page.URL == duplicate.URL {
			sections = page.SectionSlugs
			break
		}
	}
	require.ElementsMatch(t, []string{"logs-overview", "another-section"}, sections)
}

func TestManifestRefreshFollowsSitemapChanges(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t)
	require.NoError(t, refresher.Trigger(context.Background(), false))
	site.take()

	added := SitemapEntry{URL: "https://signoz.io/docs/traces/overview.md", Title: "Traces Overview"}
	site.set(func() {
		site.sitemap = sitemapForEntries([]SitemapEntry{manifestEntries[0], added})
		site.pages["/docs/traces/overview.md"] = "# Traces Overview\n\nSpans.\n"
		delete(site.pages, "/docs/logs/overview.md")
	})
	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md", "200 /docs/traces/overview.md"}, site.take())

	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	urls := []string{}
	for _, page := range snapshot.Pages {
		urls = append(urls, page.URL)
	}
	require.ElementsMatch(t, []string{manifestEntries[0].URL, added.URL}, urls)
}

func TestForcedManifestRefreshDoesNotRedownloadPages(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, _, refresher := newManifestSite(t)
	require.NoError(t, refresher.Trigger(context.Background(), false))
	site.take()

	require.NoError(t, refresher.Trigger(context.Background(), true))
	require.Equal(t, []string{"200 /docs/manifest.json"}, site.take(),
		"a forced refresh re-reads the manifest but not unchanged pages")
}

func TestManifestRefreshWaitsForMatchingSitemap(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t)
	ctx := context.Background()
	require.NoError(t, refresher.Trigger(ctx, false))
	site.take()

	added := SitemapEntry{URL: "https://signoz.io/docs/traces/overview.md", Title: "Traces Overview"}
	site.set(func() {
		site.manifestSitemap = sitemapForEntries(append(append([]SitemapEntry{}, manifestEntries...), added))
		site.sitemap = sitemapForEntries(manifestEntries[:1])
		site.pages["/docs/traces/overview.md"] = "# Traces Overview\n\nSpans.\n"
	})
	require.Error(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md"}, site.take())
	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	require.Len(t, snapshot.Pages, 2)

	site.set(func() { site.sitemap = site.manifestSitemap })
	require.NoError(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md", "200 /docs/traces/overview.md"}, site.take())
	snapshot, ok = reg.Snapshot()
	require.True(t, ok)
	require.Len(t, snapshot.Pages, 3)
}

func TestManifestRefreshRetriesStalePageOnNextIncrementalRefresh(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t, manyEntries(20)...)
	ctx := context.Background()
	const path = "/docs/install/docker.md"
	const updated = "# Install SigNoz Using Docker\n\nUpdated instructions.\n"
	require.NoError(t, refresher.Trigger(ctx, false))
	site.take()

	site.set(func() { site.published[path] = weakETag(updated) })
	require.NoError(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "304 " + path}, site.take())

	site.set(func() { site.pages[path] = "# Install SigNoz Using Docker\n\nAn older edge copy.\n" })
	require.NoError(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 " + path}, site.take())
	doc, code, err := reg.FetchDoc(ctx, "https://signoz.io"+path, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "Docker Compose setup.")

	site.set(func() { site.pages[path] = updated })
	require.NoError(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "200 " + path}, site.take())
	doc, code, err = reg.FetchDoc(ctx, "https://signoz.io"+path, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "Updated instructions.")

	require.NoError(t, refresher.Trigger(ctx, false))
	require.Equal(t, []string{"304 /docs/manifest.json"}, site.take())
}

func TestManifestOmissionRevalidatesThePageAndRetriesA404(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t, manyEntries(20)...)
	ctx := context.Background()
	const path = "/docs/install/docker.md"
	require.NoError(t, refresher.Trigger(ctx, false))
	site.take()

	site.set(func() { site.published[path] = "" })
	require.NoError(t, refresher.Trigger(ctx, false))
	require.ElementsMatch(t, []string{"200 /docs/manifest.json", "304 " + path}, site.take(),
		"an omission alone is insufficient evidence that the page is gone")

	site.set(func() { delete(site.pages, path) })
	for range 2 {
		require.NoError(t, refresher.Trigger(ctx, false))
		require.ElementsMatch(t, []string{"200 /docs/manifest.json", "404 " + path}, site.take(),
			"an unchanged manifest should not prevent confirming a missing page")
	}
	doc, code, err := reg.FetchDoc(ctx, "https://signoz.io"+path, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "Docker Compose setup.")
}

func TestManifestErrorSkipsTheTickInsteadOfSweeping(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t)
	failing := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/manifest.json" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		site.ServeHTTP(w, r)
	}))
	refresher.fetcher = newRewriteFetcher(failing)

	require.Error(t, refresher.Trigger(context.Background(), false))
	require.Empty(t, site.take(), "no sitemap or page requests while the manifest is failing")
	doc, code, err := reg.FetchDoc(context.Background(), manifestEntries[0].URL, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "Docker Compose setup.")
}
