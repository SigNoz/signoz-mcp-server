package docs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// deltaFixture returns sitemap entries as the refresher parses them, a
// baseline snapshot built from those entries, and a fetch map serving the
// sitemap plus every page body.
func deltaFixture(t *testing.T, n int) ([]SitemapEntry, CorpusSnapshot, map[string]PageFetch) {
	t.Helper()
	sitemap := sitemapForEntries(manyEntries(n))
	parsed, err := ParseSitemapMarkdown(sitemap)
	require.NoError(t, err)
	require.Len(t, parsed, n)
	bodies := bodiesForEntries(parsed)
	initial := snapshotForEntries(parsed, bodies)
	// The live sitemap never matches the baseline hash, so the refresh takes
	// the full path rather than the hash no-op.
	initial.SitemapHash = "stale"
	fetches := map[string]PageFetch{
		DefaultSitemapURL: {Status: FetchStatusOK, URL: DefaultSitemapURL, Body: sitemap, FetchedAt: time.Now()},
	}
	for _, entry := range parsed {
		fetches[entry.URL] = PageFetch{Status: FetchStatusOK, URL: entry.URL, Body: bodies[entry.URL], FetchedAt: time.Now()}
	}
	return parsed, initial, fetches
}

func changeBodies(t *testing.T, entries []SitemapEntry, fetches map[string]PageFetch, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		fetch := fetches[entries[i].URL]
		fetch.Body = "# " + entries[i].Title + "\n\nRewritten with opentelemetry collector notes.\n"
		fetches[entries[i].URL] = fetch
	}
}

func newDeltaRefresher(t *testing.T, reg *IndexRegistry, fetches map[string]PageFetch) (*Refresher, func(string) int64) {
	t.Helper()
	reader, meters := newDocsTestMeters(t)
	refresher := NewRefresher(slog.Default(), reg, nil, RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.fetcher = mapFetcher(fetches)
	refresher.SetMeters(meters)
	return refresher, func(outcome string) int64 { return docsRefreshMetricValue(t, reader, outcome) }
}

func TestApplyDeltaUpdatesLiveIndex(t *testing.T) {
	verifyNoDocsLeaks(t)
	ctx := context.Background()
	entries := manyEntries(4)
	bodies := bodiesForEntries(entries)
	current := snapshotForEntries(entries, bodies)
	reg := newTestRegistry(t, current)
	handle := reg.currentHandle(t)

	added := SitemapEntry{URL: "https://signoz.io/docs/page-new/", Title: "Page New", SectionSlug: "test", SectionBreadcrumb: "Test > Page New"}
	nextEntries := []SitemapEntry{entries[0], entries[1], entries[2], added}
	nextBodies := map[string]string{}
	for url, body := range bodies {
		nextBodies[url] = body
	}
	nextBodies[added.URL] = "# Page New\n\nBrand new sampling guide.\n"
	nextBodies[entries[1].URL] = "# Page 01\n\nRewritten with clickhouse retention notes.\n"
	next := snapshotForEntries(nextEntries, nextBodies)

	delta := diffSnapshots(current, next)
	require.Len(t, delta.added, 1)
	require.Len(t, delta.changed, 1)
	require.Equal(t, []string{entries[3].URL}, delta.removed)

	require.NoError(t, reg.ApplyDelta(ctx, next, delta))
	require.Same(t, handle, reg.currentHandle(t), "a delta reuses the live index")
	require.Equal(t, int64(1), atomic.LoadInt64(&handle.incrementalApplies))

	doc, code, err := reg.FetchDoc(ctx, added.URL, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "Brand new sampling guide")

	doc, code, err = reg.FetchDoc(ctx, entries[1].URL, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "clickhouse retention")

	_, code, err = reg.FetchDoc(ctx, entries[3].URL, "")
	require.NoError(t, err)
	require.Equal(t, CodeDocNotFound, code)

	search, err := reg.Search(ctx, "sampling guide", "", 10)
	require.NoError(t, err)
	require.NotEmpty(t, search.Results)
	require.Equal(t, added.URL, search.Results[0].URL)

	search, err = reg.Search(ctx, "page", "", 25)
	require.NoError(t, err)
	for _, result := range search.Results {
		require.NotEqual(t, entries[3].URL, result.URL, "removed pages must not be searchable")
	}

	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	require.Len(t, snapshot.Pages, 4)
	require.Equal(t, next.SitemapHash, snapshot.SitemapHash)

	reg.mu.RLock()
	generation := reg.current.generation
	reg.mu.RUnlock()
	require.Equal(t, uint64(2), generation)
}

func TestApplyDeltaFallsBackToSwapWhenClosed(t *testing.T) {
	entries := manyEntries(3)
	snapshot := snapshotForEntries(entries, bodiesForEntries(entries))
	reg := newTestRegistry(t, snapshot)
	reg.Close(context.Background())
	require.Error(t, reg.ApplyDelta(context.Background(), snapshot, snapshotDelta{added: snapshot.Pages}))
}

func TestRefreshAppliesSmallDeltaInPlace(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries, initial, fetches := deltaFixture(t, 40)
	changeBodies(t, entries, fetches, 1)
	reg := newTestRegistry(t, initial)
	handle := reg.currentHandle(t)
	refresher, metric := newDeltaRefresher(t, reg, fetches)

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(1), metric("applied-delta"))
	require.Zero(t, metric("rebuilt"))
	require.Same(t, handle, reg.currentHandle(t))
	require.Equal(t, int64(1), atomic.LoadInt64(&handle.incrementalApplies))

	doc, code, err := reg.FetchDoc(context.Background(), entries[0].URL, "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "opentelemetry collector notes")
}

func TestRefreshRebuildsWhenDeltaExceedsFraction(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries, initial, fetches := deltaFixture(t, 40)
	changeBodies(t, entries, fetches, 11) // > 25% of 40
	reg := newTestRegistry(t, initial)
	handle := reg.currentHandle(t)
	refresher, metric := newDeltaRefresher(t, reg, fetches)

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(1), metric("rebuilt"))
	require.Zero(t, metric("applied-delta"))
	require.NotSame(t, handle, reg.currentHandle(t))
}

func TestForcedRefreshAlwaysRebuilds(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries, initial, fetches := deltaFixture(t, 40)
	changeBodies(t, entries, fetches, 1)
	reg := newTestRegistry(t, initial)
	handle := reg.currentHandle(t)
	refresher, metric := newDeltaRefresher(t, reg, fetches)

	require.NoError(t, refresher.Trigger(context.Background(), true))
	require.Equal(t, int64(1), metric("forced-full-rebuilt"))
	require.Zero(t, metric("applied-delta"))
	require.NotSame(t, handle, reg.currentHandle(t), "a forced refresh compacts the index")
}

func TestRefreshCompactsAfterApplyCap(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries, initial, fetches := deltaFixture(t, 40)
	changeBodies(t, entries, fetches, 1)
	reg := newTestRegistry(t, initial)
	handle := reg.currentHandle(t)
	atomic.StoreInt64(&handle.incrementalApplies, maxIncrementalApplies)
	refresher, metric := newDeltaRefresher(t, reg, fetches)

	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(1), metric("rebuilt"))
	require.Zero(t, metric("applied-delta"))
	next := reg.currentHandle(t)
	require.NotSame(t, handle, next)
	require.Zero(t, atomic.LoadInt64(&next.incrementalApplies), "a rebuild resets the apply budget")
}

func TestApplyDeltaConcurrentWithSearch(t *testing.T) {
	verifyNoDocsLeaks(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries := manyEntries(12)
	bodies := bodiesForEntries(entries)
	current := snapshotForEntries(entries, bodies)
	reg := newTestRegistry(t, current)
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
		nextBodies := map[string]string{}
		for url, body := range bodies {
			nextBodies[url] = body
		}
		nextBodies[entries[i%len(entries)].URL] = fmt.Sprintf("# %s\n\nRevision %d.\n", entries[i%len(entries)].Title, i)
		next := snapshotForEntries(entries, nextBodies)
		delta := diffSnapshots(current, next)
		require.NoError(t, reg.ApplyDelta(ctx, next, delta))
		require.Same(t, handle, reg.currentHandle(t))
		current = next
		bodies = nextBodies
	}
	wg.Wait()
	require.Equal(t, int64(10), atomic.LoadInt64(&handle.incrementalApplies))

	res, err := reg.Search(ctx, "revision", "", 25)
	require.NoError(t, err)
	require.NotEmpty(t, res.Results)
}

func TestBuildIndexChunksLargeCorpus(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries := manyEntries(250)
	require.Greater(t, len(entries), 2*indexBatchSize, "corpus must span several batches")
	snapshot := snapshotForEntries(entries, bodiesForEntries(entries))
	reg := newTestRegistry(t, snapshot)

	for _, entry := range entries {
		doc, code, err := reg.FetchDoc(context.Background(), entry.URL, "")
		require.NoError(t, err)
		require.Empty(t, code, "page %s missing after a chunked build", entry.URL)
		require.Equal(t, entry.Title, doc.Title)
	}
	search, err := reg.Search(context.Background(), "page 42", "", 5)
	require.NoError(t, err)
	require.NotEmpty(t, search.Results)
}
