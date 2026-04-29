package docs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSitemapMarkdownFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/sitemap_fixtures/basic.md")
	require.NoError(t, err)
	entries, err := ParseSitemapMarkdown(string(raw))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "https://signoz.io/docs/logs-management/overview/", entries[0].URL)
	require.Equal(t, "logs-management", entries[0].SectionSlug)
	require.Equal(t, "Logs Management > Overview", entries[0].SectionBreadcrumb)
	require.Equal(t, "https://signoz.io/docs/install/docker/", entries[1].URL)
	require.Equal(t, "install", entries[1].SectionSlug)
}

func TestParseSitemapMarkdownTopLevelOnly(t *testing.T) {
	raw, err := os.ReadFile("testdata/sitemap_fixtures/top-level-only.md")
	require.NoError(t, err)
	entries, err := ParseSitemapMarkdown(string(raw))
	require.NoError(t, err)
	require.Len(t, entries, 4)
	require.Equal(t, "https://signoz.io/docs/introduction/", entries[0].URL)
	require.Equal(t, "introduction", entries[0].SectionSlug)
	require.Equal(t, "Introduction", entries[0].SectionBreadcrumb)
}

func TestParseSitemapMarkdownEmptySection(t *testing.T) {
	raw, err := os.ReadFile("testdata/sitemap_fixtures/empty-section.md")
	require.NoError(t, err)
	entries, err := ParseSitemapMarkdown(string(raw))
	require.NoError(t, err)
	// "Deprecated Section" and "Reserved > Internal" have no docs links and
	// must be skipped without tripping the "no docs links" error.
	require.Len(t, entries, 3)
	urls := make([]string, 0, len(entries))
	for _, e := range entries {
		urls = append(urls, e.URL)
	}
	require.Contains(t, urls, "https://signoz.io/docs/logs-management/overview/")
	require.Contains(t, urls, "https://signoz.io/docs/logs-management/send-logs/")
	require.Contains(t, urls, "https://signoz.io/docs/install/docker/")
}

func TestParseSitemapMarkdownNested(t *testing.T) {
	raw, err := os.ReadFile("testdata/sitemap_fixtures/nested.md")
	require.NoError(t, err)
	entries, err := ParseSitemapMarkdown(string(raw))
	require.NoError(t, err)
	require.Len(t, entries, 4)
	var helm SitemapEntry
	for _, e := range entries {
		if e.URL == "https://signoz.io/docs/install/kubernetes/helm/" {
			helm = e
		}
	}
	require.Equal(t, "install", helm.SectionSlug)
	require.Equal(t, "Install > Kubernetes > Helm", helm.SectionBreadcrumb)
}

func TestParseSitemapMarkdownEmptyInputIsError(t *testing.T) {
	_, err := ParseSitemapMarkdown("# Only a heading\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no docs links")
}

func TestParseSitemapMarkdownOutOfScopeFails(t *testing.T) {
	_, err := ParseSitemapMarkdown("- [Evil](https://evil.example.com/docs/x/)\n")
	require.Error(t, err)
}
