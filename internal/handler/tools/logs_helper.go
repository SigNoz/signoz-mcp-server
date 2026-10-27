package tools

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

const logsLegacyQueryAliasError = `parameter validation failed: "query" is not accepted by log tools; use "filter" with a Query Builder filter expression (see signoz://logs/query-builder-guide)`

// readLogFilterExpr reads the canonical filter parameter for log tools.
func readLogFilterExpr(args map[string]any) (string, error) {
	return readFilterRejectingQueryAlias(args, logsLegacyQueryAliasError)
}

// readFilterRejectingQueryAlias reads the canonical filter parameter for a
// tool that retired the legacy query alias: any supplied query is a coded
// validation error naming filter, never a silent fallback (CMP-2).
func readFilterRejectingQueryAlias(args map[string]any, aliasError string) (string, error) {
	if _, exists := args["query"]; exists {
		return "", errors.New(aliasError)
	}
	filter := stringValue(args["filter"])
	if strings.TrimSpace(filter) == "" {
		return "", nil
	}
	return filter, nil
}

// parseAggregateLogsArgs validates and parses arguments for the aggregate_logs tool.
func parseAggregateLogsArgs(args map[string]any) (*AggregateRequest, error) {
	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	filter, err := readLogFilterExpr(args)
	if err != nil {
		return nil, err
	}
	filterExpr := buildLogFilterExpr(filter, service, severity, "", "")

	return parseAggregateArgs(args, "logs", filterExpr)
}

// SearchLogsRequest holds the parsed parameters for a log search query.
type SearchLogsRequest struct {
	FilterExpression string
	// SearchScopeIgnored is set when searchScope arrived without searchText;
	// the scope only says where text matches, so the call runs without it.
	SearchScopeIgnored bool
	SelectFields       []querybuilder.FieldKey
	OrderKey           string
	OrderDir           string
	Limit              int
	LimitClamped       bool
	Offset             int
	StartTime          int64
	EndTime            int64
}

func parseSearchLogsArgs(args map[string]any) (*SearchLogsRequest, error) {
	filter, err := readLogFilterExpr(args)
	if err != nil {
		return nil, err
	}
	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	searchText, _ := args["searchText"].(string)
	searchScope, _ := args["searchScope"].(string)
	if err := validateLogSearchScope(searchScope); err != nil {
		return nil, err
	}
	filterExpr := buildLogFilterExpr(filter, service, severity, searchText, searchScope)

	limit, err := intArg(args, "limit", querybuilder.DefaultRawQueryLimit)
	if err != nil {
		return nil, err
	}
	limit, clamped := clampLimit(limit)

	offset, err := intArg(args, "offset", 0)
	if err != nil {
		return nil, err
	}

	startTime, endTime, err := resolveTimestamps(args, "1h")
	if err != nil {
		return nil, err
	}

	selectFields, err := parseLogSelectFields(args["selectFields"])
	if err != nil {
		return nil, err
	}
	var orderKey, orderDir string
	if raw := strings.TrimSpace(stringValue(args["orderBy"])); raw != "" {
		orderKey, orderDir = parseOrderBy(raw)
	}

	return &SearchLogsRequest{
		FilterExpression:   filterExpr,
		SearchScopeIgnored: searchScope != "" && searchText == "",
		SelectFields:       selectFields,
		OrderKey:           orderKey,
		OrderDir:           orderDir,
		Limit:              limit,
		LimitClamped:       clamped,
		Offset:             offset,
		StartTime:          startTime,
		EndTime:            endTime,
	}, nil
}

// logSelectFieldContexts maps accepted name prefixes to the Query Builder
// field context for log rows.
var logSelectFieldContexts = []struct{ prefix, context string }{
	{"resource.", "resource"},
	{"attribute.", "attribute"},
	{"log.", "log"},
	{"body.", "body"},
}

// parseLogSelectFields returns the core log columns plus any extra fields the
// caller named, the same contract as signoz_search_traces. It accepts an array
// of names or a comma-separated string; a resource., attribute., log., or
// body. prefix sets the field context, and an unprefixed name is left for
// SigNoz to resolve.
func parseLogSelectFields(raw any) ([]querybuilder.FieldKey, error) {
	names, err := parseFieldNameList("selectFields", raw)
	if err != nil {
		return nil, err
	}
	fields := append([]querybuilder.FieldKey(nil), querybuilder.LogSearchCoreFields...)
	seen := make(map[string]string, len(fields)+len(names))
	for _, field := range fields {
		seen[field.Name] = querybuilder.FieldContextOf(field)
	}
	extra := 0
	for _, name := range names {
		field := querybuilder.NewFieldKey(name, "logs", "", "")
		for _, c := range logSelectFieldContexts {
			if bare, found := strings.CutPrefix(name, c.prefix); found && bare != "" {
				field = querybuilder.NewFieldKey(bare, "logs", c.context, "")
				break
			}
		}
		context := querybuilder.FieldContextOf(field)
		if held, ok := seen[field.Name]; ok {
			if err := selectFieldCollision(name, field.Name, held, context); err != nil {
				return nil, err
			}
			continue
		}
		seen[field.Name] = context
		fields = append(fields, field)
		extra++
	}
	if extra > maxSelectFields {
		return nil, fmt.Errorf(`"selectFields" names %d extra fields; request at most %d per search`, extra, maxSelectFields)
	}
	return fields, nil
}

var logContainsPatternEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// quoteLogContainsLiteral first makes LIKE metacharacters literal for the
// backend's ILIKE pattern, then escapes the result for Query Builder grammar.
func quoteLogContainsLiteral(value string) string {
	return querybuilder.QuoteFilterLiteral(logContainsPatternEscaper.Replace(value))
}

// buildLogFilterExpr combines the caller's filter expression with the log
// convenience filters. The raw filter bytes are preserved; only server-composed
// literals are quoted.
func buildLogFilterExpr(filter, service, severity, searchText, searchScope string) string {
	var parts []string
	if filter != "" {
		if service == "" && severity == "" && searchText == "" {
			return filter
		}
		parts = append(parts, "("+filter+")")
	}
	if service != "" {
		parts = append(parts, "service.name = "+querybuilder.QuoteFilterLiteral(service))
	}
	if severity != "" {
		parts = append(parts, "severity_text = "+querybuilder.QuoteFilterLiteral(severity))
	}
	if searchText != "" {
		parts = append(parts, logSearchTextPredicate(searchText, searchScope))
	}
	return strings.Join(parts, " AND ")
}

// logSearchScopes maps searchScope values to search() scopes. body keeps the
// cheaper CONTAINS predicate; all is an unscoped search() over every field.
var logSearchScopes = []string{"body", "attribute", "resource", "all"}

// validateLogSearchScope rejects an unknown scope. A known scope without
// searchText is not an error: it only says where text matches, so the search
// runs without it and the result carries searchScopeIgnoredNote.
func validateLogSearchScope(scope string) error {
	if scope == "" || slices.Contains(logSearchScopes, scope) {
		return nil
	}
	return fmt.Errorf(`parameter validation failed: "searchScope" %q is not supported; use one of: %s`, scope, strings.Join(logSearchScopes, ", "))
}

// searchScopeIgnoredNote explains a searchScope that had no searchText to scope.
const searchScopeIgnoredNote = "note: searchScope was ignored because searchText is empty; pass searchText to search for text in that scope."

func logSearchTextPredicate(searchText, scope string) string {
	switch scope {
	case "", "body":
		return "body CONTAINS " + quoteLogContainsLiteral(searchText)
	case "all":
		return "search(" + querybuilder.QuoteFilterLiteral(searchText) + ")"
	default:
		return "search(" + querybuilder.QuoteFilterLiteral(searchText) + ", " + scope + ")"
	}
}
