package docs

import (
	"context"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	bq "github.com/blevesearch/bleve/v2/search/query"
	"github.com/stretchr/testify/require"
)

func TestGlossaryGroupsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, group := range docsSynonymGroups {
		require.GreaterOrEqual(t, len(group.members), 2)
		for _, member := range group.members {
			require.False(t, seen[member], "member %q appears in two groups", member)
			seen[member] = true
			require.NotEmpty(t, analyzedTerms(member), member)
		}
		for _, member := range append(append([]string{}, group.queryOnly...), group.expansionOnly...) {
			require.Contains(t, group.members, member)
		}
	}
}

func TestGlossaryExpansion(t *testing.T) {
	for _, group := range docsSynonymGroups {
		for _, member := range group.members {
			t.Run(member, func(t *testing.T) {
				expansions := glossaryExpansions(strings.ToUpper(member))
				if containsString(group.expansionOnly, member) {
					require.Empty(t, expansions)
					return
				}
				require.NotContains(t, expansions, member)
				for _, other := range group.members {
					if other == member || containsString(group.queryOnly, other) {
						require.NotContains(t, expansions, other)
					} else {
						require.Contains(t, expansions, other)
					}
				}
				require.Empty(t, glossaryExpansions(strings.Join(group.members, " ")))
				require.Empty(t, glossaryExpansions("prefix"+member+"suffix"))
				require.Len(t, glossaryClauses(member+" "+member), 2*len(expansions))
			})
		}
	}
	// Bidirectional: the long form pulls in the short form.
	require.Equal(t, []string{"js"}, glossaryExpansions("javascript"))
	require.Equal(t, []string{"postgres"}, glossaryExpansions("PostgreSQL metrics"))
	// Query-only short forms trigger expansion but are never added.
	require.Equal(t, []string{"kubernetes"}, glossaryExpansions("k8s"))
	require.Equal(t, []string{"infrastructure"}, glossaryExpansions("infra monitoring"))
	require.Empty(t, glossaryExpansions("kubernetes node memory"))
	require.Equal(t, []string{"amazon web services"}, glossaryExpansions("monitor aws infrastructure"))
	// Expansion-only: ".net" analyzes to "net", which must not trigger dotnet.
	require.Equal(t, []string{".net"}, glossaryExpansions("dotnet tracing"))
	require.Empty(t, glossaryExpansions("net/http instrumentation"))
	// Multiword members match only as a contiguous sequence.
	require.Equal(t, []string{"mute", "silence"}, glossaryExpansions("planned maintenance window"))
	require.Empty(t, glossaryExpansions("planned alert maintenance"))
	require.Equal(t, []string{"prebuilt"}, glossaryExpansions("pre-built dashboards"))
	// Ambiguous single words stay out.
	for _, raw := range []string{"node memory", "go instrumentation", "logs collector", "", "dashboard templates"} {
		require.Empty(t, glossaryClauses(raw), raw)
	}
	require.Len(t, glossaryClauses("k8s otel gcp aws js dotnet mongo"), maxGlossaryClauses)
	for _, clause := range glossaryClauses("gcp") {
		phrase, ok := clause.(*bq.MatchPhraseQuery)
		require.True(t, ok)
		require.Equal(t, "google cloud", phrase.MatchPhrase)
		require.Equal(t, defaultGlossaryBoost, clause.(bq.BoostableQuery).Boost())
	}
	for _, clause := range glossaryClauses("mute alert") {
		require.Equal(t, 2.0, clause.(bq.BoostableQuery).Boost())
	}
	for _, clause := range glossaryClauses("dotnet") {
		match, ok := clause.(*bq.MatchQuery)
		require.True(t, ok)
		require.Equal(t, ".net", match.Match)
		require.Equal(t, defaultGlossaryBoost, clause.(bq.BoostableQuery).Boost())
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
	defer func() { _ = idx.Close() }()
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
