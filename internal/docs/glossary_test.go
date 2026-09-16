package docs

import (
	"context"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	bq "github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/require"
)

func TestGlossaryExpansion(t *testing.T) {
	for _, alias := range docsAliases {
		t.Run(alias.term, func(t *testing.T) {
			require.Equal(t, []string{alias.expansion}, glossaryExpansions(strings.ToUpper(alias.term)))
			require.Empty(t, glossaryExpansions(alias.term+" "+alias.expansion))
			require.Empty(t, glossaryExpansions("prefix"+alias.term+"suffix"))
			require.Len(t, glossaryClauses(alias.term+" "+alias.term), 2)
		})
	}
	for _, raw := range []string{"kubernetes node memory", "logs collector", "", "javascript"} {
		require.Empty(t, glossaryClauses(raw), raw)
	}
	require.Len(t, glossaryClauses("k8s otel gcp aws js"), maxGlossaryClauses)
	require.Equal(t, []string{"google cloud"}, glossaryExpansions("gcp"))
	for _, clause := range glossaryClauses("gcp") {
		phrase, ok := clause.(*bq.MatchPhraseQuery)
		require.True(t, ok)
		require.Equal(t, "google cloud", phrase.MatchPhrase)
	}
}

func TestGlossaryPreservesQuerySyntax(t *testing.T) {
	for _, raw := range []string{`"otel collector"`, `title:otel`, `body:"k8s logs"`} {
		q, err := boostedDocsQuery(context.Background(), raw)
		require.NoError(t, err)
		clauses := q.(*bq.DisjunctionQuery).Disjuncts
		require.Equal(t, raw, clauses[0].(*bq.MatchQuery).Match)
		require.Equal(t, raw, clauses[5].(*bq.QueryStringQuery).Query)
		require.Equal(t, 8+len(glossaryClauses(raw))+len(minimumMatchClauses(raw, 2, 2)), len(clauses))
	}
	q, err := boostedDocsQuery(context.Background(), `"otel`)
	require.NoError(t, err)
	for _, clause := range q.(*bq.DisjunctionQuery).Disjuncts {
		_, isQueryString := clause.(*bq.QueryStringQuery)
		require.False(t, isQueryString)
	}
}

func TestGlossaryOnlySnippet(t *testing.T) {
	idx, err := bleve.NewMemOnly(newIndexMapping())
	require.NoError(t, err)
	defer idx.Close()
	body := strings.Repeat("unrelated introduction ", 40) + " Kubernetes sends pod telemetry to the collector."
	require.NoError(t, idx.Index("doc", map[string]any{"body": body}))
	q, err := boostedDocsQuery(context.Background(), "k8s")
	require.NoError(t, err)
	req := bleve.NewSearchRequest(q)
	req.Highlight = bleve.NewHighlight()
	req.Highlight.AddField("body")
	res, err := idx.Search(req)
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	snippet := chooseSnippet(res.Hits[0].Fragments["body"], body, "k8s", snippetRuneLimit)
	require.Contains(t, strings.ToLower(snippet), "kubernetes")
	require.LessOrEqual(t, len([]rune(snippet)), snippetRuneLimit)
}
