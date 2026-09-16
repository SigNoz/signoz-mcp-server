package docs

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

const (
	defaultRuntimeRefreshInterval = 6 * time.Hour
	defaultFullRefreshInterval    = 24 * time.Hour
	defaultRefreshDeadline        = 5 * time.Minute
	defaultRefreshJitter          = 30 * time.Minute
)

type RefreshConfig struct {
	SitemapURL          string
	RefreshInterval     time.Duration
	FullRefreshInterval time.Duration
	RefreshDeadline     time.Duration
	// JitterWindow is the symmetric maximum jitter applied to scheduled
	// refresh ticks so replica fleets don't herd on a common cadence. The
	// effective jitter is capped to half the scheduled interval so short
	// configured cadences do not collapse into near-tight polling loops.
	// Semantics (normalized in NewRefresher):
	//   - zero (default)    → use defaultRefreshJitter (30 min)
	//   - negative          → no jitter (tests that want determinism)
	//   - positive duration → use that value, capped per interval
	JitterWindow time.Duration
	// DisableScheduled turns off the periodic incremental refresh tick.
	// DisableFullRefresh turns off the periodic forced full refresh tick.
	// Trigger keeps working in both cases so callers can still refresh on demand.
	DisableScheduled   bool
	DisableFullRefresh bool
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
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = defaultRuntimeRefreshInterval
	}
	if cfg.FullRefreshInterval <= 0 || cfg.FullRefreshInterval < cfg.RefreshInterval {
		cfg.FullRefreshInterval = defaultFullRefreshInterval
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
// would silently lose its intent until the next scheduled tick (potentially
// 24 h away for the full-refresh cadence).
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
	if r.cfg.DisableScheduled && r.cfg.DisableFullRefresh {
		r.logger.InfoContext(ctx, "docs scheduled refresh disabled; index stays at its current build until a manual trigger")
		return
	}
	jitter := r.cfg.JitterWindow
	// A nil channel never fires, which is how a disabled schedule opts out of
	// the select below without a second code path.
	var refreshTimer, fullTimer *time.Timer
	var refreshC, fullC <-chan time.Time
	if !r.cfg.DisableScheduled {
		refreshTimer = time.NewTimer(jittered(r.cfg.RefreshInterval, jitter))
		defer refreshTimer.Stop()
		refreshC = refreshTimer.C
	} else {
		r.logger.InfoContext(ctx, "docs incremental refresh schedule disabled")
	}
	if !r.cfg.DisableFullRefresh {
		fullTimer = time.NewTimer(jittered(r.cfg.FullRefreshInterval, jitter))
		defer fullTimer.Stop()
		fullC = fullTimer.C
	} else {
		r.logger.InfoContext(ctx, "docs full refresh schedule disabled")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-refreshC:
			if err := r.Trigger(ctx, false); err != nil {
				r.logger.WarnContext(ctx, "docs refresh failed", "error", err)
			}
			refreshTimer.Reset(jittered(r.cfg.RefreshInterval, jitter))
		case <-fullC:
			if err := r.Trigger(ctx, true); err != nil {
				r.logger.WarnContext(ctx, "docs full refresh failed", "error", err)
			}
			fullTimer.Reset(jittered(r.cfg.FullRefreshInterval, jitter))
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
	sitemap := r.fetcher.Fetch(ctx, r.cfg.SitemapURL)
	if sitemap.Status != FetchStatusOK {
		return fmt.Errorf("fetch sitemap: %v", sitemap.Err)
	}
	hash := SitemapHash(sitemap.Body)
	if !forced && current.SitemapHash == hash {
		outcome = "no-op"
		r.logger.InfoContext(ctx, "docs refresh no-op; sitemap unchanged")
		return nil
	}
	entries, err := ParseSitemapMarkdown(sitemap.Body)
	if err != nil {
		outcome = "parse-error"
		if r.meters != nil {
			r.meters.DocsSitemapFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", "parse")))
		}
		return fmt.Errorf("parse sitemap: %w", err)
	}
	r.logger.InfoContext(ctx, "docs refresh fetching pages", "forced", forced, "entries", len(entries))
	next, blocked, err := r.buildSnapshot(ctx, sitemap.Body, hash, entries, current, forced)
	if err != nil {
		return err
	}
	if blocked {
		outcome = "threshold-blocked"
		r.logger.WarnContext(ctx, "docs refresh blocked by failure threshold; keeping last-good index")
		return fmt.Errorf("docs refresh blocked by failure threshold")
	}
	delta := diffSnapshots(current, next)
	if delta.empty() {
		// Nothing indexed would change; publish the new sitemap and page
		// metadata so the next tick hits the hash no-op, but keep the live
		// index and skip the rebuild peak entirely.
		if err := r.registry.PublishSnapshot(ctx, next); err != nil {
			return err
		}
		if forced {
			outcome = "forced-unchanged"
		} else {
			outcome = "unchanged"
		}
		r.logger.InfoContext(ctx, "docs refresh found no page changes; index kept", "forced", forced, "pages", len(next.Pages))
		return nil
	}
	r.logger.InfoContext(ctx, "docs refresh rebuilding index", "forced", forced, "pages", len(next.Pages),
		"added", len(delta.added), "changed", len(delta.changed), "removed", len(delta.removed))
	if err := r.registry.Swap(ctx, next); err != nil {
		return err
	}
	if forced {
		outcome = "forced-full-rebuilt"
		r.logger.InfoContext(ctx, "docs forced full refresh rebuilt index", "pages", len(next.Pages))
	} else {
		outcome = "rebuilt"
		r.logger.InfoContext(ctx, "docs refresh rebuilt index", "pages", len(next.Pages))
	}
	return nil
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

func (r *Refresher) buildSnapshot(ctx context.Context, sitemapRaw, sitemapHash string, entries []SitemapEntry, previous CorpusSnapshot, forced bool) (CorpusSnapshot, bool, error) {
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

	type item struct {
		entry SitemapEntry
		fetch PageFetch
	}
	results := make([]item, len(entries))
	// A scheduled refresh revalidates with the stored ETag so unchanged pages
	// cost a 304 instead of a body. A forced refresh re-fetches everything.
	conditional, canRevalidate := r.fetcher.(conditionalFetcher)
	canRevalidate = canRevalidate && !forced
	g, ctx := errgroup.WithContext(ctx)
	for i, entry := range entries {
		i, entry := i, entry
		g.Go(func() error {
			var fetch PageFetch
			if etag := priorETag(entry, priorByURL); canRevalidate && etag != "" {
				fetch = conditional.FetchConditional(ctx, entry.URL, etag)
			} else {
				fetch = r.fetcher.Fetch(ctx, entry.URL)
			}
			results[i] = item{entry: entry, fetch: fetch}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return CorpusSnapshot{}, false, err
	}

	failures := 0 // non-OK + non-404 (errors, out-of-scope, etc.)
	notFound := 0 // 404s this cycle (any — new or already-tombstoned)
	newNotFound := 0
	r.mu.Lock()
	for _, result := range results {
		switch result.fetch.Status {
		case FetchStatusOK, FetchStatusNotModified:
			r.notFoundCounts[result.entry.URL] = 0
		case FetchStatusNotFound:
			notFound++
			// "New" = this URL has not been 404ing in previous cycles. The
			// notFoundCounts map tracks consecutive 404 runs; a zero count
			// here means this is the first refresh to see a 404 for the URL.
			if r.notFoundCounts[result.entry.URL] == 0 {
				newNotFound++
			}
			r.notFoundCounts[result.entry.URL]++
		default:
			failures++
		}
	}
	r.mu.Unlock()
	total := len(entries)
	if total == 0 {
		return CorpusSnapshot{}, false, fmt.Errorf("cannot build docs snapshot from empty sitemap")
	}
	// Plan gates: block if >10% of any-class failures OR >5% newly 404.
	// "Any-class" includes 404s so a 9% 5xx + 5% 404 combo can't sneak a
	// degraded corpus past an errors-only gate.
	totalFailures := failures + notFound
	if totalFailures*100 > total*10 || newNotFound*100 > total*5 {
		return CorpusSnapshot{}, true, nil
	}

	pages := make([]PageRecord, 0, len(entries))
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, result := range results {
		entry := result.entry
		switch result.fetch.Status {
		case FetchStatusOK:
			body := result.fetch.Body
			headings := ExtractHeadings(body)
			title := FirstHeadingTitle(body, entry.Title)
			pages = append(pages, PageRecord{
				URL:               entry.URL,
				Title:             title,
				SectionSlug:       entry.SectionSlug,
				SectionBreadcrumb: entry.SectionBreadcrumb,
				HeadingsJSON:      mustJSON(headings),
				BodyMarkdown:      body,
				FetchedAt:         result.fetch.FetchedAt,
				SourceETag:        result.fetch.ETag,
			})
		case FetchStatusNotModified:
			if page, ok := priorPageForEntry(entry, priorByURL); ok {
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
	return CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		SitemapRaw:    sitemapRaw,
		SitemapHash:   sitemapHash,
		Pages:         NormalizePages(pages),
	}, false, nil
}

func priorETag(entry SitemapEntry, priorByURL map[string]PageRecord) string {
	canonical, ok := CanonicalDocURL(entry.URL)
	if !ok {
		return ""
	}
	return priorByURL[canonical].SourceETag
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
