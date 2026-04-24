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
