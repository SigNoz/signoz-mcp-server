package docs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type manifestSite struct {
	mu        sync.Mutex
	sitemap   string
	pages     map[string]string
	published map[string]string // manifest ETag overrides, for an edge copy that lags the manifest
	requests  []string
}

func weakETag(body string) string {
	return fmt.Sprintf(`W/"%x"`, sha256.Sum256([]byte(body)))
}

func (s *manifestSite) manifestBody() string {
	index := DocsETagIndex{
		Version: 1,
		Sitemap: DocsETagIndexEntry{URL: DefaultSitemapURL, ETag: weakETag(s.sitemap)},
	}
	for path, body := range s.pages {
		etag := weakETag(body)
		if override, ok := s.published[path]; ok {
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

func newManifestSite(t *testing.T) (*manifestSite, *IndexRegistry, *Refresher) {
	t.Helper()
	site := &manifestSite{
		sitemap: sitemapForEntries(manifestEntries),
		pages: map[string]string{
			"/docs/install/docker.md": "# Install SigNoz Using Docker\n\nDocker Compose setup.\n",
			"/docs/logs/overview.md":  "# Logs Overview\n\nCollect logs.\n",
		},
		published: map[string]string{},
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

func TestManifestRefreshWithoutChangesCostsOneRequest(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, _, refresher := newManifestSite(t)

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md"}, site.take(),
		"pages whose stored ETag matches the manifest are not requested")

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, []string{"304 /docs/manifest.json"}, site.take())
}

func TestManifestRefreshFetchesOnlyChangedPages(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t)
	require.NoError(t, refresher.Trigger(context.Background(), false))
	site.take()

	site.set(func() {
		site.pages["/docs/logs/overview.md"] = "# Logs Overview\n\nCollect logs with the OpenTelemetry Collector.\n"
	})
	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, []string{"200 /docs/manifest.json", "200 /docs/logs/overview.md"}, site.take())

	doc, code, err := reg.FetchDoc(context.Background(), "https://signoz.io/docs/logs/overview.md", "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "OpenTelemetry Collector")
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
	require.Equal(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md", "200 /docs/traces/overview.md"}, site.take())

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

func TestManifestMismatchIsRetriedOnlyByForcedRefresh(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, _, refresher := newManifestSite(t)
	// The manifest already lists a new ETag the page's edge copy doesn't serve yet.
	site.set(func() { site.published["/docs/install/docker.md"] = `W/"newer"` })

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, []string{"200 /docs/manifest.json", "200 /docs/sitemap.md", "304 /docs/install/docker.md"}, site.take())

	site.set(func() { site.pages["/docs/logs/overview.md"] = "# Logs Overview\n\nUpdated.\n" })
	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, []string{"200 /docs/manifest.json", "200 /docs/logs/overview.md"}, site.take())

	require.NoError(t, refresher.Trigger(context.Background(), true))
	require.Equal(t, []string{"200 /docs/manifest.json", "304 /docs/install/docker.md"}, site.take())
}

func TestManifestMissingPagesKeepsLastGoodIndex(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t)
	before := reg.currentHandle(t)
	var index DocsETagIndex
	require.NoError(t, json.Unmarshal([]byte(site.manifestBody()), &index))
	index.Pages = index.Pages[:1]
	truncated, err := json.Marshal(index)
	require.NoError(t, err)
	server := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/manifest.json" {
			_, _ = w.Write(truncated)
			return
		}
		site.ServeHTTP(w, r)
	}))
	refresher.fetcher = newRewriteFetcher(server)

	require.Error(t, refresher.Trigger(context.Background(), false))
	require.Same(t, before, reg.currentHandle(t))
	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	require.Len(t, snapshot.Pages, 2)
}

func TestManifestErrorSkipsTheTickInsteadOfSweeping(t *testing.T) {
	verifyNoDocsLeaks(t)
	site, reg, refresher := newManifestSite(t)
	before := reg.currentHandle(t)
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
	require.Same(t, before, reg.currentHandle(t))
}
