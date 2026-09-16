package docs

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/registry"
	bq "github.com/blevesearch/bleve/v2/search/query"
)

const (
	maxGlossaryClauses   = 12
	defaultGlossaryBoost = 0.5
)

// A synonym group lists spellings that name the same thing in queries and
// docs. When a query contains one member, the others are added as clauses.
// Ambiguous single words such as "go" and "node" stay out.
type synonymGroup struct {
	members []string
	// queryOnly members trigger expansion but are never added: docs use them
	// with a narrower meaning than the long form ("k8s-infra").
	queryOnly []string
	// expansionOnly members are added but never trigger: ".net" analyzes to the
	// bare token "net", which also appears in "net/http".
	expansionOnly []string
	boost         float64
}

var docsSynonymGroups = []synonymGroup{
	{members: []string{"k8s", "kubernetes"}, queryOnly: []string{"k8s"}},
	{members: []string{"otel", "opentelemetry"}},
	{members: []string{"gcp", "google cloud"}},
	{members: []string{"aws", "amazon web services"}},
	{members: []string{"js", "javascript"}},
	{members: []string{"prebuilt", "pre-built"}},
	// Boosted because 0.5 cannot lift "Planned Maintenance/Downtime" above
	// pages whose titles contain "alert".
	{members: []string{"mute", "silence", "planned maintenance"}, boost: 2.0},
	{members: []string{"postgres", "postgresql"}},
	{members: []string{"msteams", "microsoft teams"}},
	{members: []string{"dotnet", ".net"}, expansionOnly: []string{".net"}},
	{members: []string{"nodejs", "node.js"}},
	{members: []string{"mongo", "mongodb"}},
	{members: []string{"infra", "infrastructure"}, queryOnly: []string{"infra"}},
}

var docsQueryAnalyzer = func() analysis.Analyzer {
	analyzer, err := registry.NewCache().AnalyzerNamed(standard.Name)
	if err != nil {
		panic(err)
	}
	return analyzer
}()

type glossaryMember struct {
	text          string
	terms         []string
	queryOnly     bool
	expansionOnly bool
}

type compiledGroup struct {
	members []glossaryMember
	boost   float64
}

var compiledSynonymGroups = func() []compiledGroup {
	groups := make([]compiledGroup, 0, len(docsSynonymGroups))
	for _, group := range docsSynonymGroups {
		compiled := compiledGroup{boost: group.boost}
		if compiled.boost == 0 {
			compiled.boost = defaultGlossaryBoost
		}
		for _, text := range group.members {
			compiled.members = append(compiled.members, glossaryMember{
				text:          text,
				terms:         analyzedTerms(text),
				queryOnly:     containsString(group.queryOnly, text),
				expansionOnly: containsString(group.expansionOnly, text),
			})
		}
		groups = append(groups, compiled)
	}
	return groups
}()

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func analyzedTerms(text string) []string {
	tokens := docsQueryAnalyzer.Analyze([]byte(text))
	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		terms = append(terms, string(token.Term))
	}
	return terms
}

func containsSequence(terms, seq []string) bool {
	if len(seq) == 0 || len(seq) > len(terms) {
		return false
	}
outer:
	for i := 0; i+len(seq) <= len(terms); i++ {
		for j := range seq {
			if terms[i+j] != seq[j] {
				continue outer
			}
		}
		return true
	}
	return false
}

type glossaryExpansion struct {
	text  string
	terms []string
	boost float64
}

// glossaryExpansionsWithBoost returns, for every group with a triggering
// member present in the query, the members that are absent and addable.
func glossaryExpansionsWithBoost(raw string) []glossaryExpansion {
	terms := analyzedTerms(raw)
	if len(terms) == 0 {
		return nil
	}
	var expansions []glossaryExpansion
	for _, group := range compiledSynonymGroups {
		present := make([]bool, len(group.members))
		triggered := false
		for i, member := range group.members {
			present[i] = containsSequence(terms, member.terms)
			triggered = triggered || (present[i] && !member.expansionOnly)
		}
		if !triggered {
			continue
		}
		for i, member := range group.members {
			if !present[i] && !member.queryOnly && len(member.terms) > 0 {
				expansions = append(expansions, glossaryExpansion{text: member.text, terms: member.terms, boost: group.boost})
			}
		}
	}
	return expansions
}

func glossaryExpansions(raw string) []string {
	var out []string
	for _, expansion := range glossaryExpansionsWithBoost(raw) {
		out = append(out, expansion.text)
	}
	return out
}

func glossaryClauses(raw string) []bq.Query {
	var clauses []bq.Query
	for _, expansion := range glossaryExpansionsWithBoost(raw) {
		for _, field := range []string{"title", "body"} {
			if len(clauses) == maxGlossaryClauses {
				return clauses
			}
			if len(expansion.terms) > 1 {
				q := bleve.NewMatchPhraseQuery(expansion.text)
				q.SetField(field)
				q.SetBoost(expansion.boost)
				clauses = append(clauses, q)
			} else {
				q := bleve.NewMatchQuery(expansion.text)
				q.SetField(field)
				q.SetBoost(expansion.boost)
				clauses = append(clauses, q)
			}
		}
	}
	return clauses
}
