package docs

import "time"

const CorpusSchemaVersion = 1

const (
	DefaultSitemapURL = "https://signoz.io/docs/sitemap.md"
	DocsSitemapURI    = "signoz://docs/sitemap"
)

type CorpusSnapshot struct {
	SchemaVersion int          `json:"schema_version"`
	BuiltAt       time.Time    `json:"built_at"`
	SitemapRaw    string       `json:"sitemap_raw"`
	SitemapHash   string       `json:"sitemap_hash"`
	Pages         []PageRecord `json:"pages"`
}

type PageRecord struct {
	URL               string    `json:"url"`
	Title             string    `json:"title"`
	SectionSlug       string    `json:"section_slug"`
	SectionBreadcrumb string    `json:"section_breadcrumb"`
	HeadingsJSON      string    `json:"headings_json"`
	BodyMarkdown      string    `json:"body_markdown"`
	FetchedAt         time.Time `json:"fetched_at"`
	SourceETag        string    `json:"source_etag,omitempty"`
}

type SitemapEntry struct {
	URL               string `json:"url"`
	SectionBreadcrumb string `json:"section_breadcrumb"`
	SectionSlug       string `json:"section_slug"`
	Title             string `json:"title"`
}

type Heading struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level int    `json:"level"`
}

type SearchResult struct {
	Title             string  `json:"title"`
	URL               string  `json:"url"`
	SectionSlug       string  `json:"section_slug"`
	SectionBreadcrumb string  `json:"section_breadcrumb"`
	Snippet           string  `json:"snippet"`
	Score             float64 `json:"score"`
}

type SearchResponse struct {
	Results      []SearchResult `json:"results"`
	Query        string         `json:"query"`
	TotalMatches uint64         `json:"total_matches"`
}

type FetchResult struct {
	URL               string    `json:"url"`
	Title             string    `json:"title"`
	SectionSlug       string    `json:"section_slug"`
	SectionBreadcrumb string    `json:"section_breadcrumb"`
	Content           string    `json:"content"`
	Heading           string    `json:"heading,omitempty"`
	AvailableHeadings []Heading `json:"available_headings"`
	TruncationReason  string    `json:"truncation_reason"`
	LastFetchedAt     string    `json:"last_fetched_at"`
}

type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	BuiltAt       time.Time       `json:"built_at"`
	SitemapHash   string          `json:"sitemap_hash"`
	Pages         []ManifestEntry `json:"pages"`
}

type ManifestEntry struct {
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	SHA256    string    `json:"sha256"`
	FetchedAt time.Time `json:"fetched_at"`
}

type FetchStatus string

const (
	FetchStatusOK         FetchStatus = "ok"
	FetchStatusNotFound   FetchStatus = "not_found"
	FetchStatusOutOfScope FetchStatus = "out_of_scope"
	FetchStatusError      FetchStatus = "error"
)

type PageFetch struct {
	Status      FetchStatus
	URL         string
	Body        string
	ETag        string
	FetchedAt   time.Time
	StatusCode  int
	FinalURL    string
	Err         error
	RetryCount  int
	RetryStatus int
}
