package docs

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

type goldenQuery struct {
	Query            string   `json:"query"`
	ExpectedTopPages []string `json:"expected_top_pages"`
	Style            string   `json:"style"`
	Holdout          bool     `json:"holdout,omitempty"`
	BaselineRank     *int     `json:"baseline_rank,omitempty"`
}

func TestGoldenSet(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	raw, err := os.ReadFile("testdata/golden_queries.json")
	require.NoError(t, err)
	var queries []goldenQuery
	require.NoError(t, json.Unmarshal(raw, &queries))
	require.GreaterOrEqual(t, len(queries), 30)
	require.LessOrEqual(t, len(queries), 160)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, err := LoadEmbeddedCorpus()
	require.NoError(t, err)
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())

	type counts struct{ total, recall, precision int }
	byStyle := map[string]*counts{"keyword": {}, "sentence": {}, "abbreviation": {}, "production-raw": {}, "production-keyword": {}, "production-real": {}}
	baselineByStyle := map[string]*counts{"keyword": {}, "sentence": {}, "abbreviation": {}, "production-raw": {}, "production-keyword": {}, "production-real": {}}
	for _, q := range queries {
		c, ok := byStyle[q.Style]
		require.True(t, ok, "unknown style %q", q.Style)
		res, err := reg.Search(ctx, q.Query, "", 3)
		require.NoError(t, err, q.Query)
		require.NotEmpty(t, res.Results, q.Query)
		require.NotEmpty(t, q.ExpectedTopPages, q.Query)
		expected := map[string]bool{}
		for _, url := range q.ExpectedTopPages {
			canonical, ok := CanonicalDocURL(url)
			require.True(t, ok, "bad expected URL for %q: %s", q.Query, url)
			expected[canonical] = true
		}
		rank := 0
		for i, hit := range res.Results {
			if expected[hit.URL] {
				rank = i + 1
				break
			}
		}
		require.NotNil(t, q.BaselineRank, "missing pre-change baseline: %s", q.Query)
		baseline := baselineByStyle[q.Style]
		baseline.total++
		if *q.BaselineRank > 0 {
			baseline.recall++
		}
		if *q.BaselineRank == 1 {
			baseline.precision++
		}
		c.total++
		if rank > 0 {
			c.recall++
		}
		if rank == 1 {
			c.precision++
		}
		t.Logf("rank=%d style=%s holdout=%t query=%q", rank, q.Style, q.Holdout, q.Query)
		if q.BaselineRank != nil && *q.BaselineRank != rank {
			t.Logf("rank change %q: %d -> %d (0 means outside top 3)", q.Query, *q.BaselineRank, rank)
		}
	}
	var overall, baselineOverall counts
	for _, style := range []string{"keyword", "sentence", "abbreviation", "production-raw", "production-keyword", "production-real"} {
		c, baseline := byStyle[style], baselineByStyle[style]
		require.Positive(t, c.total, style)
		recall, precision := float64(c.recall)/float64(c.total), float64(c.precision)/float64(c.total)
		baselineRecall, baselinePrecision := float64(baseline.recall)/float64(baseline.total), float64(baseline.precision)/float64(baseline.total)
		t.Logf("style=%s recall@3=%.6f precision@1=%.6f queries=%d baseline=%.6f/%.6f", style, recall, precision, c.total, baselineRecall, baselinePrecision)
		require.GreaterOrEqual(t, recall, baselineRecall-0.05, style+" recall@3 baseline tolerance")
		require.GreaterOrEqual(t, precision, baselinePrecision-0.05, style+" precision@1 baseline tolerance")
		if style == "keyword" {
			require.GreaterOrEqual(t, recall, 0.9)
			require.GreaterOrEqual(t, precision, 0.7)
		}
		overall.total += c.total
		overall.recall += c.recall
		overall.precision += c.precision
		baselineOverall.total += baseline.total
		baselineOverall.recall += baseline.recall
		baselineOverall.precision += baseline.precision
	}
	recall, precision := float64(overall.recall)/float64(overall.total), float64(overall.precision)/float64(overall.total)
	baselineRecall, baselinePrecision := float64(baselineOverall.recall)/float64(baselineOverall.total), float64(baselineOverall.precision)/float64(baselineOverall.total)
	t.Logf("overall recall@3=%.6f precision@1=%.6f queries=%d baseline=%.6f/%.6f", recall, precision, overall.total, baselineRecall, baselinePrecision)
	require.GreaterOrEqual(t, recall, baselineRecall, "overall recall@3 must not decrease")
	require.GreaterOrEqual(t, precision, baselinePrecision, "overall precision@1 must not decrease")
	require.True(t, recall > baselineRecall || precision > baselinePrecision, "at least one overall metric must improve")
}

func TestEmbeddedCorpusSectionFilters(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve_index_api.AnalysisWorker"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, err := LoadEmbeddedCorpus()
	require.NoError(t, err)
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())

	tests := []struct {
		name    string
		query   string
		section string
		wantURL string
	}{
		{
			name:    "setup install docs",
			query:   "docker",
			section: "setup",
			wantURL: "https://signoz.io/docs/install/docker/",
		},
		{
			name:    "traces docs",
			query:   "missing spans troubleshooting",
			section: "apm-distributed-tracing",
			wantURL: "https://signoz.io/docs/traces-management/troubleshooting/troubleshooting/",
		},
		{
			name:    "alerts docs",
			query:   "slack notification channel",
			section: "alerts",
			wantURL: "https://signoz.io/docs/alerts-management/notification-channel/slack/",
		},
		{
			name:    "api docs",
			query:   "search logs api",
			section: "signoz-apis",
			wantURL: "https://signoz.io/docs/logs-management/logs-api/search-logs/",
		},
		{
			name:    "duplicate page alternate section",
			query:   "deno",
			section: "logs-management",
			wantURL: "https://signoz.io/docs/instrumentation/opentelemetry-deno/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := reg.Search(ctx, tt.query, tt.section, 5)
			require.NoError(t, err)
			require.NotEmpty(t, res.Results)
			for _, hit := range res.Results {
				require.Equal(t, tt.section, hit.SectionSlug)
			}
			var urls []string
			for _, hit := range res.Results {
				urls = append(urls, hit.URL)
			}
			require.Contains(t, urls, tt.wantURL)
		})
	}
}
