package main

import (
	"testing"

	"github.com/SigNoz/signoz-mcp-server/internal/docs"
)

func TestCorpusFailureThresholdExceeded(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		failures int
		notFound int
		want     bool
	}{
		{
			name:     "allows clean build",
			total:    100,
			failures: 0,
			notFound: 0,
			want:     false,
		},
		{
			name:     "allows limited non-404 failures",
			total:    100,
			failures: 10,
			notFound: 0,
			want:     false,
		},
		{
			name:     "blocks too many total failures",
			total:    100,
			failures: 11,
			notFound: 0,
			want:     true,
		},
		{
			name:     "blocks too many 404s",
			total:    100,
			failures: 0,
			notFound: 6,
			want:     true,
		},
		{
			name:     "blocks combined failures and 404s",
			total:    100,
			failures: 8,
			notFound: 3,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := corpusFailureThresholdExceeded(tt.total, tt.failures, tt.notFound)
			if got != tt.want {
				t.Fatalf("corpusFailureThresholdExceeded(%d, %d, %d) = %v, want %v", tt.total, tt.failures, tt.notFound, got, tt.want)
			}
		})
	}
}

func TestCurrentPagesReusesOnlyPagesTheManifestStillPublishes(t *testing.T) {
	pages := []docs.PageRecord{
		{URL: "https://signoz.io/docs/install.md", SourceETag: `W/"same"`},
		{URL: "https://signoz.io/docs/logs.md", SourceETag: `W/"old"`},
		{URL: "https://signoz.io/docs/removed.md", SourceETag: `W/"gone"`},
		{URL: "https://signoz.io/docs/no-etag.md"},
	}
	published := map[string]string{
		"https://signoz.io/docs/install.md": `W/"same"`,
		"https://signoz.io/docs/logs.md":    `W/"new"`,
		"https://signoz.io/docs/no-etag.md": `W/"any"`,
	}

	current := currentPages(pages, published)
	if len(current) != 1 {
		t.Fatalf("currentPages reused %d pages, want only install.md: %v", len(current), current)
	}
	if _, ok := current["https://signoz.io/docs/install.md"]; !ok {
		t.Fatalf("install.md has the published ETag and should be reused")
	}
}
