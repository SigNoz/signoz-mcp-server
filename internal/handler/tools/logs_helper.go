package tools

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

const logsLegacyQueryAliasError = `parameter validation failed: "query" is not accepted by log tools; use "filter" with a Query Builder filter expression (see signoz://logs/query-builder-guide)`

// readLogFilterExpr reads the canonical filter parameter for log tools. The
// legacy query alias was removed from the changed log tools, so the presence
// of query is a correctable validation error rather than a silent fallback.
func readLogFilterExpr(args map[string]any) (string, error) {
	if _, exists := args["query"]; exists {
		return "", errors.New(logsLegacyQueryAliasError)
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
	Limit            int
	LimitClamped     bool
	Offset           int
	StartTime        int64
	EndTime          int64
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
	if err := validateLogSearchScope(searchScope, searchText); err != nil {
		return nil, err
	}
	filterExpr := buildLogFilterExpr(filter, service, severity, searchText, searchScope)

	limit, err := intArg(args, "limit", types.DefaultRawQueryLimit)
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

	return &SearchLogsRequest{
		FilterExpression: filterExpr,
		Limit:            limit,
		LimitClamped:     clamped,
		Offset:           offset,
		StartTime:        startTime,
		EndTime:          endTime,
	}, nil
}

// quoteLogFilterLiteral escapes a literal for a single-quoted Query Builder
// string: each backslash becomes two backslashes first, then each apostrophe
// is backslash-escaped, matching the backend's FilterStringLiteral rules.
func quoteLogFilterLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}

var logContainsPatternEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// quoteLogContainsLiteral first makes LIKE metacharacters literal for the
// backend's ILIKE pattern, then escapes the result for Query Builder grammar.
func quoteLogContainsLiteral(value string) string {
	return quoteLogFilterLiteral(logContainsPatternEscaper.Replace(value))
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
		parts = append(parts, "service.name = "+quoteLogFilterLiteral(service))
	}
	if severity != "" {
		parts = append(parts, "severity_text = "+quoteLogFilterLiteral(severity))
	}
	if searchText != "" {
		parts = append(parts, logSearchTextPredicate(searchText, searchScope))
	}
	return strings.Join(parts, " AND ")
}

// logSearchScopes maps searchScope values to search() scopes. body keeps the
// cheaper CONTAINS predicate; all is an unscoped search() over every field.
var logSearchScopes = []string{"body", "attribute", "resource", "all"}

func validateLogSearchScope(scope, searchText string) error {
	if scope == "" {
		return nil
	}
	if !slices.Contains(logSearchScopes, scope) {
		return fmt.Errorf(`parameter validation failed: "searchScope" %q is not supported; use one of: %s`, scope, strings.Join(logSearchScopes, ", "))
	}
	if searchText == "" {
		return errors.New(`parameter validation failed: "searchScope" needs "searchText"; supply the text to find, or remove searchScope`)
	}
	return nil
}

func logSearchTextPredicate(searchText, scope string) string {
	switch scope {
	case "", "body":
		return "body CONTAINS " + quoteLogContainsLiteral(searchText)
	case "all":
		return "search(" + quoteLogFilterLiteral(searchText) + ")"
	default:
		return "search(" + quoteLogFilterLiteral(searchText) + ", " + scope + ")"
	}
}
