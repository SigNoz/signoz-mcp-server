package docs

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	"go.opentelemetry.io/otel/trace"
)

const fetchContentByteLimit = 256 * 1024
const snippetRuneLimit = 200

// indexBatchSize bounds how many analysed documents a build holds in one
// bleve batch. Keep 64-page batches for build-heap headroom; smaller chunks
// increase resident memory because in-memory scorch never merges.
const indexBatchSize = 64

var urlSearchTokenReplacer = strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ")

// ErrInvalidSearchQuery marks empty or whitespace-only search text.
var ErrInvalidSearchQuery = errors.New("invalid docs search query")

type invalidSearchQueryError struct {
	cause error
}

func (e *invalidSearchQueryError) Error() string {
	return e.cause.Error()
}

func (e *invalidSearchQueryError) Unwrap() error {
	return e.cause
}

func (e *invalidSearchQueryError) Is(target error) bool {
	return target == ErrInvalidSearchQuery
}

//go:embed assets/corpus.gob.gz assets/corpus.manifest.json
var embeddedAssets embed.FS

type IndexRegistry struct {
	mu sync.RWMutex
	// writeMu serializes Swap, PublishSnapshot and ApplyDelta against each
	// other so no two writers mutate the live index or the current pointer
	// concurrently. Readers only take mu.
	writeMu sync.Mutex
	current *IndexEntry
	closed  atomic.Bool
	// ready flips to true only after a Swap with a real corpus succeeds.
	// NewIndexRegistry(ctx, snapshot) sets ready=true because the caller
	// supplied the corpus up-front; NewPlaceholderRegistry leaves it false
	// so handlers return INDEX_NOT_READY until the async boot build lands.
	ready atomic.Bool
}

type IndexEntry struct {
	idx      bleve.Index
	handle   *indexHandle
	snapshot CorpusSnapshot
	// fetchedAt serves last_fetched_at from the published snapshot. Content
	// gating leaves unchanged pages un-reindexed, so the stored field can lag
	// behind a successful revalidation; the snapshot never does.
	fetchedAt  map[string]time.Time
	refs       int64
	generation uint64
}

func fetchedAtIndex(snapshot CorpusSnapshot) map[string]time.Time {
	out := make(map[string]time.Time, len(snapshot.Pages))
	for _, page := range snapshot.Pages {
		if !page.FetchedAt.IsZero() {
			out[page.URL] = page.FetchedAt
		}
	}
	return out
}

// indexHandle owns one bleve index. Several IndexEntry generations may share
// a handle when only the snapshot metadata changed (PublishSnapshot) or when a
// delta was applied to the live index; the index is closed when the last
// entry referencing it drains.
type indexHandle struct {
	idx     bleve.Index
	entries int64
	// incrementalApplies counts the deltas applied to this live index.
	// In-memory scorch never merges segments, so the count bounds how long a
	// handle may accumulate segments before a rebuild compacts it.
	incrementalApplies int64
}

func newIndexEntry(idx bleve.Index, snapshot CorpusSnapshot, generation uint64) *IndexEntry {
	handle := &indexHandle{idx: idx, entries: 1}
	return &IndexEntry{idx: idx, handle: handle, snapshot: snapshot, fetchedAt: fetchedAtIndex(snapshot), generation: generation}
}

// shareIndex returns a new entry that serves snapshot from the same live index
// as entry. The caller must hold r.mu so the handle cannot drain concurrently.
func (e *IndexEntry) shareIndex(snapshot CorpusSnapshot) *IndexEntry {
	atomic.AddInt64(&e.handle.entries, 1)
	return &IndexEntry{idx: e.idx, handle: e.handle, snapshot: snapshot, fetchedAt: fetchedAtIndex(snapshot), generation: e.generation + 1}
}

func NewIndexRegistry(ctx context.Context, snapshot CorpusSnapshot) (*IndexRegistry, error) {
	idx, err := BuildIndex(snapshot)
	if err != nil {
		return nil, err
	}
	reg := &IndexRegistry{}
	reg.current = newIndexEntry(idx, snapshot, 1)
	reg.ready.Store(true)
	go func() {
		<-ctx.Done()
		reg.Close(context.Background())
	}()
	return reg, nil
}

// NewPlaceholderRegistry returns an IndexRegistry whose Ready() is false until
// the first successful Swap. Intended for async boot flows that want the
// *IndexRegistry pointer available immediately for handler wiring while the
// real corpus is still being assembled.
func NewPlaceholderRegistry(ctx context.Context) (*IndexRegistry, error) {
	idx, err := BuildIndex(EmptyCorpus())
	if err != nil {
		return nil, err
	}
	reg := &IndexRegistry{}
	reg.current = newIndexEntry(idx, EmptyCorpus(), 0)
	// ready intentionally left false.
	go func() {
		<-ctx.Done()
		reg.Close(context.Background())
	}()
	return reg, nil
}

func (r *IndexRegistry) Ready() bool {
	return r != nil && !r.closed.Load() && r.ready.Load()
}

func (r *IndexRegistry) Snapshot() (CorpusSnapshot, bool) {
	entry, release, ok := r.acquire()
	if !ok {
		return CorpusSnapshot{}, false
	}
	defer release()
	return entry.snapshot, true
}

func (r *IndexRegistry) Swap(ctx context.Context, snapshot CorpusSnapshot) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.swapLocked(ctx, snapshot)
}

func (r *IndexRegistry) swapLocked(ctx context.Context, snapshot CorpusSnapshot) error {
	if r.closed.Load() {
		return fmt.Errorf("docs index registry is closed")
	}
	idx, err := BuildIndex(snapshot)
	if err != nil {
		return err
	}
	r.mu.Lock()
	old := r.current
	var generation uint64 = 1
	if old != nil {
		generation = old.generation + 1
	}
	r.current = newIndexEntry(idx, snapshot, generation)
	r.mu.Unlock()
	// Only flip ready after the current pointer swap is visible, so any
	// Ready()==true observer is guaranteed to see the new entry via acquire().
	r.ready.Store(true)
	if old != nil {
		go closeWhenDrained(ctx, old)
	}
	return nil
}

// PublishSnapshot replaces the served snapshot metadata (sitemap, hashes,
// page records) without touching the live bleve index. It is the cheap path
// for a refresh whose page contents all matched what is already indexed.
// It falls back to Swap when there is no current index to share.
func (r *IndexRegistry) PublishSnapshot(ctx context.Context, snapshot CorpusSnapshot) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.closed.Load() {
		return fmt.Errorf("docs index registry is closed")
	}
	r.mu.Lock()
	old := r.current
	if old == nil {
		r.mu.Unlock()
		return r.swapLocked(ctx, snapshot)
	}
	r.current = old.shareIndex(snapshot)
	r.mu.Unlock()
	r.ready.Store(true)
	go closeWhenDrained(ctx, old)
	return nil
}

// deltaApplyMaxFraction caps how much of the corpus may change before a delta
// stops being cheaper than a rebuild. maxIncrementalApplies caps how many
// deltas one live index may accumulate; the next refresh rebuilds instead,
// which is the only compaction an in-memory scorch index gets.
const (
	deltaApplyMaxFraction = 0.25
	maxIncrementalApplies = 24
)

// applyDeltaBeforePublish is a test seam that runs after the batch commits
// and before the new generation is published. Nil in production.
var applyDeltaBeforePublish func()

// CanApplyDelta reports whether delta is small enough, and the live index
// young enough, to update in place instead of rebuilding.
func (r *IndexRegistry) CanApplyDelta(delta snapshotDelta, nextPages int) bool {
	if r == nil || r.closed.Load() || nextPages <= 0 || delta.empty() {
		return false
	}
	touched := len(delta.added) + len(delta.changed) + len(delta.removed)
	if float64(touched) > deltaApplyMaxFraction*float64(nextPages) {
		return false
	}
	r.mu.RLock()
	entry := r.current
	r.mu.RUnlock()
	if entry == nil {
		return false
	}
	return atomic.LoadInt64(&entry.handle.incrementalApplies) < maxIncrementalApplies
}

// ApplyDelta indexes the added and changed pages and deletes the removed ones
// on the live index, then publishes next over the same handle. It avoids the
// rebuild peak entirely at the cost of one extra scorch segment.
func (r *IndexRegistry) ApplyDelta(ctx context.Context, next CorpusSnapshot, delta snapshotDelta) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.closed.Load() {
		return fmt.Errorf("docs index registry is closed")
	}
	r.mu.Lock()
	old := r.current
	if old == nil {
		r.mu.Unlock()
		return r.swapLocked(ctx, next)
	}
	// Hold a reference across the batch so the handle cannot be closed under
	// us if the previous generation drains mid-apply.
	atomic.AddInt64(&old.handle.entries, 1)
	r.mu.Unlock()
	release := func() {
		if atomic.AddInt64(&old.handle.entries, -1) == 0 {
			_ = old.handle.idx.Close()
		}
	}

	batch := old.idx.NewBatch()
	for _, page := range delta.added {
		canonical, doc, ok := indexDocument(page)
		if !ok {
			continue
		}
		if err := batch.Index(canonical, doc); err != nil {
			release()
			return err
		}
	}
	for _, page := range delta.changed {
		canonical, doc, ok := indexDocument(page)
		if !ok {
			continue
		}
		if err := batch.Index(canonical, doc); err != nil {
			release()
			return err
		}
	}
	for _, rawURL := range delta.removed {
		canonical, ok := CanonicalDocURL(rawURL)
		if !ok {
			continue
		}
		batch.Delete(canonical)
	}
	if err := old.idx.Batch(batch); err != nil {
		release()
		return err
	}
	if applyDeltaBeforePublish != nil {
		applyDeltaBeforePublish()
	}

	r.mu.Lock()
	r.current = old.shareIndex(next)
	r.mu.Unlock()
	atomic.AddInt64(&old.handle.incrementalApplies, 1)
	r.ready.Store(true)
	release()
	go closeWhenDrained(ctx, old)
	return nil
}

func (r *IndexRegistry) Close(ctx context.Context) {
	if r == nil {
		return
	}
	// Serialize with writers so an in-flight Swap or ApplyDelta cannot publish
	// a new generation after the current one has been detached and drained,
	// which would leak a fresh index or drain a shared handle twice.
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if !r.closed.CompareAndSwap(false, true) {
		return
	}
	r.mu.Lock()
	current := r.current
	r.current = nil
	r.mu.Unlock()
	if current != nil {
		closeWhenDrained(ctx, current)
	}
}

func (r *IndexRegistry) Search(ctx context.Context, query, sectionSlug string, limit int) (SearchResponse, error) {
	entry, release, ok := r.acquire()
	if !ok {
		return SearchResponse{}, errors.New(CodeIndexNotReady)
	}
	defer release()
	if limit <= 0 {
		limit = 10
	}
	if limit > 25 {
		limit = 25
	}
	finalQuery, err := boostedDocsQuery(ctx, query)
	if err != nil {
		return SearchResponse{}, err
	}
	if sectionSlug != "" {
		sectionQuery := bleve.NewTermQuery(sectionSlug)
		sectionQuery.SetField("section_slugs")
		finalQuery = bleve.NewConjunctionQuery(finalQuery, sectionQuery)
	}
	req := bleve.NewSearchRequestOptions(finalQuery, limit, 0, false)
	req.Fields = []string{"title", "url", "section_slug", "section_breadcrumb", "section_map", "body_markdown"}
	// Use bleve's built-in highlighter + default fragmenter so snippets are
	// anchored on the matched terms rather than picked by manual substring
	// scan. We rune-trim afterwards to honor the snippetRuneLimit contract.
	req.Highlight = bleve.NewHighlight()
	req.Highlight.AddField("body")
	res, err := entry.idx.SearchInContext(ctx, req)
	if err != nil {
		return SearchResponse{}, err
	}
	// Results must be non-nil: the advertised output schema promises an
	// array, and a zero-hit search serializing "results": null violates it.
	out := SearchResponse{Query: query, TotalMatches: res.Total, Results: []SearchResult{}}
	for _, hit := range res.Hits {
		body := stringField(hit.Fields, "body_markdown")
		resultSectionSlug := stringField(hit.Fields, "section_slug")
		resultSectionBreadcrumb := stringField(hit.Fields, "section_breadcrumb")
		if sectionSlug != "" {
			if breadcrumb, ok := sectionBreadcrumbForFilter(hit.Fields, sectionSlug); ok {
				resultSectionSlug = sectionSlug
				resultSectionBreadcrumb = breadcrumb
			}
		}
		out.Results = append(out.Results, SearchResult{
			Title:             stringField(hit.Fields, "title"),
			URL:               stringField(hit.Fields, "url"),
			SectionSlug:       resultSectionSlug,
			SectionBreadcrumb: resultSectionBreadcrumb,
			Snippet:           chooseSnippet(hit.Fragments["body"], body, query, snippetRuneLimit),
			Score:             hit.Score,
		})
	}
	return out, nil
}

func sectionBreadcrumbForFilter(fields map[string]any, sectionSlug string) (string, bool) {
	raw := stringField(fields, "section_map")
	if raw == "" {
		return "", false
	}
	sectionMap := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &sectionMap); err != nil {
		return "", false
	}
	breadcrumb, ok := sectionMap[sectionSlug]
	return breadcrumb, ok
}

func boostedDocsQuery(ctx context.Context, raw string) (bleveQuery.Query, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, &invalidSearchQueryError{cause: errors.New("searchText must contain non-whitespace text")}
	}
	title := bleve.NewMatchQuery(raw)
	title.SetField("title")
	title.SetBoost(5)
	headings := bleve.NewMatchQuery(raw)
	headings.SetField("headings")
	headings.SetBoost(3)
	body := bleve.NewMatchQuery(raw)
	body.SetField("body")
	body.SetBoost(1)
	sectionBreadcrumb := bleve.NewMatchQuery(raw)
	sectionBreadcrumb.SetField("section_breadcrumb_text")
	sectionBreadcrumb.SetBoost(2.5)
	urlTokens := bleve.NewMatchQuery(raw)
	urlTokens.SetField("url_text")
	urlTokens.SetBoost(2.5)
	queryString := bleve.NewQueryStringQuery(raw)
	queryString.SetBoost(0.5)
	clauses := []bleveQuery.Query{title, headings, body, sectionBreadcrumb, urlTokens}
	if err := queryString.Validate(); err != nil {
		trace.SpanFromContext(ctx).SetAttributes(otelpkg.MCPDocsQueryStringDroppedKey.Bool(true))
	} else {
		clauses = append(clauses, queryString)
	}
	titleAll := bleve.NewMatchQuery(raw)
	titleAll.SetField("title")
	titleAll.SetOperator(bleveQuery.MatchQueryOperatorAnd)
	titleAll.SetBoost(6)
	bodyAll := bleve.NewMatchQuery(raw)
	bodyAll.SetField("body")
	bodyAll.SetOperator(bleveQuery.MatchQueryOperatorAnd)
	bodyAll.SetBoost(5)
	clauses = append(clauses, titleAll, bodyAll)
	clauses = append(clauses, glossaryClauses(raw)...)
	clauses = append(clauses, minimumMatchClauses(raw, 2, 2)...)
	return bleve.NewDisjunctionQuery(clauses...), nil
}

// Long query bags get a preference for matching most distinct non-stopword terms.
func minimumMatchClauses(raw string, titleBoost, bodyBoost float64) []bleveQuery.Query {
	tokens := docsQueryAnalyzer.Analyze([]byte(raw))
	terms := make([]string, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		term := string(token.Term)
		if !seen[term] {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	// Skip oversized expansions; the original search clauses still apply.
	if len(terms) < 4 || len(terms) > 64 {
		return nil
	}
	clauses := make([]bleveQuery.Query, 0, 2)
	for _, field := range []struct {
		name  string
		boost float64
	}{{"title", titleBoost}, {"body", bodyBoost}} {
		perTerm := make([]bleveQuery.Query, 0, len(terms))
		for _, term := range terms {
			q := bleve.NewMatchQuery(term)
			q.SetField(field.name)
			perTerm = append(perTerm, q)
		}
		q := bleve.NewDisjunctionQuery(perTerm...)
		q.SetMin(float64((3*len(terms) + 4) / 5))
		q.SetBoost(field.boost)
		clauses = append(clauses, q)
	}
	return clauses
}

func (r *IndexRegistry) FetchDoc(ctx context.Context, rawURL, heading string) (FetchResult, string, error) {
	canonical, ok := CanonicalDocURL(rawURL)
	if !ok {
		return FetchResult{}, CodeOutOfScopeURL, nil
	}
	entry, release, ok := r.acquire()
	if !ok {
		return FetchResult{}, CodeIndexNotReady, nil
	}
	defer release()
	term := bleve.NewTermQuery(canonical)
	term.SetField("url")
	req := bleve.NewSearchRequestOptions(term, 1, 0, false)
	req.Fields = []string{"title", "url", "section_slug", "section_breadcrumb", "body_markdown", "available_headings", "last_fetched_at"}
	res, err := entry.idx.SearchInContext(ctx, req)
	if err != nil {
		return FetchResult{}, "", err
	}
	if len(res.Hits) == 0 {
		return FetchResult{}, CodeDocNotFound, nil
	}
	fields := res.Hits[0].Fields
	// Non-nil for the same reason as SearchResponse.Results: the output
	// schema promises an array even for headingless or unparseable metadata.
	headings := []Heading{}
	_ = json.Unmarshal([]byte(stringField(fields, "available_headings")), &headings)
	if headings == nil {
		headings = []Heading{}
	}
	body := stringField(fields, "body_markdown")
	selectedHeading := ""
	if heading != "" {
		selected, id, found := extractHeadingSection(body, heading, headings)
		if !found {
			return FetchResult{AvailableHeadings: headings}, CodeHeadingMissing, nil
		}
		body = selected
		selectedHeading = id
	}
	content, truncation := truncateContent(body, fetchContentByteLimit)
	lastFetchedAt := newestFetchedAt(stringField(fields, "last_fetched_at"), entry.fetchedAt[stringField(fields, "url")])
	return FetchResult{
		URL:               stringField(fields, "url"),
		Title:             stringField(fields, "title"),
		SectionSlug:       stringField(fields, "section_slug"),
		SectionBreadcrumb: stringField(fields, "section_breadcrumb"),
		Content:           content,
		Heading:           selectedHeading,
		AvailableHeadings: headings,
		TruncationReason:  truncation,
		LastFetchedAt:     lastFetchedAt,
	}, "", nil
}

// newestFetchedAt reconciles the timestamp stored in the document with the
// one carried by the served snapshot. Content gating leaves an unchanged
// document's stored value behind the snapshot; a delta applied to the live
// index can briefly put a re-indexed document ahead of the entry a reader
// already holds. Whichever is newer is the truth, and the stored value is
// the fallback when the snapshot has none.
func newestFetchedAt(stored string, snapshot time.Time) string {
	if snapshot.IsZero() {
		return stored
	}
	if parsed, err := time.Parse(time.RFC3339, stored); err == nil && !snapshot.After(parsed) {
		return stored
	}
	return snapshot.UTC().Format(time.RFC3339)
}

func (r *IndexRegistry) acquire() (*IndexEntry, func(), bool) {
	if r == nil || r.closed.Load() {
		return nil, nil, false
	}
	r.mu.RLock()
	entry := r.current
	if entry != nil {
		atomic.AddInt64(&entry.refs, 1)
	}
	r.mu.RUnlock()
	if entry == nil {
		return nil, nil, false
	}
	return entry, func() { atomic.AddInt64(&entry.refs, -1) }, true
}

// closeWhenDrained waits until no search is holding a ref on entry and only
// then calls Close(). ctx.Done() signals shutdown but MUST NOT trigger
// a premature Close while refs>0 — that would race mid-query and crash
// bleve. After ctx is done we tighten the poll interval so the goroutine
// exits as soon as the in-flight readers release, respecting the request
// timeouts that bound how long they can hold a ref.
func closeWhenDrained(ctx context.Context, entry *IndexEntry) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	ctxDone := false
	for {
		if atomic.LoadInt64(&entry.refs) == 0 {
			if atomic.AddInt64(&entry.handle.entries, -1) == 0 {
				_ = entry.idx.Close()
			}
			return
		}
		if !ctxDone {
			select {
			case <-ctx.Done():
				ctxDone = true
				ticker.Reset(5 * time.Millisecond)
			case <-ticker.C:
			}
			continue
		}
		<-ticker.C
	}
}

func BuildIndex(snapshot CorpusSnapshot) (bleve.Index, error) {
	if snapshot.SchemaVersion != CorpusSchemaVersion {
		return nil, fmt.Errorf("unsupported corpus schema version %d", snapshot.SchemaVersion)
	}
	// In-memory scorch index. scorch (bleve's default on-disk backend) supports
	// a path-less in-memory mode and is dramatically more memory-efficient than
	// the legacy upsidedown+gtreap store that bleve.NewMemOnly uses: profiling
	// the 777-page corpus showed ~323 MiB resident + ~1.5 GiB allocation churn
	// per rebuild on upsidedown/gtreap, which dominated the server's footprint
	// and made the periodic docs refresh a latent OOM trigger on the
	// memory-limited multi-tenant pod.
	idx, err := bleve.NewUsing("", newIndexMapping(), scorch.Name, scorch.Name, nil)
	if err != nil {
		return nil, err
	}
	batch := idx.NewBatch()
	pending := 0
	seenURLs := make(map[string]struct{}, len(snapshot.Pages))
	for _, page := range snapshot.Pages {
		canonical, doc, ok := indexDocument(page)
		if !ok {
			continue
		}
		if _, exists := seenURLs[canonical]; exists {
			_ = idx.Close()
			return nil, fmt.Errorf("duplicate canonical docs URL %q in corpus schema %d", canonical, snapshot.SchemaVersion)
		}
		seenURLs[canonical] = struct{}{}
		if err := batch.Index(canonical, doc); err != nil {
			_ = idx.Close()
			return nil, err
		}
		pending++
		// Commit in chunks: the analysed form of every pending document stays
		// live in the batch until it does, which is what drives the peak.
		if pending >= indexBatchSize {
			if err := idx.Batch(batch); err != nil {
				_ = idx.Close()
				return nil, err
			}
			batch = idx.NewBatch()
			pending = 0
		}
	}
	if pending > 0 {
		if err := idx.Batch(batch); err != nil {
			_ = idx.Close()
			return nil, err
		}
	}
	return idx, nil
}

// indexDocument maps a page record to its bleve document. BuildIndex and
// ApplyDelta share it so an incrementally updated index is byte-identical to
// a rebuilt one. ok is false when the URL is out of scope.
func indexDocument(page PageRecord) (string, map[string]any, bool) {
	canonical, ok := CanonicalDocURL(page.URL)
	if !ok {
		return "", nil, false
	}
	sectionSlugs, sectionMap := pageSectionMetadata(page)
	sectionSlug := page.SectionSlug
	if sectionSlug == "" && len(sectionSlugs) > 0 {
		sectionSlug = sectionSlugs[0]
	}
	sectionBreadcrumb := page.SectionBreadcrumb
	if sectionBreadcrumb == "" {
		sectionBreadcrumb = sectionMap[sectionSlug]
	}
	body := page.BodyMarkdown
	headingsJSON := page.HeadingsJSON
	if headingsJSON == "" {
		headingsJSON = mustJSON(ExtractHeadings(body))
	}
	return canonical, map[string]any{
		"title":                   page.Title,
		"headings":                headingsJSON,
		"body":                    body,
		"section_breadcrumb_text": breadcrumbSearchText(sectionSlugs, sectionMap),
		"url_text":                urlSearchText(canonical),
		"section_slug":            sectionSlug,
		"section_breadcrumb":      sectionBreadcrumb,
		"section_slugs":           sectionSlugs,
		"section_map":             mustJSON(sectionMap),
		"url":                     canonical,
		"body_markdown":           body,
		"available_headings":      headingsJSON,
		"last_fetched_at":         page.FetchedAt.UTC().Format(time.RFC3339),
	}, true
}

// NormalizePages returns one PageRecord per canonical URL while retaining all
// section associations. The first record supplies stable navigation metadata;
// a fresher duplicate may replace only the fetched page payload.
func NormalizePages(pages []PageRecord) []PageRecord {
	type accumulator struct {
		page         PageRecord
		sectionSlugs []string
		sectionMap   map[string]string
		seenSections map[string]struct{}
	}
	byURL := make(map[string]*accumulator, len(pages))
	order := make([]string, 0, len(pages))

	for _, page := range pages {
		canonical, ok := CanonicalDocURL(page.URL)
		if !ok {
			continue
		}
		current, ok := byURL[canonical]
		if !ok {
			current = &accumulator{
				page:         page,
				sectionMap:   map[string]string{},
				seenSections: map[string]struct{}{},
			}
			current.page.URL = canonical
			current.page.SectionSlugs = nil
			current.page.SectionMap = nil
			byURL[canonical] = current
			order = append(order, canonical)
		} else if shouldReplaceDuplicatePayload(page, current.page) {
			replacePagePayload(&current.page, page)
		}

		sectionSlugs, sectionMap := pageSectionMetadata(page)
		for _, slug := range sectionSlugs {
			if _, exists := current.seenSections[slug]; exists {
				if current.sectionMap[slug] == "" && sectionMap[slug] != "" {
					current.sectionMap[slug] = sectionMap[slug]
				}
				continue
			}
			current.seenSections[slug] = struct{}{}
			current.sectionSlugs = append(current.sectionSlugs, slug)
			current.sectionMap[slug] = sectionMap[slug]
		}
	}

	merged := make([]PageRecord, 0, len(order))
	for _, canonical := range order {
		current := byURL[canonical]
		current.page.SectionSlugs = append([]string(nil), current.sectionSlugs...)
		current.page.SectionMap = current.sectionMap
		if current.page.SectionSlug == "" && len(current.sectionSlugs) > 0 {
			current.page.SectionSlug = current.sectionSlugs[0]
		}
		if current.page.SectionBreadcrumb == "" {
			current.page.SectionBreadcrumb = current.sectionMap[current.page.SectionSlug]
		}
		merged = append(merged, current.page)
	}
	return merged
}

func pageSectionMetadata(page PageRecord) ([]string, map[string]string) {
	seen := map[string]struct{}{}
	sectionSlugs := make([]string, 0, len(page.SectionSlugs)+1)
	sectionMap := make(map[string]string, len(page.SectionMap)+1)
	add := func(slug, breadcrumb string) {
		if slug == "" {
			return
		}
		if _, ok := seen[slug]; ok {
			if sectionMap[slug] == "" && breadcrumb != "" {
				sectionMap[slug] = breadcrumb
			}
			return
		}
		seen[slug] = struct{}{}
		sectionSlugs = append(sectionSlugs, slug)
		sectionMap[slug] = breadcrumb
	}

	add(page.SectionSlug, page.SectionBreadcrumb)
	for _, slug := range page.SectionSlugs {
		add(slug, page.SectionMap[slug])
	}
	extras := make([]string, 0, len(page.SectionMap))
	for slug := range page.SectionMap {
		if _, ok := seen[slug]; !ok && slug != "" {
			extras = append(extras, slug)
		}
	}
	sort.Strings(extras)
	for _, slug := range extras {
		add(slug, page.SectionMap[slug])
	}
	return sectionSlugs, sectionMap
}

func replacePagePayload(current *PageRecord, candidate PageRecord) {
	current.Title = candidate.Title
	current.HeadingsJSON = candidate.HeadingsJSON
	current.BodyMarkdown = candidate.BodyMarkdown
	current.FetchedAt = candidate.FetchedAt
	current.SourceETag = candidate.SourceETag
}

func breadcrumbSearchText(sectionSlugs []string, sectionMap map[string]string) string {
	seen := make(map[string]struct{}, len(sectionSlugs))
	parts := make([]string, 0, len(sectionSlugs))
	for _, slug := range sectionSlugs {
		breadcrumb := strings.TrimSpace(sectionMap[slug])
		if breadcrumb == "" {
			continue
		}
		if _, ok := seen[breadcrumb]; ok {
			continue
		}
		seen[breadcrumb] = struct{}{}
		parts = append(parts, breadcrumb)
	}
	return strings.Join(parts, " ")
}

func urlSearchText(canonical string) string {
	pathText := strings.TrimPrefix(canonical, "https://signoz.io/docs/")
	pathText = strings.Trim(pathText, "/")
	return urlSearchTokenReplacer.Replace(pathText)
}

func shouldReplaceDuplicatePayload(candidate, current PageRecord) bool {
	if current.BodyMarkdown == "" && candidate.BodyMarkdown != "" {
		return true
	}
	if candidate.FetchedAt.After(current.FetchedAt) {
		return true
	}
	return false
}

func newIndexMapping() *mapping.IndexMappingImpl {
	idxMapping := bleve.NewIndexMapping()
	docMapping := bleve.NewDocumentMapping()
	docMapping.Dynamic = false

	// Note: bleve v2 removed per-mapping Boost. Boost is applied at query time
	// via DisjunctionQuery of per-field queries where necessary (see Search).
	title := bleve.NewTextFieldMapping()
	title.Analyzer = "en"
	docMapping.AddFieldMappingsAt("title", title)

	headings := bleve.NewTextFieldMapping()
	headings.Analyzer = "en"
	docMapping.AddFieldMappingsAt("headings", headings)

	body := bleve.NewTextFieldMapping()
	body.Analyzer = "standard"
	docMapping.AddFieldMappingsAt("body", body)

	navigationText := func() *mapping.FieldMapping {
		m := bleve.NewTextFieldMapping()
		m.Analyzer = "en"
		return m
	}
	docMapping.AddFieldMappingsAt("section_breadcrumb_text", navigationText())
	docMapping.AddFieldMappingsAt("url_text", navigationText())

	keyword := func() *mapping.FieldMapping {
		m := bleve.NewKeywordFieldMapping()
		m.Store = true
		return m
	}
	docMapping.AddFieldMappingsAt("section_slug", keyword())
	docMapping.AddFieldMappingsAt("section_slugs", keyword())
	docMapping.AddFieldMappingsAt("url", keyword())

	storedText := func() *mapping.FieldMapping {
		m := bleve.NewTextFieldMapping()
		m.Index = false
		m.Store = true
		return m
	}
	docMapping.AddFieldMappingsAt("section_breadcrumb", storedText())
	docMapping.AddFieldMappingsAt("section_map", storedText())
	docMapping.AddFieldMappingsAt("body_markdown", storedText())
	docMapping.AddFieldMappingsAt("available_headings", storedText())
	docMapping.AddFieldMappingsAt("last_fetched_at", storedText())
	idxMapping.DefaultMapping = docMapping
	return idxMapping
}

func LoadEmbeddedCorpus() (CorpusSnapshot, error) {
	raw, err := embeddedAssets.ReadFile("assets/corpus.gob.gz")
	if err != nil {
		return EmptyCorpus(), err
	}
	snapshot, err := DecodeCorpus(bytes.NewReader(raw))
	if err != nil {
		return EmptyCorpus(), err
	}
	if snapshot.SchemaVersion != CorpusSchemaVersion {
		return EmptyCorpus(), fmt.Errorf("embedded corpus schema version %d != %d", snapshot.SchemaVersion, CorpusSchemaVersion)
	}
	return snapshot, nil
}

func EmptyCorpus() CorpusSnapshot {
	return CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       time.Now().UTC(),
		Pages:         []PageRecord{},
	}
}

func DecodeCorpus(r io.Reader) (CorpusSnapshot, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return CorpusSnapshot{}, err
	}
	defer func() { _ = gz.Close() }()
	var snapshot CorpusSnapshot
	if err := gob.NewDecoder(gz).Decode(&snapshot); err != nil {
		return CorpusSnapshot{}, err
	}
	return snapshot, nil
}

func EncodeCorpus(w io.Writer, snapshot CorpusSnapshot) error {
	gz := gzip.NewWriter(w)
	if err := gob.NewEncoder(gz).Encode(snapshot); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func ManifestForSnapshot(snapshot CorpusSnapshot) Manifest {
	manifest := Manifest{
		SchemaVersion: snapshot.SchemaVersion,
		BuiltAt:       snapshot.BuiltAt,
		SitemapHash:   snapshot.SitemapHash,
		Pages:         make([]ManifestEntry, 0, len(snapshot.Pages)),
	}
	for _, page := range snapshot.Pages {
		sum := sha256.Sum256([]byte(page.BodyMarkdown))
		manifest.Pages = append(manifest.Pages, ManifestEntry{
			URL:       page.URL,
			Title:     page.Title,
			SHA256:    hex.EncodeToString(sum[:]),
			FetchedAt: page.FetchedAt,
		})
	}
	return manifest
}

func SitemapHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func stringField(fields map[string]any, name string) string {
	v, ok := fields[name]
	if !ok || v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return typed
	case []string:
		return strings.Join(typed, " ")
	default:
		return fmt.Sprint(typed)
	}
}

func truncateContent(s string, maxBytes int) (string, string) {
	if len(s) <= maxBytes {
		return s, "none"
	}
	var bytesUsed int
	var out []rune
	// Range over the string directly — Go iterates by rune, avoiding a
	// []rune(s) allocation the linter flags (staticcheck SA6003).
	for _, r := range s {
		runeLen := utf8.RuneLen(r)
		if runeLen < 0 {
			runeLen = 3
		}
		if bytesUsed+runeLen > maxBytes {
			break
		}
		out = append(out, r)
		bytesUsed += runeLen
	}
	return string(out), "size"
}

// chooseSnippet prefers bleve's highlighter fragment when it produced a match,
// and falls back to the manual term-anchored scan when the query matched via
// title/headings only (no body fragment). The returned string is trimmed to
// maxRunes on a rune boundary.
func chooseSnippet(fragments []string, body, query string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	// Bleve also emits unmarked fallback fragments for fields without matches.
	for _, frag := range fragments {
		if strings.Contains(frag, "<mark>") {
			return trimToRunes(frag, maxRunes)
		}
	}
	for _, frag := range fragments {
		if strings.TrimSpace(frag) == "" {
			continue
		}
		return trimToRunes(frag, maxRunes)
	}
	return trimToRunes(makeSnippet(body, query, maxRunes), maxRunes)
}

func trimToRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func makeSnippet(body, query string, maxRunes int) string {
	bodyRunes := []rune(strings.TrimSpace(body))
	if len(bodyRunes) <= maxRunes {
		return string(bodyRunes)
	}
	center := 0
	for _, term := range strings.Fields(strings.ToLower(query)) {
		term = strings.Trim(term, "\"'`.,:;!?()[]{}")
		if len(term) < 3 {
			continue
		}
		if byteIdx := caseInsensitiveByteIndex(body, term); byteIdx >= 0 {
			center = utf8.RuneCountInString(body[:byteIdx])
			break
		}
	}
	start := center - maxRunes/2
	if start < 0 {
		start = 0
	}
	if start+maxRunes > len(bodyRunes) {
		start = len(bodyRunes) - maxRunes
	}
	snippet := strings.TrimSpace(string(bodyRunes[start : start+maxRunes]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if start+maxRunes < len(bodyRunes) {
		snippet += "..."
	}
	return snippet
}

func caseInsensitiveByteIndex(s, term string) int {
	pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(term))
	if err != nil {
		return -1
	}
	loc := pattern.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

func extractHeadingSection(body, requested string, headings []Heading) (string, string, bool) {
	target := NormalizeHeadingID(requested)
	for _, h := range headings {
		if h.ID == requested || h.ID == target || NormalizeHeadingID(h.Text) == target {
			section, ok := sectionByHeading(body, h)
			return section, h.ID, ok
		}
	}
	return "", "", false
}

func sectionByHeading(body string, heading Heading) (string, bool) {
	lines := strings.Split(body, "\n")
	start := -1
	seen := map[string]int{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, strings.Repeat("#", heading.Level)+" ") {
			continue
		}
		text := strings.TrimSpace(trimmed[heading.Level:])
		id := SlugifyHeading(text)
		if id == "" {
			continue
		}
		seen[id]++
		if seen[id] > 1 {
			id = id + "-" + strconv.Itoa(seen[id])
		}
		if id == heading.ID {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		level := 0
		for _, r := range trimmed {
			if r != '#' {
				break
			}
			level++
		}
		if level > 0 && level <= heading.Level && len(trimmed) > level && trimmed[level] == ' ' {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n")), true
}
