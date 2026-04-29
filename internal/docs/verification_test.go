package docs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SigNoz/signoz-mcp-server/internal/config"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/goleak"
)

const corpusBlobSizeLimit = 2 * 1024 * 1024

func TestCorpusBlobSizeLimit(t *testing.T) {
	// Plan item 24: the embedded corpus blob must stay under 2 MB so the
	// binary growth from //go:embed stays bounded. This is the cheapest of
	// the plan's size-budget gates to automate; the binary-growth and steady
	// RSS gates remain operational checks.
	stat, err := os.Stat("assets/corpus.gob.gz")
	require.NoError(t, err)
	require.LessOrEqual(t, stat.Size(), int64(corpusBlobSizeLimit),
		"internal/docs/assets/corpus.gob.gz is %d bytes; over the %d byte plan budget. Regenerate via go run ./cmd/build-docs-index and audit whether the docs corpus unexpectedly grew.",
		stat.Size(), corpusBlobSizeLimit)
}

func TestCorpusManifestMatches(t *testing.T) {
	snapshot, err := LoadEmbeddedCorpus()
	require.NoError(t, err)

	raw, err := os.ReadFile("assets/corpus.manifest.json")
	require.NoError(t, err)
	var manifest Manifest
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, manifest.SchemaVersion, snapshot.SchemaVersion)
	require.Equal(t, manifest.SitemapHash, snapshot.SitemapHash)
	require.Len(t, manifest.Pages, len(snapshot.Pages))

	for i, page := range snapshot.Pages {
		sum := sha256.Sum256([]byte(page.BodyMarkdown))
		require.Equal(t, manifest.Pages[i].URL, page.URL)
		require.Equal(t, manifest.Pages[i].Title, page.Title)
		require.Equal(t, manifest.Pages[i].SHA256, hex.EncodeToString(sum[:]), page.URL)
	}
}

func TestSchemaVersionGuard(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	snapshot := testSnapshot()
	snapshot.SchemaVersion = CorpusSchemaVersion + 1
	_, err := NewIndexRegistry(context.Background(), snapshot)
	require.ErrorContains(t, err, "unsupported corpus schema version")
}

func TestRefreshNoOp(t *testing.T) {
	verifyNoDocsLeaks(t)
	snapshot := testSnapshot()
	snapshot.SitemapHash = SitemapHash(snapshot.SitemapRaw)
	reg := newTestRegistry(t, snapshot)
	reader, meters := newDocsTestMeters(t)

	srv := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/docs/sitemap.md", r.URL.Path)
		_, _ = w.Write([]byte(snapshot.SitemapRaw))
	}))
	defer srv.Close()

	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(srv), RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.SetMeters(meters)
	require.NoError(t, refresher.Trigger(context.Background(), false))
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "no-op"))
}

func TestForcedFullRefresh(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries := []SitemapEntry{{URL: "https://signoz.io/docs/install/docker/", Title: "Install SigNoz Using Docker", SectionSlug: "install", SectionBreadcrumb: "Install > Docker"}}
	initial := snapshotForEntries(entries, map[string]string{entries[0].URL: "# Old\n"})
	initial.SitemapHash = SitemapHash(sitemapForEntries(entries))
	reg := newTestRegistry(t, initial)
	reader, meters := newDocsTestMeters(t)
	srv := docsCorpusServer(t, sitemapForEntries(entries), map[string]PageFetch{
		"/docs/install/docker/": {Status: FetchStatusOK, Body: "# Install SigNoz Using Docker\n\nDocker Compose setup.\n"},
	})
	defer srv.Close()

	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(srv), RefreshConfig{
		SitemapURL:          DefaultSitemapURL,
		RefreshInterval:     time.Hour,
		FullRefreshInterval: time.Minute,
	})
	refresher.SetMeters(meters)
	require.NoError(t, refresher.Trigger(context.Background(), true))
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "forced-full-rebuilt"))
	snapshot, ok := reg.Snapshot()
	require.True(t, ok)
	require.Contains(t, snapshot.Pages[0].BodyMarkdown, "Docker Compose")

	metrics := collectDocsMetrics(t, reader)
	require.Equal(t, int64(1), int64GaugeValue(t, metrics, "signoz_docs_index_doc_count"))
	require.Equal(t, int64(2), int64GaugeValue(t, metrics, "signoz_docs_index_generation"))
	require.Equal(t, approximateCorpusSizeBytes(snapshot), int64GaugeValue(t, metrics, "signoz_docs_index_size_bytes"))
	require.Less(t, float64GaugeValue(t, metrics, "signoz_docs_index_age_seconds"), 10.0)
}

func TestSingleflightSerialization(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries := []SitemapEntry{{URL: "https://signoz.io/docs/install/docker/", Title: "Install SigNoz Using Docker", SectionSlug: "install", SectionBreadcrumb: "Install > Docker"}}
	initial := snapshotForEntries(entries, map[string]string{entries[0].URL: "# Old\n"})
	initial.SitemapHash = "old"
	reg := newTestRegistry(t, initial)
	var sitemapFetches atomic.Int64
	release := make(chan struct{})
	srv := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/sitemap.md" {
			sitemapFetches.Add(1)
			<-release
			_, _ = w.Write([]byte(sitemapForEntries(entries)))
			return
		}
		_, _ = w.Write([]byte("# Install SigNoz Using Docker\n\nDocker Compose setup.\n"))
	}))
	defer srv.Close()

	reader, meters := newDocsTestMeters(t)
	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(srv), RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.SetMeters(meters)
	errs := make(chan error, 2)
	go func() { errs <- refresher.Trigger(context.Background(), false) }()
	require.Eventually(t, func() bool { return sitemapFetches.Load() == 1 }, time.Second, 10*time.Millisecond)
	go func() { errs <- refresher.Trigger(context.Background(), true) }()
	time.Sleep(25 * time.Millisecond)
	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	// Plan: forced intent must be preserved across singleflight share. The
	// incremental trigger rebuilds once; the forced trigger joined mid-flight,
	// so the Do-owner must re-run the refresh with forced=true after its
	// first pass — producing a second sitemap fetch and a forced outcome.
	require.Equal(t, int64(2), sitemapFetches.Load())
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "rebuilt"))
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "forced-full-rebuilt"))
}

func TestRefreshFailure(t *testing.T) {
	verifyNoDocsLeaks(t)
	reg := newTestRegistry(t, testSnapshot())
	reader, meters := newDocsTestMeters(t)
	srv := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(srv), RefreshConfig{SitemapURL: DefaultSitemapURL})
	refresher.SetMeters(meters)
	require.Error(t, refresher.Trigger(context.Background(), false))
	search, err := reg.Search(context.Background(), "docker", "", 3)
	require.NoError(t, err)
	require.NotEmpty(t, search.Results)
	require.Equal(t, int64(1), docsRefreshMetricValue(t, reader, "error"))
}

func TestRecordDocsIndexMetrics(t *testing.T) {
	verifyNoDocsLeaks(t)
	reg := newTestRegistry(t, testSnapshot())
	reader, meters := newDocsTestMeters(t)

	snapshot := testSnapshot()
	snapshot.BuiltAt = time.Now().UTC().Add(-2 * time.Hour)
	require.NoError(t, reg.Swap(context.Background(), snapshot))
	require.True(t, reg.RecordMetrics(context.Background(), meters))

	metrics := collectDocsMetrics(t, reader)
	require.Equal(t, int64(len(snapshot.Pages)), int64GaugeValue(t, metrics, "signoz_docs_index_doc_count"))
	require.Equal(t, int64(2), int64GaugeValue(t, metrics, "signoz_docs_index_generation"))
	require.Equal(t, approximateCorpusSizeBytes(snapshot), int64GaugeValue(t, metrics, "signoz_docs_index_size_bytes"))

	age := float64GaugeValue(t, metrics, "signoz_docs_index_age_seconds")
	require.GreaterOrEqual(t, age, 2*time.Hour.Seconds())
	require.Less(t, age, 2*time.Hour.Seconds()+10)
}

func TestAtomicSwapRace(t *testing.T) {
	verifyNoDocsLeaks(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := newTestRegistry(t, testSnapshot())
	defer reg.Close(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, err := reg.Search(ctx, "docker logs", "", 5)
				require.NoError(t, err)
			}
		}()
	}
	for i := 0; i < 20; i++ {
		require.NoError(t, reg.Swap(ctx, testSnapshot()))
	}
	wg.Wait()
}

func TestShutdownSemantics(t *testing.T) {
	verifyNoDocsLeaks(t)
	entries := []SitemapEntry{{URL: "https://signoz.io/docs/install/docker/", Title: "Install SigNoz Using Docker", SectionSlug: "install", SectionBreadcrumb: "Install > Docker"}}
	reg := newTestRegistry(t, snapshotForEntries(entries, map[string]string{entries[0].URL: "# Old\n"}))
	defer reg.Close(context.Background())
	started := make(chan struct{})
	srv := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/sitemap.md" {
			closeOnce(started)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		_, _ = w.Write([]byte(sitemapForEntries(entries)))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	refresher := NewRefresher(slog.Default(), reg, newRewriteFetcher(srv), RefreshConfig{
		SitemapURL:      DefaultSitemapURL,
		RefreshInterval: time.Hour,
	})
	refresher.Start(ctx)
	done := make(chan error, 1)
	go func() { done <- refresher.Trigger(ctx, false) }()
	<-started
	cancel()
	require.Error(t, <-done)
}

func TestFetcherRetryAndRedirect(t *testing.T) {
	var attempts atomic.Int64
	srv := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs/retry-after/":
			w.Header().Set("Retry-After", "1")
			if attempts.Add(1) == 1 {
				http.Error(w, "slow down", http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte("# Retry After\n"))
		case "/docs/retry-500/":
			if attempts.Add(1) <= 3 {
				http.Error(w, "try again", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("# Retry 500\n"))
		case "/docs/redirect-out/":
			http.Redirect(w, r, "https://example.com/docs/out/", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fetcher := newRewriteFetcher(srv)
	var slept []time.Duration
	fetcher.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	got := fetcher.Fetch(context.Background(), "https://signoz.io/docs/retry-after/")
	require.Equal(t, FetchStatusOK, got.Status)
	require.Equal(t, 1, got.RetryCount)
	require.Contains(t, slept, time.Second)

	attempts.Store(0)
	got = fetcher.Fetch(context.Background(), "https://signoz.io/docs/retry-500/")
	require.Equal(t, FetchStatusOK, got.Status)
	require.Equal(t, 3, got.RetryCount)
	require.Equal(t, http.StatusInternalServerError, got.RetryStatus)

	got = fetcher.Fetch(context.Background(), "https://signoz.io/docs/redirect-out/")
	require.Equal(t, FetchStatusOutOfScope, got.Status)
	require.Contains(t, got.FinalURL, "example.com")
}

func TestFetcherAllowsFractionalRequestsPerSecond(t *testing.T) {
	srv := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Slow Docs\n"))
	}))
	defer srv.Close()

	fetcher := NewFetcher(FetcherConfig{
		RequestsPerSec: 0.5,
		Transport:      rewriteTransport{handler: srv.handler},
	})
	got := fetcher.Fetch(context.Background(), "https://signoz.io/docs/slow/")

	require.Equal(t, FetchStatusOK, got.Status)
	require.Equal(t, "# Slow Docs\n", got.Body)
}

func TestConfigPrecedence(t *testing.T) {
	t.Setenv(config.SignozURL, "http://localhost:8080")
	t.Setenv(config.SignozApiKey, "test-key")
	t.Setenv(config.DocsRefreshIntervalEnv, "10m")
	t.Setenv(config.DocsFullRefreshIntervalEnv, "5m")
	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	require.Equal(t, 6*time.Hour, cfg.DocsRefreshInterval)
	require.Equal(t, 24*time.Hour, cfg.DocsFullRefreshInterval)
}

func TestJitteredCapsLargeJitterRelativeToBase(t *testing.T) {
	base := 5 * time.Minute
	for i := 0; i < 1000; i++ {
		got := jittered(base, defaultRefreshJitter)
		require.GreaterOrEqual(t, got, base/2)
		require.LessOrEqual(t, got, base+base/2)
	}
}

func TestJitteredPreservesSmallerJitterWindow(t *testing.T) {
	base := 6 * time.Hour
	jitter := 30 * time.Minute
	for i := 0; i < 1000; i++ {
		got := jittered(base, jitter)
		require.GreaterOrEqual(t, got, base-jitter)
		require.LessOrEqual(t, got, base+jitter)
	}
}

func TestJitteredDisabledForNonPositiveJitter(t *testing.T) {
	require.Equal(t, 5*time.Minute, jittered(5*time.Minute, 0))
	require.Equal(t, 5*time.Minute, jittered(5*time.Minute, -time.Minute))
}

func TestRefreshThreshold(t *testing.T) {
	entries := manyEntries(20)
	previous := snapshotForEntries(entries, bodiesForEntries(entries))
	refresher := NewRefresher(slog.Default(), newTestRegistry(t, previous), nil, RefreshConfig{})

	blockedFetches := fetchMapForEntries(entries)
	for i := 0; i < 3; i++ {
		blockedFetches[entries[i].URL] = PageFetch{Status: FetchStatusNotFound, URL: entries[i].URL}
	}
	refresher.fetcher = mapFetcher(blockedFetches)
	_, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "new", entries, previous)
	require.NoError(t, err)
	require.True(t, blocked)

	allowedFetches := fetchMapForEntries(entries)
	allowedFetches[entries[0].URL] = PageFetch{Status: FetchStatusNotFound, URL: entries[0].URL}
	refresher.fetcher = mapFetcher(allowedFetches)
	next, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "newer", entries, previous)
	require.NoError(t, err)
	require.False(t, blocked)
	require.Len(t, next.Pages, len(entries))
}

func TestRefreshThresholdPersistsNotFoundHistoryWhenBlocked(t *testing.T) {
	entries := manyEntries(20)
	previous := snapshotForEntries(entries, bodiesForEntries(entries))
	refresher := NewRefresher(slog.Default(), newTestRegistry(t, previous), nil, RefreshConfig{})

	fetches := fetchMapForEntries(entries)
	for i := 0; i < 2; i++ {
		fetches[entries[i].URL] = PageFetch{Status: FetchStatusNotFound, URL: entries[i].URL}
	}
	refresher.fetcher = mapFetcher(fetches)

	_, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "first-blocked", entries, previous)
	require.NoError(t, err)
	require.True(t, blocked)

	next, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "second-allowed", entries, previous)
	require.NoError(t, err)
	require.False(t, blocked)
	require.Len(t, next.Pages, len(entries))
}

func TestTransient404Grace(t *testing.T) {
	entries := manyEntries(20)
	previous := snapshotForEntries(entries, bodiesForEntries(entries))
	refresher := NewRefresher(slog.Default(), newTestRegistry(t, previous), nil, RefreshConfig{})
	fetches := fetchMapForEntries(entries)
	fetches[entries[0].URL] = PageFetch{Status: FetchStatusNotFound, URL: entries[0].URL}
	refresher.fetcher = mapFetcher(fetches)

	current := previous
	for i := 1; i <= 3; i++ {
		next, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), fmt.Sprintf("hash-%d", i), entries, current)
		require.NoError(t, err)
		require.False(t, blocked)
		require.Len(t, next.Pages, len(entries), "404 #%d should preserve previous page", i)
		current = next
	}
	next, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "hash-4", entries, current)
	require.NoError(t, err)
	require.False(t, blocked)
	require.Len(t, next.Pages, len(entries)-1)

	refresher = NewRefresher(slog.Default(), newTestRegistry(t, previous), nil, RefreshConfig{})
	fetches = fetchMapForEntries(entries)
	fetches[entries[0].URL] = PageFetch{Status: FetchStatusNotFound, URL: entries[0].URL}
	refresher.fetcher = mapFetcher(fetches)
	next, blocked, err = refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "hash-once", entries, previous)
	require.NoError(t, err)
	require.False(t, blocked)
	require.Len(t, next.Pages, len(entries))
}

func TestRefreshFallbackPreservesDuplicateURLSections(t *testing.T) {
	entries := manyEntries(18)
	duplicateLogs := SitemapEntry{
		URL:               "https://signoz.io/docs/duplicate-deno/",
		Title:             "Deno Logs",
		SectionSlug:       "logs-management",
		SectionBreadcrumb: "Logs Management > Deno",
	}
	duplicateMetrics := SitemapEntry{
		URL:               "https://signoz.io/docs/duplicate-deno/",
		Title:             "Deno Metrics",
		SectionSlug:       "metrics",
		SectionBreadcrumb: "Metrics > Deno",
	}
	entries = append(entries, duplicateLogs, duplicateMetrics)
	previous := snapshotForEntries(entries, bodiesForEntries(entries))
	refresher := NewRefresher(slog.Default(), newTestRegistry(t, previous), nil, RefreshConfig{})
	fetches := fetchMapForEntries(entries)
	fetches[duplicateLogs.URL] = PageFetch{Status: FetchStatusError, URL: duplicateLogs.URL, Err: fmt.Errorf("temporary fetch failure")}
	refresher.fetcher = mapFetcher(fetches)

	next, blocked, err := refresher.buildSnapshot(context.Background(), sitemapForEntries(entries), "fallback-sections", entries, previous)
	require.NoError(t, err)
	require.False(t, blocked)

	var duplicatePages []PageRecord
	for _, page := range next.Pages {
		if page.URL == duplicateLogs.URL {
			duplicatePages = append(duplicatePages, page)
		}
	}
	require.Len(t, duplicatePages, 2)
	sections := map[string]string{}
	for _, page := range duplicatePages {
		sections[page.SectionSlug] = page.SectionBreadcrumb
	}
	require.Equal(t, map[string]string{
		"logs-management": "Logs Management > Deno",
		"metrics":         "Metrics > Deno",
	}, sections)
}

func verifyNoDocsLeaks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
		)
	})
}

func newTestRegistry(t *testing.T, snapshot CorpusSnapshot) *IndexRegistry {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		reg.Close(context.Background())
	})
	return reg
}

func newDocsTestMeters(t *testing.T) (*sdkmetric.ManualReader, *otelpkg.Meters) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meters, err := otelpkg.NewMeters(provider)
	require.NoError(t, err)
	return reader, meters
}

func docsRefreshMetricValue(t *testing.T, reader *sdkmetric.ManualReader, outcome string) int64 {
	t.Helper()
	rm := collectDocsMetrics(t, reader)
	sum, ok := oteltest.FindInt64SumMetric(rm, "signoz_docs_refresh_total")
	require.True(t, ok)
	for _, dp := range sum.DataPoints {
		if attrValue(dp.Attributes, "outcome") == outcome {
			return dp.Value
		}
	}
	return 0
}

func collectDocsMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	return rm
}

func int64GaugeValue(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	gauge, ok := oteltest.FindInt64GaugeMetric(rm, name)
	require.True(t, ok, "metric %s not found", name)
	require.NotEmpty(t, gauge.DataPoints, "metric %s has no data points", name)
	return gauge.DataPoints[0].Value
}

func float64GaugeValue(t *testing.T, rm metricdata.ResourceMetrics, name string) float64 {
	t.Helper()
	gauge, ok := oteltest.FindFloat64GaugeMetric(rm, name)
	require.True(t, ok, "metric %s not found", name)
	require.NotEmpty(t, gauge.DataPoints, "metric %s has no data points", name)
	return gauge.DataPoints[0].Value
}

func attrValue(set attribute.Set, key string) string {
	for _, attr := range set.ToSlice() {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func newRewriteFetcher(server *localHTTPTestServer) *Fetcher {
	fetcher := NewFetcher(FetcherConfig{
		RequestsPerSec: 1000,
		Transport:      rewriteTransport{handler: server.handler},
	})
	fetcher.sleep = func(ctx context.Context, d time.Duration) error { return nil }
	return fetcher
}

// rewriteTransport lets http.Client make "requests" to a local handler while
// preserving the client's native redirect handling. It is deliberately a
// thin passthrough — it does NOT inspect the Location header or manually
// rewrite resp.Request — so the Fetcher's out-of-scope redirect logic must
// rely on Location-header probing and http.Client's CheckRedirect, exactly
// as it will in production.
type rewriteTransport struct {
	handler http.Handler
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	original := req.Clone(req.Context())
	rr := httptest.NewRecorder()
	t.handler.ServeHTTP(rr, req)
	resp := rr.Result()
	resp.Request = original
	return resp, nil
}

func docsCorpusServer(t *testing.T, sitemap string, pages map[string]PageFetch) *localHTTPTestServer {
	t.Helper()
	return newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs/sitemap.md" {
			_, _ = w.Write([]byte(sitemap))
			return
		}
		fetch, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch fetch.Status {
		case FetchStatusOK:
			_, _ = w.Write([]byte(fetch.Body))
		case FetchStatusNotFound:
			http.NotFound(w, r)
		default:
			http.Error(w, "error", http.StatusInternalServerError)
		}
	}))
}

type localHTTPTestServer struct {
	URL     string
	handler http.Handler
}

func (s *localHTTPTestServer) Close() {}

func newHTTPTestServer(t *testing.T, handler http.Handler) *localHTTPTestServer {
	t.Helper()
	return &localHTTPTestServer{URL: "http://127.0.0.1", handler: handler}
}

type mapFetcher map[string]PageFetch

func (m mapFetcher) Fetch(_ context.Context, rawURL string) PageFetch {
	canonical, ok := CanonicalDocURL(rawURL)
	if !ok {
		return PageFetch{Status: FetchStatusOutOfScope, URL: rawURL}
	}
	if fetch, ok := m[canonical]; ok {
		if fetch.FetchedAt.IsZero() {
			fetch.FetchedAt = time.Now()
		}
		return fetch
	}
	return PageFetch{Status: FetchStatusError, URL: canonical, Err: fmt.Errorf("missing fetch for %s", canonical)}
}

func manyEntries(n int) []SitemapEntry {
	entries := make([]SitemapEntry, 0, n)
	for i := 0; i < n; i++ {
		url := fmt.Sprintf("https://signoz.io/docs/page-%02d/", i)
		entries = append(entries, SitemapEntry{
			URL:               url,
			Title:             fmt.Sprintf("Page %02d", i),
			SectionSlug:       "test",
			SectionBreadcrumb: fmt.Sprintf("Test > Page %02d", i),
		})
	}
	return entries
}

func bodiesForEntries(entries []SitemapEntry) map[string]string {
	bodies := make(map[string]string, len(entries))
	for _, entry := range entries {
		bodies[entry.URL] = "# " + entry.Title + "\n\nBody.\n"
	}
	return bodies
}

func fetchMapForEntries(entries []SitemapEntry) map[string]PageFetch {
	fetches := make(map[string]PageFetch, len(entries))
	for _, entry := range entries {
		fetches[entry.URL] = PageFetch{Status: FetchStatusOK, URL: entry.URL, Body: "# " + entry.Title + "\n\nBody.\n", FetchedAt: time.Now()}
	}
	return fetches
}

func snapshotForEntries(entries []SitemapEntry, bodies map[string]string) CorpusSnapshot {
	pages := make([]PageRecord, 0, len(entries))
	now := time.Now().UTC()
	for _, entry := range entries {
		body := bodies[entry.URL]
		pages = append(pages, PageRecord{
			URL:               entry.URL,
			Title:             entry.Title,
			SectionSlug:       entry.SectionSlug,
			SectionBreadcrumb: entry.SectionBreadcrumb,
			HeadingsJSON:      mustJSON(ExtractHeadings(body)),
			BodyMarkdown:      body,
			FetchedAt:         now,
		})
	}
	sitemap := sitemapForEntries(entries)
	return CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       now,
		SitemapRaw:    sitemap,
		SitemapHash:   SitemapHash(sitemap),
		Pages:         pages,
	}
}

func sitemapForEntries(entries []SitemapEntry) string {
	var b strings.Builder
	b.WriteString("# SigNoz Docs\n")
	for _, entry := range entries {
		b.WriteString(fmt.Sprintf("- [%s](%s)\n", entry.Title, entry.URL))
	}
	return b.String()
}

func closeOnce(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}
