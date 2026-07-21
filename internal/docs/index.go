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

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
)

const fetchContentByteLimit = 256 * 1024
const snippetRuneLimit = 200

var urlSearchTokenReplacer = strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ")

// ErrInvalidSearchQuery marks searchText that Bleve's query-string parser
// rejects. Callers can classify it as a correctable input error without
// depending on Bleve's error text.
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
	mu      sync.RWMutex
	current *IndexEntry
	closed  atomic.Bool
	// ready flips to true only after a Swap with a real corpus succeeds.
	// NewIndexRegistry(ctx, snapshot) sets ready=true because the caller
	// supplied the corpus up-front; NewPlaceholderRegistry leaves it false
	// so handlers return INDEX_NOT_READY until the async boot build lands.
	ready atomic.Bool
}

type IndexEntry struct {
	idx        bleve.Index
	snapshot   CorpusSnapshot
	refs       int64
	generation uint64
}

func NewIndexRegistry(ctx context.Context, snapshot CorpusSnapshot) (*IndexRegistry, error) {
	idx, err := BuildIndex(snapshot)
	if err != nil {
		return nil, err
	}
	reg := &IndexRegistry{}
	reg.current = &IndexEntry{idx: idx, snapshot: snapshot, generation: 1}
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
	reg.current = &IndexEntry{idx: idx, snapshot: EmptyCorpus(), generation: 0}
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
	if r.closed.Load() {
		return fmt.Errorf("docs index registry is closed")
	}
	idx, err := BuildIndex(snapshot)
	if err != nil {
		return err
	}
	newEntry := &IndexEntry{idx: idx, snapshot: snapshot}
	r.mu.Lock()
	old := r.current
	if old != nil {
		newEntry.generation = old.generation + 1
	} else {
		newEntry.generation = 1
	}
	r.current = newEntry
	r.mu.Unlock()
	// Only flip ready after the current pointer swap is visible, so any
	// Ready()==true observer is guaranteed to see the new entry via acquire().
	r.ready.Store(true)
	if old != nil {
		go closeWhenDrained(ctx, old)
	}
	return nil
}

func (r *IndexRegistry) Close(ctx context.Context) {
	if r == nil || !r.closed.CompareAndSwap(false, true) {
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
	finalQuery, err := boostedDocsQuery(query)
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

func boostedDocsQuery(raw string) (bleveQuery.Query, error) {
	raw = strings.TrimSpace(raw)
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
	if err := queryString.Validate(); err != nil {
		return nil, &invalidSearchQueryError{cause: err}
	}
	return bleve.NewDisjunctionQuery(title, headings, body, sectionBreadcrumb, urlTokens, queryString), nil
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
	return FetchResult{
		URL:               stringField(fields, "url"),
		Title:             stringField(fields, "title"),
		SectionSlug:       stringField(fields, "section_slug"),
		SectionBreadcrumb: stringField(fields, "section_breadcrumb"),
		Content:           content,
		Heading:           selectedHeading,
		AvailableHeadings: headings,
		TruncationReason:  truncation,
		LastFetchedAt:     stringField(fields, "last_fetched_at"),
	}, "", nil
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
			_ = entry.idx.Close()
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
	seenURLs := make(map[string]struct{}, len(snapshot.Pages))
	for _, page := range snapshot.Pages {
		canonical, ok := CanonicalDocURL(page.URL)
		if !ok {
			continue
		}
		if _, exists := seenURLs[canonical]; exists {
			_ = idx.Close()
			return nil, fmt.Errorf("duplicate canonical docs URL %q in corpus schema %d", canonical, snapshot.SchemaVersion)
		}
		seenURLs[canonical] = struct{}{}
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
		doc := map[string]any{
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
		}
		if err := batch.Index(canonical, doc); err != nil {
			_ = idx.Close()
			return nil, err
		}
	}
	if err := idx.Batch(batch); err != nil {
		_ = idx.Close()
		return nil, err
	}
	return idx, nil
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
	title.Analyzer = "standard"
	docMapping.AddFieldMappingsAt("title", title)

	headings := bleve.NewTextFieldMapping()
	headings.Analyzer = "standard"
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
	for _, frag := range fragments {
		if strings.TrimSpace(frag) == "" {
			continue
		}
		return trimToRunes(frag, maxRunes)
	}
	return makeSnippet(body, query, maxRunes)
}

func trimToRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
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
