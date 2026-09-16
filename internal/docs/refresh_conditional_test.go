package docs

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// etagServer serves a sitemap and pages with stable weak ETags and honours
// If-None-Match with 304, mirroring what signoz.io does for docs markdown.
type etagServer struct {
	mu          sync.Mutex
	sitemap     string
	bodies      map[string]string // path → body
	etags       map[string]string // path → etag
	conditional atomic.Int64      // requests carrying If-None-Match
	served200   atomic.Int64
	served304   atomic.Int64
}

func newETagServer(sitemap string, bodies map[string]string) *etagServer {
	s := &etagServer{sitemap: sitemap, bodies: map[string]string{}, etags: map[string]string{}}
	for path, body := range bodies {
		s.set(path, body, `W/"`+path+`-v1"`)
	}
	return s
}

func (s *etagServer) set(path, body, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies[path] = body
	s.etags[path] = etag
}

func (s *etagServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/docs/sitemap.md" {
		_, _ = w.Write([]byte(s.sitemap))
		return
	}
	s.mu.Lock()
	body, ok := s.bodies[r.URL.Path]
	etag := s.etags[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		s.conditional.Add(1)
		if inm == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			s.served304.Add(1)
			return
		}
	}
	w.Header().Set("ETag", etag)
	s.served200.Add(1)
	_, _ = w.Write([]byte(body))
}

func conditionalFixture(t *testing.T) ([]SitemapEntry, CorpusSnapshot, *etagServer) {
	t.Helper()
	entries := []SitemapEntry{
		{URL: "https://signoz.io/docs/install/docker/", Title: "Install SigNoz Using Docker", SectionSlug: "install", SectionBreadcrumb: "Install > Docker"},
		{URL: "https://signoz.io/docs/logs/overview/", Title: "Logs Overview", SectionSlug: "logs", SectionBreadcrumb: "Logs > Overview"},
	}
	bodies := map[string]string{
		"/docs/install/docker/": "# Install SigNoz Using Docker\n\nDocker Compose setup.\n",
		"/docs/logs/overview/":  "# Logs Overview\n\nCollect logs.\n",
	}
	// Build the baseline from the entries the refresher will parse out of the
	// served sitemap so section metadata matches what a live fetch produces;
	// the content-gating diff compares those fields too.
	sitemap := sitemapForEntries(entries)
	parsed, err := ParseSitemapMarkdown(sitemap)
	require.NoError(t, err)
	require.Len(t, parsed, len(entries))
	initial := snapshotForEntries(parsed, map[string]string{
		entries[0].URL: bodies["/docs/install/docker/"],
		entries[1].URL: bodies["/docs/logs/overview/"],
	})
	for i := range initial.Pages {
		initial.Pages[i].SourceETag = `W/"` + "/docs" + initial.Pages[i].URL[len("https://signoz.io/docs"):] + `-v1"`
		initial.Pages[i].FetchedAt = time.Now().UTC().Add(-48 * time.Hour)
	}
	// The embedded corpus never matches the live sitemap; model that so the
	// refresh takes the full path rather than the hash no-op.
	initial.SitemapHash = "stale"
	return parsed, initial, newETagServer(sitemap, bodies)
}

func (r *IndexRegistry) currentHandle(t *testing.T) *indexHandle {
	t.Helper()
	r.mu.RLock()
	defer r.mu.RUnlock()
	require.NotNil(t, r.current)
	return r.current.handle
}

func TestFetcherConditionalRequest(t *testing.T) {
	_, _, srv := conditionalFixture(t)
	fetcher := newRewriteFetcher(newHTTPTestServer(t, srv))
	url := "https://signoz.io/docs/install/docker/"

	first := fetcher.Fetch(context.Background(), url)
	require.Equal(t, FetchStatusOK, first.Status)
	require.Equal(t, `W/"/docs/install/docker/-v1"`, first.ETag)
	require.Contains(t, first.Body, "Docker Compose")

	same := fetcher.FetchConditional(context.Background(), url, first.ETag)
	require.Equal(t, FetchStatusNotModified, same.Status)
	require.Equal(t, first.ETag, same.ETag)
	require.Empty(t, same.Body)
	require.Equal(t, http.StatusNotModified, same.StatusCode)

	srv.set("/docs/install/docker/", "# Install SigNoz Using Docker\n\nNew steps.\n", `W/"v2"`)
	changed := fetcher.FetchConditional(context.Background(), url, first.ETag)
	require.Equal(t, FetchStatusOK, changed.Status)
	require.Equal(t, `W/"v2"`, changed.ETag)
	require.Contains(t, changed.Body, "New steps")
}

func TestRefreshRevalidatesWithETagAndKeepsIndex(t *testing.T) {
	verifyNoDocsLeaks(t)
	_, initial, srv := conditionalFixture(t)
	reg := newTestRegistry(t, initial)
	before := reg.currentHandle(t)
	reader, meters := newDocsTestMeters(t)
	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(newHTTPTestServer(t, srv)), RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.SetMeters(meters)

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(2), srv.conditional.Load(), "every page with a stored ETag is revalidated")
	require.Equal(t, int64(2), srv.served304.Load())
	require.Zero(t, srv.served200.Load())
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "unchanged"))
	require.Zero(t, docsRefreshMetricValue(t, reader, "rebuilt"))
	require.Same(t, before, reg.currentHandle(t), "unchanged content must not rebuild the index")

	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	require.Equal(t, SitemapHash(srv.sitemap), snapshot.SitemapHash, "published snapshot carries the live sitemap hash")
	require.Contains(t, snapshot.Pages[0].BodyMarkdown, "Docker Compose")
	metrics := collectDocsMetrics(t, reader)
	require.Equal(t, int64(2), int64GaugeValue(t, metrics, "signoz_docs_index_generation"))

	// The next scheduled tick now short-circuits on the sitemap hash.
	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "no-op"))
	require.Equal(t, int64(2), srv.conditional.Load(), "a no-op tick fetches no pages")

	search, err := reg.Search(context.Background(), "docker compose", "", 3)
	require.NoError(t, err)
	require.NotEmpty(t, search.Results)
}

func TestForcedRefreshRefetchesWithoutRevalidation(t *testing.T) {
	verifyNoDocsLeaks(t)
	_, initial, srv := conditionalFixture(t)
	reg := newTestRegistry(t, initial)
	before := reg.currentHandle(t)
	reader, meters := newDocsTestMeters(t)
	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(newHTTPTestServer(t, srv)), RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.SetMeters(meters)

	require.NoError(t, refresher.Trigger(context.Background(), true))
	require.Zero(t, srv.conditional.Load(), "forced refresh must not send If-None-Match")
	require.Equal(t, int64(2), srv.served200.Load())
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "forced-unchanged"))
	require.Zero(t, docsRefreshMetricValue(t, reader, "forced-full-rebuilt"))
	require.Same(t, before, reg.currentHandle(t), "identical bodies must not rebuild even when forced")

	doc, code, err := reg.FetchDoc(context.Background(), "https://signoz.io/docs/install/docker/", "")
	require.NoError(t, err)
	require.Empty(t, code)
	fetchedAt, err := time.Parse(time.RFC3339, doc.LastFetchedAt)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), fetchedAt, time.Minute,
		"last_fetched_at must reflect the successful re-download even though the index was not rebuilt")
}

func TestRefreshRebuildsWhenContentChanges(t *testing.T) {
	verifyNoDocsLeaks(t)
	_, initial, srv := conditionalFixture(t)
	srv.set("/docs/logs/overview/", "# Logs Overview\n\nCollect logs with the OpenTelemetry Collector.\n", `W/"v2"`)
	reg := newTestRegistry(t, initial)
	before := reg.currentHandle(t)
	reader, meters := newDocsTestMeters(t)
	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(newHTTPTestServer(t, srv)), RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.SetMeters(meters)

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(1), srv.served304.Load(), "the unchanged page revalidates")
	require.Equal(t, int64(1), srv.served200.Load(), "the changed page downloads a body")
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "rebuilt"))
	require.NotSame(t, before, reg.currentHandle(t))

	doc, code, err := reg.FetchDoc(context.Background(), "https://signoz.io/docs/logs/overview/", "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "OpenTelemetry Collector")
	snapshot, _ := reg.Snapshot()
	for _, page := range snapshot.Pages {
		if page.URL == "https://signoz.io/docs/logs/overview/" {
			require.Equal(t, `W/"v2"`, page.SourceETag, "new ETag is stored for the next revalidation")
		}
	}
}

func TestDiffSnapshots(t *testing.T) {
	entries := manyEntries(4)
	bodies := bodiesForEntries(entries)
	current := snapshotForEntries(entries, bodies)

	next := snapshotForEntries(entries, bodies)
	require.True(t, diffSnapshots(current, next).empty(), "identical content is an empty delta")

	// Bookkeeping-only differences are not content changes.
	for i := range next.Pages {
		next.Pages[i].FetchedAt = next.Pages[i].FetchedAt.Add(time.Hour)
		next.Pages[i].SourceETag = "changed"
	}
	require.True(t, diffSnapshots(current, next).empty())

	changedBodies := map[string]string{}
	for k, v := range bodies {
		changedBodies[k] = v
	}
	changedBodies[entries[1].URL] = "# Page 01\n\nRewritten.\n"
	extra := SitemapEntry{URL: "https://signoz.io/docs/page-new/", Title: "New", SectionSlug: "test", SectionBreadcrumb: "Test > New"}
	changedBodies[extra.URL] = "# New\n"
	next = snapshotForEntries(append([]SitemapEntry{entries[0], entries[1], entries[2]}, extra), changedBodies)
	delta := diffSnapshots(current, next)
	require.Len(t, delta.added, 1)
	require.Equal(t, extra.URL, delta.added[0].URL)
	require.Len(t, delta.changed, 1)
	require.Equal(t, entries[1].URL, delta.changed[0].URL)
	require.Equal(t, []string{entries[3].URL}, delta.removed)
}

func TestPublishSnapshotSharesIndexAcrossGenerations(t *testing.T) {
	verifyNoDocsLeaks(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries := manyEntries(3)
	snapshot := snapshotForEntries(entries, bodiesForEntries(entries))
	reg := newTestRegistry(t, snapshot)
	handle := reg.currentHandle(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				_, err := reg.Search(ctx, "page", "", 5)
				require.NoError(t, err)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		next := snapshot
		next.SitemapHash = SitemapHash(next.SitemapRaw + string(rune('a'+i)))
		require.NoError(t, reg.PublishSnapshot(ctx, next))
		require.Same(t, handle, reg.currentHandle(t))
	}
	wg.Wait()

	require.NoError(t, reg.Swap(ctx, snapshot))
	require.NotSame(t, handle, reg.currentHandle(t))
	require.Eventually(t, func() bool { return atomic.LoadInt64(&handle.entries) == 0 }, time.Second, 5*time.Millisecond,
		"the shared index closes once the last generation referencing it drains")
	res, err := reg.Search(ctx, "page", "", 5)
	require.NoError(t, err)
	require.NotEmpty(t, res.Results)
}
