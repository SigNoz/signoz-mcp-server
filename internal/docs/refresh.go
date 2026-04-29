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
	// refresh ticks so replica fleets don't herd on a common cadence.
	// Semantics (normalized in NewRefresher):
	//   - zero (default)    → use defaultRefreshJitter (30 min)
	//   - negative          → no jitter (tests that want determinism)
	//   - positive duration → use that value
	JitterWindow time.Duration
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
	jitter := r.cfg.JitterWindow
	refreshTimer := time.NewTimer(jittered(r.cfg.RefreshInterval, jitter))
	defer refreshTimer.Stop()
	fullTimer := time.NewTimer(jittered(r.cfg.FullRefreshInterval, jitter))
	defer fullTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refreshTimer.C:
			if err := r.Trigger(ctx, false); err != nil {
				r.logger.WarnContext(ctx, "docs refresh failed", "error", err)
			}
			refreshTimer.Reset(jittered(r.cfg.RefreshInterval, jitter))
		case <-fullTimer.C:
			if err := r.Trigger(ctx, true); err != nil {
				r.logger.WarnContext(ctx, "docs full refresh failed", "error", err)
			}
			fullTimer.Reset(jittered(r.cfg.FullRefreshInterval, jitter))
		}
	}
}

// jittered applies uniform ±maxJitter to base. Clamped so the returned
// interval is never below 1 s (avoid pathological tight loops on misconfig).
func jittered(base, maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return base
	}
	delta := rand.Int63n(int64(maxJitter*2)) - int64(maxJitter)
	result := base + time.Duration(delta)
	if result < time.Second {
		return time.Second
	}
	return result
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
	next, blocked, err := r.buildSnapshot(ctx, sitemap.Body, hash, entries, current)
	if err != nil {
		return err
	}
	if blocked {
		outcome = "threshold-blocked"
		r.logger.WarnContext(ctx, "docs refresh blocked by failure threshold; keeping last-good index")
		return fmt.Errorf("docs refresh blocked by failure threshold")
	}
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

func (r *Refresher) buildSnapshot(ctx context.Context, sitemapRaw, sitemapHash string, entries []SitemapEntry, previous CorpusSnapshot) (CorpusSnapshot, bool, error) {
	prior := make(map[string]PageRecord, len(previous.Pages))
	for _, page := range previous.Pages {
		prior[page.URL] = page
	}

	type item struct {
		entry SitemapEntry
		fetch PageFetch
	}
	results := make([]item, len(entries))
	g, ctx := errgroup.WithContext(ctx)
	for i, entry := range entries {
		i, entry := i, entry
		g.Go(func() error {
			results[i] = item{entry: entry, fetch: r.fetcher.Fetch(ctx, entry.URL)}
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
		case FetchStatusOK:
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
		case FetchStatusNotFound:
			if r.notFoundCounts[entry.URL] <= 3 {
				if page, ok := prior[entry.URL]; ok {
					pages = append(pages, page)
				}
			}
		default:
			if page, ok := prior[entry.URL]; ok {
				pages = append(pages, page)
			}
		}
	}
	return CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		SitemapRaw:    sitemapRaw,
		SitemapHash:   sitemapHash,
		Pages:         pages,
	}, false, nil
}
