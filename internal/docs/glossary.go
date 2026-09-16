package docs

import (
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/registry"
	bq "github.com/blevesearch/bleve/v2/search/query"
)

const maxGlossaryClauses = 8

var docsAliases = []struct{ term, expansion string }{
	{"k8s", "kubernetes"},
	{"otel", "opentelemetry"},
	{"gcp", "google cloud"},
	{"aws", "amazon web services"},
	{"js", "javascript"},
}

var docsQueryAnalyzer = func() analysis.Analyzer {
	analyzer, err := registry.NewCache().AnalyzerNamed(standard.Name)
	if err != nil {
		panic(err)
	}
	return analyzer
}()

func glossaryExpansions(raw string) []string {
	tokens := docsQueryAnalyzer.Analyze([]byte(raw))
	terms := make([]string, 0, len(tokens))
	present := map[string]bool{}
	for _, token := range tokens {
		term := string(token.Term)
		present[term] = true
		terms = append(terms, term)
	}
	text := " " + strings.Join(terms, " ") + " "
	var expansions []string
	for _, alias := range docsAliases {
		if !present[alias.term] || strings.Contains(text, " "+alias.expansion+" ") {
			continue
		}
		expansions = append(expansions, alias.expansion)
	}
	return expansions
}

func glossaryClauses(raw string) []bq.Query {
	var clauses []bq.Query
	for _, expansion := range glossaryExpansions(raw) {
		for _, field := range []string{"title", "body"} {
			if len(clauses) == maxGlossaryClauses {
				return clauses
			}
			if strings.Contains(expansion, " ") {
				q := bleve.NewMatchPhraseQuery(expansion)
				q.SetField(field)
				q.SetBoost(0.5)
				clauses = append(clauses, q)
			} else {
				q := bleve.NewMatchQuery(expansion)
				q.SetField(field)
				q.SetBoost(0.5)
				clauses = append(clauses, q)
			}
		}
	}
	return clauses
}
