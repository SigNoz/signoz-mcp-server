package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestDocsHandlers(t *testing.T) {
	ctx := context.Background()

	t.Run("index not ready", func(t *testing.T) {
		h := newTestHandler(nil)
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{"query": "docker"}))
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, docsindex.CodeIndexNotReady, result.StructuredContent.(map[string]any)["code"])
	})

	h, cleanup := newDocsTestHandler(t)
	defer cleanup()

	t.Run("search section filter and snippet", func(t *testing.T) {
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"query":        "docker collector logs",
			"section_slug": "logs-management",
			"limit":        5,
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		search := result.StructuredContent.(docsindex.SearchResponse)
		require.NotEmpty(t, search.Results)
		require.Equal(t, "logs-management", search.Results[0].SectionSlug)
		require.Contains(t, strings.ToLower(search.Results[0].Snippet), "docker")
	})

	t.Run("fetch errors", func(t *testing.T) {
		result, err := h.handleFetchDoc(ctx, makeToolRequest("signoz_fetch_doc", map[string]any{"url": "https://example.com/docs/logs/"}))
		require.NoError(t, err)
		require.Equal(t, docsindex.CodeOutOfScopeURL, result.StructuredContent.(map[string]any)["code"])

		result, err = h.handleFetchDoc(ctx, makeToolRequest("signoz_fetch_doc", map[string]any{"url": "https://signoz.io/docs/missing/"}))
		require.NoError(t, err)
		require.Equal(t, docsindex.CodeDocNotFound, result.StructuredContent.(map[string]any)["code"])

		result, err = h.handleFetchDoc(ctx, makeToolRequest("signoz_fetch_doc", map[string]any{"url": "/docs/logs-management/send-logs-to-signoz/", "heading": "missing-heading"}))
		require.NoError(t, err)
		require.Equal(t, docsindex.CodeHeadingMissing, result.StructuredContent.(map[string]any)["code"])
		require.NotEmpty(t, result.StructuredContent.(map[string]any)["available_headings"])
	})

	t.Run("fetch truncation and duplicate heading disambiguation", func(t *testing.T) {
		result, err := h.handleFetchDoc(ctx, makeToolRequest("signoz_fetch_doc", map[string]any{"url": "/docs/large/"}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		fetched := result.StructuredContent.(docsindex.FetchResult)
		require.Equal(t, "size", fetched.TruncationReason)
		require.LessOrEqual(t, len(fetched.Content), 100*1024)

		result, err = h.handleFetchDoc(ctx, makeToolRequest("signoz_fetch_doc", map[string]any{"url": "/docs/duplicate-headings/", "heading": "setup-2"}))
		require.NoError(t, err)
		fetched = result.StructuredContent.(docsindex.FetchResult)
		require.Equal(t, "setup-2", fetched.Heading)
		require.Contains(t, fetched.Content, "second setup")
		require.NotContains(t, fetched.Content, "first setup")
	})

	t.Run("sitemap resource", func(t *testing.T) {
		contents, err := h.handleDocsSitemap(ctx, mcp.ReadResourceRequest{Params: mcp.ReadResourceParams{URI: docsindex.DocsSitemapURI}})
		require.NoError(t, err)
		require.Len(t, contents, 1)
		text := contents[0].(mcp.TextResourceContents)
		require.Equal(t, docsindex.DocsSitemapURI, text.URI)
		require.Contains(t, text.Text, "Send logs to SigNoz")
	})
}

func newDocsTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	h := newTestHandler(nil)
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := docsindex.NewIndexRegistry(ctx, docsHandlerSnapshot())
	require.NoError(t, err)
	h.SetDocsIndex(reg)
	return h, func() {
		cancel()
		reg.Close(context.Background())
	}
}

func docsHandlerSnapshot() docsindex.CorpusSnapshot {
	now := time.Now().UTC()
	logs := "# Send logs to SigNoz\n\n## Docker collector\n\nCollect Docker logs with the OpenTelemetry Collector and send them to SigNoz.\n"
	large := "# Large Doc\n\n" + strings.Repeat("large content line\n", 9000)
	duplicate := "# Duplicate Headings\n\n## Setup\n\nfirst setup\n\n## Setup\n\nsecond setup\n\n## Done\n\nfinished\n"
	pages := []docsindex.PageRecord{
		docsHandlerPage("https://signoz.io/docs/logs-management/send-logs-to-signoz/", "Send logs to SigNoz", "logs-management", "Logs Management > Send logs", logs, now),
		docsHandlerPage("https://signoz.io/docs/install/docker/", "Install SigNoz Using Docker", "install", "Install > Docker", "# Install SigNoz Using Docker\n\n## Start\n\nRun docker compose up.\n", now),
		docsHandlerPage("https://signoz.io/docs/large/", "Large Doc", "test", "Test > Large", large, now),
		docsHandlerPage("https://signoz.io/docs/duplicate-headings/", "Duplicate Headings", "test", "Test > Duplicate", duplicate, now),
	}
	sitemap := "- [Send logs to SigNoz](https://signoz.io/docs/logs-management/send-logs-to-signoz/)\n- [Install SigNoz Using Docker](https://signoz.io/docs/install/docker/)\n"
	return docsindex.CorpusSnapshot{
		SchemaVersion: docsindex.CorpusSchemaVersion,
		BuiltAt:       now,
		SitemapRaw:    sitemap,
		SitemapHash:   docsindex.SitemapHash(sitemap),
		Pages:         pages,
	}
}

func docsHandlerPage(url, title, section, breadcrumb, body string, fetchedAt time.Time) docsindex.PageRecord {
	return docsindex.PageRecord{
		URL:               url,
		Title:             title,
		SectionSlug:       section,
		SectionBreadcrumb: breadcrumb,
		HeadingsJSON:      mustJSONForDocsTest(docsindex.ExtractHeadings(body)),
		BodyMarkdown:      body,
		FetchedAt:         fetchedAt,
	}
}

func mustJSONForDocsTest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
