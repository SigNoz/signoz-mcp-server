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
	require.LessOrEqual(t, len(queries), 50)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot, err := LoadEmbeddedCorpus()
	require.NoError(t, err)
	reg, err := NewIndexRegistry(ctx, snapshot)
	require.NoError(t, err)
	defer reg.Close(context.Background())

	var recallHits, precisionHits int
	for _, q := range queries {
		res, err := reg.Search(ctx, q.Query, "", 3)
		require.NoError(t, err, q.Query)
		require.NotEmpty(t, res.Results, q.Query)
		expected := map[string]struct{}{}
		for _, url := range q.ExpectedTopPages {
			canonical, ok := CanonicalDocURL(url)
			require.True(t, ok, "bad expected URL for %q: %s", q.Query, url)
			expected[canonical] = struct{}{}
		}
		if _, ok := expected[res.Results[0].URL]; ok {
			precisionHits++
		}
		for _, hit := range res.Results {
			if _, ok := expected[hit.URL]; ok {
				recallHits++
				break
			}
		}
	}
	recallAt3 := float64(recallHits) / float64(len(queries))
	precisionAt1 := float64(precisionHits) / float64(len(queries))
	t.Logf("recall@3=%.3f precision@1=%.3f queries=%d", recallAt3, precisionAt1, len(queries))
	require.GreaterOrEqual(t, recallAt3, 0.9)
	require.GreaterOrEqual(t, precisionAt1, 0.7)
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
