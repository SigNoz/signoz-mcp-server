package docs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	bq "github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestIndexSearchFetchAndSwap(t *testing.T) {
	// bleve's AnalysisQueue starts long-lived worker goroutines that don't
	// exit when an Index is closed (library-owned global). They are not a
	// real leak in our code, so filter them out of the goleak check.
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg, err := NewIndexRegistry(ctx, testSnapshot())
	require.NoError(t, err)
	defer reg.Close(context.Background())

	search, err := reg.Search(ctx, "docker logs", "", 3)
	require.NoError(t, err)
	require.NotEmpty(t, search.Results)

	fallback, err := reg.Search(ctx, `"docker`, "", 3)
	require.NoError(t, err)
	require.NotEmpty(t, fallback.Results)
	for _, empty := range []string{"", "  ", "\t\n"} {
		_, err = reg.Search(ctx, empty, "", 3)
		require.ErrorIs(t, err, ErrInvalidSearchQuery)
	}

	filtered, err := reg.Search(ctx, "docker", "install", 3)
	require.NoError(t, err)
	require.NotEmpty(t, filtered.Results)
	require.Equal(t, "install", filtered.Results[0].SectionSlug)

	doc, code, err := reg.FetchDoc(ctx, "/docs/install/docker/", "prerequisites")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Equal(t, "prerequisites", doc.Heading)
	require.Contains(t, doc.Content, "Docker")

	_, code, err = reg.FetchDoc(ctx, "https://example.com/docs/install/docker/", "")
	require.NoError(t, err)
	require.Equal(t, CodeOutOfScopeURL, code)

	err = reg.Swap(ctx, testSnapshot())
	require.NoError(t, err)
}

func TestInvalidSearchQueryErrorPreservesCauseMessage(t *testing.T) {
	cause := errors.New("parser detail")
	err := &invalidSearchQueryError{cause: cause}
	require.Equal(t, cause.Error(), err.Error())
	require.ErrorIs(t, err, ErrInvalidSearchQuery)
	require.ErrorIs(t, err, cause)
}

func TestTruncateContentIsUTF8Safe(t *testing.T) {
	content := strings.Repeat("日志🙂", 80*1024)
	truncated, reason := truncateContent(content, 256*1024)
	require.Equal(t, "size", reason)
	require.True(t, strings.HasPrefix(content, truncated))
}

func TestNormalizePagesPreservesURLSectionsForFiltering(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now().UTC()
	body := "# Deno OpenTelemetry Instrumentation\n\nSend Deno traces, logs, and metrics to SigNoz.\n"
	snapshot := CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       now,
		Pages: []PageRecord{
			{
				URL:               "https://signoz.io/docs/instrumentation/opentelemetry-deno/",
				Title:             "Deno OpenTelemetry Instrumentation",
				SectionSlug:       "apm-distributed-tracing",
				SectionBreadcrumb: "APM & Distributed Tracing > Instrument Application > Deno",
				HeadingsJSON:      mustJSON(ExtractHeadings(body)),
				BodyMarkdown:      body,
				FetchedAt:         now,
			},
			{
				URL:               "https://signoz.io/docs/instrumentation/opentelemetry-deno/",
				Title:             "Deno OpenTelemetry Instrumentation",
				SectionSlug:       "logs-management",
				SectionBreadcrumb: "Logs Management > Send Logs to SigNoz > Application Logs > Deno",
				HeadingsJSON:      mustJSON(ExtractHeadings(body)),
				BodyMarkdown:      body,
				FetchedAt:         now,
			},
			{
				URL:               "https://signoz.io/docs/instrumentation/opentelemetry-deno/",
				Title:             "Deno OpenTelemetry Instrumentation",
				SectionSlug:       "metrics",
				SectionBreadcrumb: "Metrics > Send Metrics > Application Metrics > Deno",
				HeadingsJSON:      mustJSON(ExtractHeadings(body)),
				BodyMarkdown:      body,
				FetchedAt:         now,
			},
		},
	}
	snapshot.Pages = NormalizePages(snapshot.Pages)
	require.Len(t, snapshot.Pages, 1)
	require.Equal(t, []string{"apm-distributed-tracing", "logs-management", "metrics"}, snapshot.Pages[0].SectionSlugs)
	require.Equal(t, "APM & Distributed Tracing > Instrument Application > Deno", snapshot.Pages[0].SectionMap["apm-distributed-tracing"])
	require.Equal(t, snapshot.Pages, NormalizePages(snapshot.Pages), "normalization must be idempotent")
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())

	for _, section := range []string{"apm-distributed-tracing", "logs-management", "metrics"} {
		result, err := reg.Search(ctx, "deno", section, 5)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		require.Equal(t, "https://signoz.io/docs/instrumentation/opentelemetry-deno/", result.Results[0].URL)
		require.Equal(t, section, result.Results[0].SectionSlug)
		require.Contains(t, result.Results[0].SectionBreadcrumb, "Deno")
	}

	result, err := reg.Search(ctx, "deno", "", 5)
	require.NoError(t, err)
	require.Len(t, result.Results, 1)
}

func TestNormalizePagesUsesFreshestPayloadAndStablePrimarySection(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldFetchedAt := time.Now().UTC().Add(-time.Hour)
	newFetchedAt := time.Now().UTC()
	oldBody := "# Duplicate Doc\n\nstale fallback body\n"
	newBody := "# Duplicate Doc\n\nfresh successful body\n"
	snapshot := CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       newFetchedAt,
		Pages: []PageRecord{
			{
				URL:               "https://signoz.io/docs/duplicate/",
				Title:             "Duplicate Doc",
				SectionSlug:       "logs-management",
				SectionBreadcrumb: "Logs Management > Duplicate",
				HeadingsJSON:      mustJSON(ExtractHeadings(oldBody)),
				BodyMarkdown:      oldBody,
				FetchedAt:         oldFetchedAt,
			},
			{
				URL:               "https://signoz.io/docs/duplicate/",
				Title:             "Duplicate Doc",
				SectionSlug:       "metrics",
				SectionBreadcrumb: "Metrics > Duplicate",
				HeadingsJSON:      mustJSON(ExtractHeadings(newBody)),
				BodyMarkdown:      newBody,
				FetchedAt:         newFetchedAt,
			},
		},
	}
	snapshot.Pages = NormalizePages(snapshot.Pages)
	require.Len(t, snapshot.Pages, 1)
	require.Equal(t, "logs-management", snapshot.Pages[0].SectionSlug)
	require.Equal(t, "Logs Management > Duplicate", snapshot.Pages[0].SectionBreadcrumb)
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())

	doc, code, err := reg.FetchDoc(ctx, "https://signoz.io/docs/duplicate/", "")
	require.NoError(t, err)
	require.Empty(t, code)
	require.Contains(t, doc.Content, "fresh successful body")
	require.NotContains(t, doc.Content, "stale fallback body")

	for _, section := range []string{"logs-management", "metrics"} {
		result, err := reg.Search(ctx, "fresh", section, 5)
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		require.Equal(t, section, result.Results[0].SectionSlug)
	}
}

func TestBuildIndexRejectsDuplicateCanonicalURLs(t *testing.T) {
	snapshot := testSnapshot()
	duplicate := snapshot.Pages[0]
	duplicate.URL += "?duplicate=true"
	snapshot.Pages = append(snapshot.Pages, duplicate)

	idx, err := BuildIndex(snapshot)
	if idx != nil {
		defer func() { _ = idx.Close() }()
	}
	require.ErrorContains(t, err, "duplicate canonical docs URL")
}

func TestNavigationSearchText(t *testing.T) {
	require.Equal(t, "install kubernetes aws", urlSearchText("https://signoz.io/docs/install/kubernetes/aws/"))
	require.Equal(t,
		"Install > Kubernetes Logs Management > Kubernetes",
		breadcrumbSearchText(
			[]string{"install", "logs-management", "duplicate-install"},
			map[string]string{
				"install":           "Install > Kubernetes",
				"logs-management":   "Logs Management > Kubernetes",
				"duplicate-install": "Install > Kubernetes",
			},
		),
	)
}

func TestMakeSnippetUsesOriginalOffsetsAfterUnicodeCaseFold(t *testing.T) {
	body := strings.Repeat("İ", 220) + " target appears here " + strings.Repeat("tail ", 80)

	snippet := makeSnippet(body, "target", 40)

	require.Contains(t, snippet, "target")
}

func testSnapshot() CorpusSnapshot {
	now := time.Now().UTC()
	logs := "# Logs Management Overview\n\nSigNoz can receive Docker logs and OpenTelemetry logs.\n"
	install := "# Install SigNoz Using Docker\n\n## Prerequisites\n\nDocker and Docker Compose are required.\n\n## Start\n\nRun docker compose up.\n"
	return CorpusSnapshot{
		SchemaVersion: CorpusSchemaVersion,
		BuiltAt:       now,
		SitemapRaw:    "- [Logs Management Overview](https://signoz.io/docs/logs-management/overview/)\n- [Install SigNoz Using Docker](https://signoz.io/docs/install/docker/)\n",
		SitemapHash:   "test",
		Pages: []PageRecord{
			{
				URL:               "https://signoz.io/docs/logs-management/overview/",
				Title:             "Logs Management Overview",
				SectionSlug:       "logs-management",
				SectionBreadcrumb: "Logs Management > Overview",
				HeadingsJSON:      mustJSON(ExtractHeadings(logs)),
				BodyMarkdown:      logs,
				FetchedAt:         now,
			},
			{
				URL:               "https://signoz.io/docs/install/docker/",
				Title:             "Install SigNoz Using Docker",
				SectionSlug:       "install",
				SectionBreadcrumb: "Install > Docker",
				HeadingsJSON:      mustJSON(ExtractHeadings(install)),
				BodyMarkdown:      install,
				FetchedAt:         now,
			},
		},
	}
}

func TestSnippetRuneBudget(t *testing.T) {
	body := strings.Repeat("界", 300) + " target " + strings.Repeat("文", 300)
	for _, limit := range []int{0, 1, 3, 40, snippetRuneLimit} {
		for _, fragments := range [][]string{nil, {body}} {
			snippet := chooseSnippet(fragments, body, "target", limit)
			require.LessOrEqual(t, len([]rune(snippet)), limit)
			require.True(t, utf8.ValidString(snippet))
		}
	}
}

func TestEmbeddedSearchProbeRegressions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, err := LoadEmbeddedCorpus()
	require.NoError(t, err)
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())
	for _, tc := range []struct{ query, url string }{
		{"How do I send logs from a Kubernetes cluster to SigNoz Cloud?", "https://signoz.io/docs/logs-management/send-logs-to-signoz/"},
		{"alerting on error rate", "https://signoz.io/docs/alerts-management/metrics-based-alerts/"},
		{"instrumenting a python fastapi app", "https://signoz.io/docs/instrumentation/opentelemetry-python/"},
		{"k8s otel collector helm", "https://signoz.io/docs/opentelemetry-collection-agents/k8s/k8s-infra/overview/"},
		{"nodejs express instrument", "https://signoz.io/docs/instrumentation/javascript/opentelemetry-nodejs/"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			res, err := reg.Search(ctx, tc.query, "", 3)
			require.NoError(t, err)
			var urls []string
			for _, hit := range res.Results {
				urls = append(urls, hit.URL)
			}
			require.Contains(t, urls, tc.url)
		})
	}
	res, err := reg.Search(ctx, "kubernetes node memory", "", 3)
	require.NoError(t, err)
	for _, hit := range res.Results {
		require.NotContains(t, hit.URL, "/instrumentation/javascript/")
	}
}

func TestSearchStemsTitleAndHeadingsOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := strings.Repeat("Unrelated introduction. ", 60) + " Instrumentation connects Python services to tracing."
	snapshot := EmptyCorpus()
	snapshot.Pages = []PageRecord{
		{URL: "https://signoz.io/docs/title-match/", Title: "Instrumentation guide", BodyMarkdown: body},
		{URL: "https://signoz.io/docs/heading-match/", Title: "Telemetry guide", HeadingsJSON: mustJSON([]Heading{{Text: "Instrumentation"}}), BodyMarkdown: body},
		{URL: "https://signoz.io/docs/body-only/", Title: "Telemetry guide", BodyMarkdown: body},
	}
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())
	entry, release, ok := reg.acquire()
	require.True(t, ok)
	defer release()
	exact := bleve.NewMatchQuery("instrumenting")
	exact.SetField("body")
	raw, err := entry.idx.Search(bleve.NewSearchRequest(exact))
	require.NoError(t, err)
	require.Empty(t, raw.Hits)
	res, err := reg.Search(ctx, "instrumenting", "", 3)
	require.NoError(t, err)
	require.Len(t, res.Results, 2)
	var urls []string
	for _, hit := range res.Results {
		urls = append(urls, hit.URL)
		require.NotEmpty(t, hit.Snippet)
		require.LessOrEqual(t, len([]rune(hit.Snippet)), snippetRuneLimit)
	}
	require.ElementsMatch(t, []string{"https://signoz.io/docs/title-match/", "https://signoz.io/docs/heading-match/"}, urls)
}

func TestChooseSnippetPrefersMatchedBodyFragment(t *testing.T) {
	snippet := chooseSnippet([]string{"Unmatched body introduction", "Matched <mark>instrumentation</mark> fragment"}, "", "instrumentation", snippetRuneLimit)
	require.Contains(t, snippet, "<mark>instrumentation</mark>")
	snippet = chooseSnippet([]string{"First <mark>body</mark> fragment", "Second <mark>body</mark> fragment"}, "", "body", snippetRuneLimit)
	require.Equal(t, "First <mark>body</mark> fragment", snippet)
}

func TestMinimumMatchClauses(t *testing.T) {
	for _, raw := range []string{"", "the and on", "logs metrics traces", "logs logs LOGS metrics traces"} {
		require.Empty(t, minimumMatchClauses(raw, 2, 2), raw)
	}
	clauses := minimumMatchClauses("The LOGS metrics traces dashboard logs", 2, 3)
	require.Len(t, clauses, 2)
	for i, field := range []string{"title", "body"} {
		q := clauses[i].(*bq.DisjunctionQuery)
		require.Equal(t, float64(3), q.Min)
		require.Len(t, q.Disjuncts, 4)
		require.Equal(t, []float64{2, 3}[i], q.Boost())
		var terms []string
		for _, term := range q.Disjuncts {
			match := term.(*bq.MatchQuery)
			require.Equal(t, field, match.Field())
			terms = append(terms, match.Match)
		}
		require.Equal(t, []string{"logs", "metrics", "traces", "dashboard"}, terms)
	}
	q := minimumMatchClauses("alpha bravo charlie delta echo foxtrot", 2, 2)[0].(*bq.DisjunctionQuery)
	require.Equal(t, float64(4), q.Min)
	var long []string
	for i := 0; i < 65; i++ {
		long = append(long, fmt.Sprintf("word%d", i))
	}
	require.Len(t, minimumMatchClauses(strings.Join(long[:64], " "), 2, 2), 2)
	require.Empty(t, minimumMatchClauses(strings.Join(long, " "), 2, 2))
}

func TestMinimumMatchClauseRequiresMostTerms(t *testing.T) {
	idx, err := bleve.NewMemOnly(newIndexMapping())
	require.NoError(t, err)
	defer func() { _ = idx.Close() }()
	require.NoError(t, idx.Index("most", map[string]any{"body": "alpha bravo charlie"}))
	require.NoError(t, idx.Index("partial", map[string]any{"body": "alpha bravo"}))
	clause := minimumMatchClauses("alpha bravo charlie delta", 2, 2)[1]
	res, err := idx.Search(bleve.NewSearchRequest(clause))
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	require.Equal(t, "most", res.Hits[0].ID)
}
