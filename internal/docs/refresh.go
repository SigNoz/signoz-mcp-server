package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/singleflight"
)

const (
	defaultRuntimeRefreshInterval = 6 * time.Hour
	defaultRefreshDeadline        = 5 * time.Minute
	defaultRefreshJitter          = 30 * time.Minute
)

type RefreshConfig struct {
	SitemapURL      string
	ManifestURL     string
	RefreshInterval time.Duration
	RefreshDeadline time.Duration
	// JitterWindow is the symmetric maximum jitter applied to scheduled
	// refresh ticks so replica fleets don't herd on a common cadence. The
	// effective jitter is capped to half the scheduled interval so short
	// configured cadences do not collapse into near-tight polling loops.
	// Semantics (normalized in NewRefresher):
	//   - zero (default)    → use defaultRefreshJitter (30 min)
	//   - negative          → no jitter (tests that want determinism)
	//   - positive duration → use that value, capped per interval
	JitterWindow     time.Duration
	DisableScheduled bool
}

type Refresher struct {
	logger         *slog.Logger
	registry       *IndexRegistry
	fetcher        pageFetcher
	cfg            RefreshConfig
	group          singleflight.Group
	meters         *otelpkg.Meters
	mu             sync.Mutex
	notFoundCounts map[string]int
	// forceRequested carries a pending force-refresh intent across a
	// singleflight share. If a forced Trigger arrives while an incremental
	// refresh is in flight, setting this flag guarantees that the running
	// Do()-owner re-runs the refresh with forced=true after its first pass
	// instead of silently swallowing the forced intent.
	forceRequested atomic.Bool
}

type pageFetcher interface {
	Fetch(context.Context, string) PageFetch
}

// conditionalFetcher is implemented by fetchers that can revalidate a page
// with If-None-Match. The refresher uses it opportunistically so test fakes
// only need Fetch.
type conditionalFetcher interface {
	FetchConditional(ctx context.Context, rawURL, etag string) PageFetch
}

type manifestFetcher interface {
	conditionalFetcher
	FetchManifest(ctx context.Context, rawURL, etag string) PageFetch
}

const fetchStatusCurrent FetchStatus = "current"

const docsManifestVersion = 1

type refreshManifest struct {
	etag        string
	sitemapETag string
	pages       map[string]string
}

func NewRefresher(logger *slog.Logger, registry *IndexRegistry, fetcher *Fetcher, cfg RefreshConfig) *Refresher {
	if logger == nil {
		logger = slog.Default()
	}
	if fetcher == nil {
		fetcher = NewFetcher(FetcherConfig{})
	}
	if cfg.SitemapURL == "" {
		cfg.SitemapURL = DefaultSitemapURL
	}
	if cfg.ManifestURL == "" {
		cfg.ManifestURL = DefaultManifestURL
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = defaultRuntimeRefreshInterval
	}
	if cfg.RefreshDeadline <= 0 {
		cfg.RefreshDeadline = defaultRefreshDeadline
	}
	// Preserve the sign distinction: zero → default, negative → explicitly
	// disable jitter. jittered() treats any <=0 value as disabled.
	if cfg.JitterWindow == 0 {
		cfg.JitterWindow = defaultRefreshJitter
	}
	return &Refresher{
		logger:         logger,
		registry:       registry,
		fetcher:        fetcher,
		cfg:            cfg,
		notFoundCounts: map[string]int{},
	}
}

func (r *Refresher) SetMeters(meters *otelpkg.Meters) {
	r.meters = meters
}

func (r *Refresher) Start(ctx context.Context) {
	go r.run(ctx)
}

// Trigger performs (or joins) a refresh. Multiple concurrent triggers share a
// single in-flight refresh via singleflight, but forced intent is preserved
// across sharing via two layers:
//
//  1. The Do-owner loops internally — if a forced caller lands while the
//     refresh is running, the owner consumes the flag and runs another pass
//     with forced=true instead of returning the (possibly no-op) result to
//     the forced caller.
//
//  2. After Do returns, THIS caller (if it was forced) checks whether the
//     flag is still set — meaning a tail-race caller set it in the narrow
//     window between the owner's last Load() and Do's cleanup, joined the
//     completing Do and received a non-forced result. In that case the
//     outer loop re-enters Do, starting a fresh singleflight cycle that
//     consumes the pending forced intent.
//
// Without the outer loop, a forced Trigger hitting that tail-race window
// would silently lose its intent.
func (r *Refresher) Trigger(ctx context.Context, forced bool) error {
	if forced {
		r.forceRequested.Store(true)
	}
	for {
		_, err, _ := r.group.Do("docs-refresh", func() (any, error) {
			for {
				effectiveForced := r.forceRequested.Swap(false)
				deadlineCtx, cancel := context.WithTimeout(ctx, r.cfg.RefreshDeadline)
				runErr := r.refresh(deadlineCtx, effectiveForced)
				cancel()
				if runErr != nil {
					return nil, runErr
				}
				if !r.forceRequested.Load() {
					return nil, nil
				}
				// Loop: a forced trigger landed after we started this
				// iteration. Re-run with forced semantics instead of
				// returning a non-forced result to the forced caller.
			}
		})
		if err != nil {
			return err
		}
		if !forced {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !r.forceRequested.Load() {
			return nil
		}
		// Tail-race: our forced intent wasn't consumed by the Do we joined.
		// Loop to enter a fresh singleflight cycle.
	}
}

func (r *Refresher) run(ctx context.Context) {
	if r.cfg.DisableScheduled {
		r.logger.InfoContext(ctx, "docs scheduled refresh disabled; index stays at its current build until a manual trigger")
		return
	}
	timer := time.NewTimer(jittered(r.cfg.RefreshInterval, r.cfg.JitterWindow))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := r.Trigger(ctx, false); err != nil {
				r.logger.WarnContext(ctx, "docs refresh failed", "error", err)
			}
			timer.Reset(jittered(r.cfg.RefreshInterval, r.cfg.JitterWindow))
		}
	}
}

// jittered applies uniform ±maxJitter to base. The effective jitter is capped
// to half of base so an oversized jitter window cannot turn a short interval
// into near-tight polling.
func jittered(base, maxJitter time.Duration) time.Duration {
	maxJitter = boundedJitter(base, maxJitter)
	if maxJitter <= 0 {
		return base
	}
	delta := rand.Int63n(int64(maxJitter*2)) - int64(maxJitter)
	return base + time.Duration(delta)
}

func boundedJitter(base, maxJitter time.Duration) time.Duration {
	if base <= 0 || maxJitter <= 0 {
		return 0
	}
	maxAllowed := base / 2
	if maxAllowed <= 0 {
		return 0
	}
	if maxJitter > maxAllowed {
		return maxAllowed
	}
	return maxJitter
}

func (r *Refresher) refresh(ctx context.Context, forced bool) error {
	start := time.Now()
	outcome := "error"
	defer func() {
		if r.meters != nil {
			attrs := metric.WithAttributes(attribute.String("outcome", outcome))
			r.meters.DocsRefreshes.Add(ctx, 1, attrs)
			r.meters.DocsRefreshDuration.Record(ctx, time.Since(start).Seconds(), attrs)
			r.registry.RecordMetrics(ctx, r.meters)
		}
	}()
	current, _ := r.registry.Snapshot()
	// Logged before any network or index work so a process killed mid-refresh
	// (for example by the cgroup OOM killer) still leaves a trace of what it
	// was doing; the completion log and meters below run only on return.
	r.logger.InfoContext(ctx, "docs refresh starting", "forced", forced, "current_pages", len(current.Pages))
	if handled, manifestOutcome, err := r.refreshFromManifest(ctx, current, forced); handled {
		outcome = manifestOutcome
		return err
	}
	sitemap := r.fetcher.Fetch(ctx, r.cfg.SitemapURL)
	if sitemap.Status != FetchStatusOK {
		return fmt.Errorf("fetch sitemap: %v", sitemap.Err)
	}
	hash := SitemapHash(sitemap.Body)
	entries, err := ParseSitemapMarkdown(sitemap.Body)
	if err != nil {
		outcome = "parse-error"
		r.recordSitemapParseFailure(ctx)
		return fmt.Errorf("parse sitemap: %w", err)
	}
	r.logger.InfoContext(ctx, "docs refresh fetching pages", "forced", forced, "entries", len(entries))
	next, blocked, err := r.buildSnapshot(ctx, sitemap.Body, hash, entries, current, forced, nil)
	if err != nil {
		return err
	}
	if blocked {
		outcome = "threshold-blocked"
		r.logger.WarnContext(ctx, "docs refresh blocked by failure threshold; keeping last-good index")
		return fmt.Errorf("docs refresh blocked by failure threshold")
	}
	outcome, err = r.publish(ctx, current, next, forced)
	return err
}

// handled is false when the site publishes no valid manifest; the caller then
// runs the sitemap-only flow. A transient manifest error fails the tick instead
// of falling back, so a cold or failing manifest never triggers a full sweep.
func (r *Refresher) refreshFromManifest(ctx context.Context, current CorpusSnapshot, forced bool) (handled bool, outcome string, err error) {
	fetcher, ok := r.fetcher.(manifestFetcher)
	if !ok {
		return false, "", nil
	}
	manifestETag := current.ManifestETag
	if forced || len(current.Pages) == 0 {
		manifestETag = ""
	}
	manifest := fetcher.FetchManifest(ctx, r.cfg.ManifestURL, manifestETag)
	switch manifest.Status {
	case FetchStatusNotModified:
		r.logger.InfoContext(ctx, "docs refresh no-op; manifest unchanged")
		return true, "manifest-unchanged", nil
	case FetchStatusOK:
	case FetchStatusNotFound, FetchStatusOutOfScope:
		r.logger.InfoContext(ctx, "docs manifest not published; refreshing from the sitemap")
		return false, "", nil
	default:
		return true, "error", fmt.Errorf("fetch docs manifest: %w", manifest.Err)
	}
	sitemapETag, published, err := ParseDocsManifest(manifest.Body)
	if err != nil {
		r.logger.WarnContext(ctx, "docs manifest invalid; refreshing from the sitemap", "error", err)
		return false, "", nil
	}

	sitemapRaw, storedSitemapETag := current.SitemapRaw, current.SitemapETag
	if sitemapRaw == "" || storedSitemapETag == "" || sitemapETag != storedSitemapETag {
		revalidate := storedSitemapETag
		if sitemapRaw == "" {
			revalidate = ""
		}
		sitemap := fetcher.FetchConditional(ctx, r.cfg.SitemapURL, revalidate)
		switch sitemap.Status {
		case FetchStatusOK:
			sitemapRaw, storedSitemapETag = sitemap.Body, sitemap.ETag
		case FetchStatusNotModified:
		default:
			return true, "error", fmt.Errorf("fetch sitemap: %v", sitemap.Err)
		}
	}
	if storedSitemapETag != sitemapETag {
		return true, "error", fmt.Errorf("docs sitemap ETag %q does not match manifest ETag %q", storedSitemapETag, sitemapETag)
	}
	entries, err := ParseSitemapMarkdown(sitemapRaw)
	if err != nil {
		r.recordSitemapParseFailure(ctx)
		return true, "parse-error", fmt.Errorf("parse sitemap: %w", err)
	}
	r.logger.InfoContext(ctx, "docs refresh reconciling with manifest", "forced", forced, "entries", len(entries), "manifest_pages", len(published))
	next, blocked, err := r.buildSnapshot(ctx, sitemapRaw, SitemapHash(sitemapRaw), entries, current, forced, &refreshManifest{
		etag: manifest.ETag, sitemapETag: sitemapETag, pages: published,
	})
	if err != nil {
		return true, "error", err
	}
	if blocked {
		r.logger.WarnContext(ctx, "docs refresh blocked by failure threshold; keeping last-good index")
		return true, "threshold-blocked", fmt.Errorf("docs refresh blocked by failure threshold")
	}
	outcome, err = r.publish(ctx, current, next, forced)
	return true, outcome, err
}

func ParseDocsManifest(raw string) (string, map[string]string, error) {
	var manifest DocsETagIndex
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return "", nil, err
	}
	if manifest.Version != docsManifestVersion {
		return "", nil, fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	if manifest.Sitemap.ETag == "" {
		return "", nil, fmt.Errorf("manifest is missing the sitemap ETag")
	}
	if len(manifest.Pages) == 0 {
		return "", nil, fmt.Errorf("manifest lists no pages")
	}
	published := make(map[string]string, len(manifest.Pages))
	for _, page := range manifest.Pages {
		canonical, ok := CanonicalDocURL(page.URL)
		if !ok || page.ETag == "" {
			return "", nil, fmt.Errorf("invalid manifest entry %q", page.URL)
		}
		published[canonical] = page.ETag
	}
	return manifest.Sitemap.ETag, published, nil
}

func (r *Refresher) recordSitemapParseFailure(ctx context.Context) {
	if r.meters != nil {
		r.meters.DocsSitemapFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", "parse")))
	}
}

func (r *Refresher) publish(ctx context.Context, current, next CorpusSnapshot, forced bool) (string, error) {
	delta := diffSnapshots(current, next)
	if delta.empty() {
		// Only the refresh metadata changed, so keep the live index.
		if err := r.registry.PublishSnapshot(ctx, next); err != nil {
			return "error", err
		}
		r.logger.InfoContext(ctx, "docs refresh found no page changes; index kept", "forced", forced, "pages", len(next.Pages))
		if forced {
			return "forced-unchanged", nil
		}
		return "unchanged", nil
	}
	// A rebuild drops the segments accumulated by in-place updates.
	if !forced && r.registry.CanApplyDelta(delta, len(next.Pages)) {
		r.logger.InfoContext(ctx, "docs refresh applying delta to live index", "pages", len(next.Pages),
			"added", len(delta.added), "changed", len(delta.changed), "removed", len(delta.removed))
		if err := r.registry.ApplyDelta(ctx, next, delta); err != nil {
			return "error", err
		}
		r.logger.InfoContext(ctx, "docs refresh applied delta", "pages", len(next.Pages))
		return "applied-delta", nil
	}
	r.logger.InfoContext(ctx, "docs refresh rebuilding index", "forced", forced, "pages", len(next.Pages),
		"added", len(delta.added), "changed", len(delta.changed), "removed", len(delta.removed))
	if err := r.registry.Swap(ctx, next); err != nil {
		return "error", err
	}
	if forced {
		r.logger.InfoContext(ctx, "docs forced full refresh rebuilt index", "pages", len(next.Pages))
		return "forced-full-rebuilt", nil
	}
	r.logger.InfoContext(ctx, "docs refresh rebuilt index", "pages", len(next.Pages))
	return "rebuilt", nil
}

// snapshotDelta lists, by canonical URL, what a candidate snapshot would
// change in the served index relative to the current one.
type snapshotDelta struct {
	added   []PageRecord
	changed []PageRecord
	removed []string
}

func (d snapshotDelta) empty() bool {
	return len(d.added) == 0 && len(d.changed) == 0 && len(d.removed) == 0
}

// diffSnapshots compares indexed content only. FetchedAt and SourceETag are
// bookkeeping and never justify re-indexing a page on their own.
func diffSnapshots(current, next CorpusSnapshot) snapshotDelta {
	currentByURL := make(map[string]PageRecord, len(current.Pages))
	for _, page := range current.Pages {
		currentByURL[page.URL] = page
	}
	var delta snapshotDelta
	seen := make(map[string]struct{}, len(next.Pages))
	for _, page := range next.Pages {
		seen[page.URL] = struct{}{}
		prior, ok := currentByURL[page.URL]
		switch {
		case !ok:
			delta.added = append(delta.added, page)
		case !pageContentEqual(prior, page):
			delta.changed = append(delta.changed, page)
		}
	}
	for _, page := range current.Pages {
		if _, ok := seen[page.URL]; !ok {
			delta.removed = append(delta.removed, page.URL)
		}
	}
	return delta
}

func pageContentEqual(a, b PageRecord) bool {
	if a.Title != b.Title || a.SectionSlug != b.SectionSlug || a.SectionBreadcrumb != b.SectionBreadcrumb {
		return false
	}
	if a.HeadingsJSON != b.HeadingsJSON || a.BodyMarkdown != b.BodyMarkdown {
		return false
	}
	if len(a.SectionSlugs) != len(b.SectionSlugs) || len(a.SectionMap) != len(b.SectionMap) {
		return false
	}
	for i := range a.SectionSlugs {
		if a.SectionSlugs[i] != b.SectionSlugs[i] {
			return false
		}
	}
	for slug, breadcrumb := range a.SectionMap {
		if b.SectionMap[slug] != breadcrumb {
			return false
		}
	}
	return true
}

func (r *Refresher) buildSnapshot(ctx context.Context, sitemapRaw, sitemapHash string, entries []SitemapEntry, previous CorpusSnapshot, forced bool, manifest *refreshManifest) (CorpusSnapshot, bool, error) {
	priorByURL := make(map[string]PageRecord, len(previous.Pages))
	for _, page := range previous.Pages {
		canonical, ok := CanonicalDocURL(page.URL)
		if !ok {
			continue
		}
		if current, ok := priorByURL[canonical]; !ok || shouldReplaceDuplicatePayload(page, current) {
			priorByURL[canonical] = page
		}
	}

	fetches := make(map[string]*PageFetch, len(entries))
	conditional, canRevalidate := r.fetcher.(conditionalFetcher)
	canRevalidate = canRevalidate && (!forced || manifest != nil)
	var wg sync.WaitGroup
	for _, entry := range entries {
		url := entry.URL
		if _, seen := fetches[url]; seen {
			continue
		}
		result := new(PageFetch)
		fetches[url] = result
		wg.Go(func() {
			etag := priorByURL[url].SourceETag
			if manifest != nil && etag != "" && etag == manifest.pages[url] {
				*result = PageFetch{Status: fetchStatusCurrent, ETag: etag, FetchedAt: time.Now()}
			} else if canRevalidate && etag != "" {
				*result = conditional.FetchConditional(ctx, url, etag)
			} else {
				*result = r.fetcher.Fetch(ctx, url)
			}
		})
	}
	wg.Wait()
	total := len(fetches)
	if total == 0 {
		return CorpusSnapshot{}, false, fmt.Errorf("cannot build docs snapshot from empty sitemap")
	}

	failures, notFound, newNotFound := 0, 0, 0
	fetched, revalidated, current, pending := 0, 0, 0, 0
	r.mu.Lock()
	defer r.mu.Unlock()
	for url, result := range fetches {
		expected := ""
		if manifest != nil {
			expected = manifest.pages[url]
			if expected != "" && expected != result.ETag {
				// A stale response cannot replace the last-good page or count as a deletion.
				result.Status = FetchStatusError
			}
		}
		switch result.Status {
		case FetchStatusOK, FetchStatusNotModified, fetchStatusCurrent:
			delete(r.notFoundCounts, url)
			switch result.Status {
			case FetchStatusOK:
				fetched++
			case FetchStatusNotModified:
				revalidated++
			default:
				current++
			}
			if manifest != nil && expected == "" {
				pending++
			}
		case FetchStatusNotFound:
			notFound++
			if r.notFoundCounts[url] == 0 {
				newNotFound++
			}
			r.notFoundCounts[url]++
			if _, indexed := priorByURL[url]; indexed && r.notFoundCounts[url] <= 3 {
				pending++
			}
		default:
			failures++
			pending++
		}
	}
	if manifest != nil && pending > 0 {
		r.logger.WarnContext(ctx, "docs manifest reconciliation incomplete; retrying on next refresh", "pending_pages", pending)
	}
	// Plan gates: block if >10% of any-class failures OR >5% newly 404.
	// "Any-class" includes 404s so a 9% 5xx + 5% 404 combo can't sneak a
	// degraded corpus past an errors-only gate.
	totalFailures := failures + notFound
	if totalFailures*100 > total*10 || newNotFound*100 > total*5 {
		return CorpusSnapshot{}, true, nil
	}

	pages := make([]PageRecord, 0, len(entries))
	for _, entry := range entries {
		result := fetches[entry.URL]
		switch result.Status {
		case FetchStatusOK:
			body := result.Body
			headings := ExtractHeadings(body)
			title := FirstHeadingTitle(body, entry.Title)
			pages = append(pages, PageRecord{
				URL:               entry.URL,
				Title:             title,
				SectionSlug:       entry.SectionSlug,
				SectionBreadcrumb: entry.SectionBreadcrumb,
				HeadingsJSON:      mustJSON(headings),
				BodyMarkdown:      body,
				FetchedAt:         result.FetchedAt,
				SourceETag:        result.ETag,
			})
		case FetchStatusNotModified, fetchStatusCurrent:
			if page, ok := priorPageForEntry(entry, priorByURL); ok {
				page.FetchedAt = result.FetchedAt
				page.Title = FirstHeadingTitle(page.BodyMarkdown, entry.Title)
				pages = append(pages, page)
			}
		case FetchStatusNotFound:
			if r.notFoundCounts[entry.URL] <= 3 {
				if page, ok := priorPageForEntry(entry, priorByURL); ok {
					pages = append(pages, page)
				}
			}
		default:
			if page, ok := priorPageForEntry(entry, priorByURL); ok {
				pages = append(pages, page)
			}
		}
	}
	normalized := NormalizePages(pages)
	r.logger.InfoContext(ctx, "docs refresh fetched pages",
		"entries", len(entries), "fetched", fetched, "revalidated", revalidated, "current", current,
		"not_found", notFound, "failed", failures, "pages", len(normalized),
		"merged_duplicates", len(pages)-len(normalized))
	next := CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		SitemapRaw:    sitemapRaw,
		SitemapHash:   sitemapHash,
		Pages:         normalized,
	}
	if manifest != nil {
		next.SitemapETag = manifest.sitemapETag
		if pending == 0 {
			next.ManifestETag = manifest.etag
		}
	}
	return next, false, nil
}

func priorPageForEntry(entry SitemapEntry, priorByURL map[string]PageRecord) (PageRecord, bool) {
	canonical, ok := CanonicalDocURL(entry.URL)
	if !ok {
		return PageRecord{}, false
	}
	page, ok := priorByURL[canonical]
	if !ok {
		return PageRecord{}, false
	}
	return projectPageToSitemapEntry(page, entry), true
}

func projectPageToSitemapEntry(page PageRecord, entry SitemapEntry) PageRecord {
	breadcrumb := entry.SectionBreadcrumb
	if breadcrumb == "" {
		_, sectionMap := pageSectionMetadata(page)
		breadcrumb = sectionMap[entry.SectionSlug]
	}
	page.URL = entry.URL
	page.SectionSlug = entry.SectionSlug
	page.SectionBreadcrumb = breadcrumb
	page.SectionSlugs = nil
	page.SectionMap = nil
	return page
}
