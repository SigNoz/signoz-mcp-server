package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	signozclient "github.com/SigNoz/signoz-mcp-server/internal/client"
	docsindex "github.com/SigNoz/signoz-mcp-server/internal/docs"
	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
	"github.com/SigNoz/signoz-mcp-server/internal/testutil/oteltest"
	otelpkg "github.com/SigNoz/signoz-mcp-server/pkg/otel"
	"github.com/SigNoz/signoz-mcp-server/pkg/util"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestDocsHandlers(t *testing.T) {
	ctx := util.SetClientSource(context.Background(), "ai-assistant")

	t.Run("index not ready", func(t *testing.T) {
		h := newTestHandler(nil)
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{"query": "docker"}))
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, docsindex.CodeIndexNotReady, result.StructuredContent.(map[string]any)["code"])
	})

	h, cleanup := newDocsTestHandler(t)
	defer cleanup()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = meterProvider.Shutdown(context.Background()) })
	meters, err := otelpkg.NewMeters(meterProvider)
	require.NoError(t, err)
	h.SetMeters(meters)

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

	t.Run("search docs requires searchText not filter", func(t *testing.T) {
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"filter": "docker collector logs",
			"limit":  5,
		}))
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Contains(t, textContent(t, result), `"searchText" is required`)
	})

	t.Run("search docs accepts canonical searchText", func(t *testing.T) {
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"searchText":   "docker collector logs",
			"section_slug": "logs-management",
			"limit":        5,
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		search := result.StructuredContent.(docsindex.SearchResponse)
		require.NotEmpty(t, search.Results)
	})

	t.Run("search docs accepts legacy query alias", func(t *testing.T) {
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"query": "docker collector logs",
			"limit": 5,
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		search := result.StructuredContent.(docsindex.SearchResponse)
		require.NotEmpty(t, search.Results)
	})

	t.Run("search docs prefers searchText over legacy query", func(t *testing.T) {
		// When both are present, the canonical searchText wins.
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"searchText": "docker collector logs",
			"query":      "",
			"limit":      5,
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		search := result.StructuredContent.(docsindex.SearchResponse)
		require.NotEmpty(t, search.Results)
	})

	t.Run("invalid query syntax falls back to matching text", func(t *testing.T) {
		result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{
			"searchText": `"docker`,
		}))
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.NotEmpty(t, result.StructuredContent.(docsindex.SearchResponse).Results)
	})

	t.Run("search cancellation preserves cause", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		result, err := h.handleSearchDocs(canceledCtx, makeToolRequest("signoz_search_docs", map[string]any{
			"searchText": "docker",
		}))
		require.NoError(t, err)
		require.Equal(t, CodeCanceled, resultCode(t, result))
	})

	t.Run("fetch deadline preserves cause", func(t *testing.T) {
		expiredCtx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer cancel()
		result, err := h.handleFetchDoc(expiredCtx, makeToolRequest("signoz_fetch_doc", map[string]any{
			"url": "/docs/install/docker/",
		}))
		require.NoError(t, err)
		require.Equal(t, CodeTimeout, resultCode(t, result))
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
		require.LessOrEqual(t, len(fetched.Content), 256*1024)

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
		text := contents[0]
		require.Equal(t, docsindex.DocsSitemapURI, text.URI)
		require.Contains(t, text.Text, "Send logs to SigNoz")
	})

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &collected))
	for _, metricName := range []string{"signoz_docs_searches_total", "signoz_docs_fetches_total"} {
		sum, found := oteltest.FindInt64SumMetric(collected, metricName)
		require.True(t, found, "%s metric not found", metricName)
		for _, point := range sum.DataPoints {
			attr, present := point.Attributes.Value(otelpkg.MCPClientSourceKey)
			require.True(t, present, "%s missing mcp.client_source", metricName)
			require.Equal(t, "ai-assistant", attr.AsString(), "%s mcp.client_source", metricName)
		}
	}
	duration, found := oteltest.FindFloat64HistogramMetric(collected, "signoz_docs_search_duration_seconds")
	require.True(t, found, "signoz_docs_search_duration_seconds metric not found")
	for _, point := range duration.DataPoints {
		attr, present := point.Attributes.Value(otelpkg.MCPClientSourceKey)
		require.True(t, present, "signoz_docs_search_duration_seconds missing mcp.client_source")
		require.Equal(t, "ai-assistant", attr.AsString(), "signoz_docs_search_duration_seconds mcp.client_source")
	}
}

// TestSearchDocs_SearchTextNotSchemaRequired pins the schema-aware-client
// contract for the docs search tool: "searchText" must be an advertised
// property but must NOT appear in the required list. The handler enforces "one
// of searchText/query is present" itself; marking searchText mcp.Required()
// would reject a legacy "query"-only call at the schema layer before the
// handler's alias fallback ever runs (the alias would be dead for validating
// clients). Mirrors TestUpdateStructs_IDNotSchemaRequired for the id-alias
// tools.
func TestSearchDocs_SearchTextNotSchemaRequired(t *testing.T) {
	h := newTestHandler(&signozclient.MockClient{})
	s := newMCPTestServer()
	h.RegisterDocsHandlers(s)

	tools := listTestTools(t, s)
	st, ok := tools["signoz_search_docs"]
	require.True(t, ok, "signoz_search_docs not registered")

	props := inputSchemaProperties(t, st.Tool)
	_, ok = props["searchText"]
	require.True(t, ok, "searchText must remain an advertised property: %#v", props)

	required := inputSchemaRequiredFields(t, st.Tool)
	require.False(t, containsString(required, "searchText"),
		"searchText must NOT be schema-required (would make the legacy query alias unusable for validating clients), got: %#v", required)
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
	large := "# Large Doc\n\n" + strings.Repeat("large content line\n", 20000)
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

func TestDocsSearchScoreTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name, query, outcome, bucket string
		canceled                     bool
	}{
		{name: "nonempty", query: "docker", outcome: "ok", bucket: "1-4"},
		{name: "empty", query: "zzzxxyynotindocs", outcome: "ok", bucket: "0"},
		{name: "syntax fallback", query: `"unclosed`, outcome: "ok", bucket: "0"},
		{name: "whitespace", query: "   ", outcome: "error"},
		{name: "canceled", query: "docker", outcome: "error", canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newDocsTestHandler(t)
			defer cleanup()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			meters, err := otelpkg.NewMeters(provider)
			require.NoError(t, err)
			h.SetMeters(meters)
			ctx := util.SetClientSource(context.Background(), "ai-assistant")
			if tc.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{"searchText": tc.query}))
			require.NoError(t, err)
			require.Equal(t, tc.outcome == "error", result.IsError)
			var collected metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(context.Background(), &collected))
			counter, found := oteltest.FindInt64SumMetric(collected, "signoz_docs_searches_total")
			require.True(t, found)
			require.Len(t, counter.DataPoints, 1)
			point := counter.DataPoints[0]
			require.EqualValues(t, 1, point.Value)
			outcome, ok := point.Attributes.Value("outcome")
			require.True(t, ok)
			require.Equal(t, tc.outcome, outcome.AsString())
			bucket, ok := point.Attributes.Value("result_count_bucket")
			require.Equal(t, tc.outcome == "ok", ok)
			require.Equal(t, tc.bucket, bucket.AsString())
			histogram, found := oteltest.FindFloat64HistogramMetric(collected, "signoz_docs_search_top_score")
			if tc.name != "nonempty" {
				require.False(t, found, "failed or empty searches must not record a score")
				return
			}
			require.True(t, found)
			require.Len(t, histogram.DataPoints, 1)
			score := histogram.DataPoints[0]
			require.EqualValues(t, 1, score.Count)
			require.Equal(t, result.StructuredContent.(docsindex.SearchResponse).Results[0].Score, score.Sum)
			source, ok := score.Attributes.Value(otelpkg.MCPClientSourceKey)
			require.True(t, ok)
			require.Equal(t, "ai-assistant", source.AsString())
		})
	}
}

func TestDocsSearchSpanAttributes(t *testing.T) {
	h, cleanup := newDocsTestHandler(t)
	defer cleanup()
	for _, tc := range []struct{ name, query, section string }{
		{"hits", "docker collector logs", "logs-management"},
		{"empty", "zzzxxyynotindocs", ""},
		{"syntax fallback", `"docker`, ""},
		{"whitespace", "   ", ""},
		{"unicode truncation", strings.Repeat("界", 300), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			ctx, span := provider.Tracer("docs-test").Start(context.Background(), "signoz_search_docs")
			result, err := h.handleSearchDocs(ctx, makeToolRequest("signoz_search_docs", map[string]any{"searchText": tc.query, "section_slug": tc.section}))
			require.NoError(t, err)
			span.End()
			spans := recorder.Ended()
			require.Len(t, spans, 1)
			attrs := map[attribute.Key]attribute.Value{}
			for _, a := range spans[0].Attributes() {
				attrs[a.Key] = a.Value
			}
			want := []rune(tc.query)
			if len(want) > 256 {
				want = want[:256]
			}
			require.Equal(t, string(want), attrs[otelpkg.MCPDocsSearchTextKey].AsString())
			require.True(t, utf8.ValidString(attrs[otelpkg.MCPDocsSearchTextKey].AsString()))
			require.Equal(t, tc.section, attrs[otelpkg.MCPDocsSectionSlugKey].AsString())
			count := 0
			if !result.IsError {
				count = len(result.StructuredContent.(docsindex.SearchResponse).Results)
			}
			require.Contains(t, attrs, otelpkg.MCPDocsResultCountKey)
			require.EqualValues(t, count, attrs[otelpkg.MCPDocsResultCountKey].AsInt64())
			dropped, hasDropped := attrs[otelpkg.MCPDocsQueryStringDroppedKey]
			require.Equal(t, tc.name == "syntax fallback", hasDropped)
			if hasDropped {
				require.True(t, dropped.AsBool())
			}
			score, ok := attrs[otelpkg.MCPDocsTopScoreKey]
			require.Equal(t, count > 0, ok)
			if count > 0 {
				require.Equal(t, result.StructuredContent.(docsindex.SearchResponse).Results[0].Score, score.AsFloat64())
			}
		})
	}
}
