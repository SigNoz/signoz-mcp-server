package tools

import (
	"encoding/json"
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
	raw, exists := args["filter"]
	if !exists || raw == nil {
		return "", nil
	}
	filter, ok := raw.(string)
	if !ok {
		return "", errFilterExpressionObject()
	}
	trimmed := strings.TrimSpace(filter)
	if trimmed == "" {
		return "", nil
	}
	// The Query Builder wire object sent as text would reach SigNoz as a
	// full-text search on logs and silently match nothing.
	if strings.HasPrefix(trimmed, "{") {
		var wire map[string]any
		if json.Unmarshal([]byte(trimmed), &wire) == nil {
			if _, wrapped := wire["expression"]; wrapped {
				return "", errFilterExpressionObject()
			}
		}
	}
	return filter, nil
}

const filterExpressionObjectHint = `"filter" takes the expression text as a string, such as "service.name = 'api'", not {"expression": ...}`

func errFilterExpressionObject() error {
	return fmt.Errorf("%s %s", validationErrorPrefix, filterExpressionObjectHint)
}

// logSourceAudit selects SigNoz audit events instead of application logs.
// TODO: use apitypes.TelemetrytypesSourceAudit once the SigNoz spec
// lists audit
const logSourceAudit = "audit"

// readLogSource returns the logs store to query. SigNoz reads application
// logs for any source it does not know, so an unsupported value is dropped and
// reported as ignored for the result to say so.
func readLogSource(args map[string]any) (source string, ignored bool) {
	raw, exists := args["source"]
	if !exists || raw == nil || raw == "" {
		return "", false
	}
	if raw == logSourceAudit {
		return logSourceAudit, false
	}
	return "", true
}

// auditEventCondition matches only events SigNoz's auditor wrote. A SigNoz that
// predates audit routing ignores source and reads application logs, which this
// condition excludes, so they never come back as audit events.
const auditEventCondition = "event_name LIKE 'signoz.audit.%'"

// filterForLogSource adds auditEventCondition to an audit query's filter.
func filterForLogSource(filter, source string) string {
	if source != logSourceAudit {
		return filter
	}
	if strings.TrimSpace(filter) == "" {
		return auditEventCondition
	}
	return "(" + filter + ") AND " + auditEventCondition
}

// sourceIgnoredNote explains a dropped source, so the rows are not mistaken
// for audit events.
const sourceIgnoredNote = `note: source was ignored because only "` + logSourceAudit + `" is supported, so this result covers application logs; set source="` + logSourceAudit + `" for SigNoz audit events.`

// parseAggregateLogsArgs validates and parses arguments for the aggregate_logs tool.
func parseAggregateLogsArgs(args map[string]any) (*AggregateRequest, error) {
	service, _ := args["service"].(string)
	severity, _ := args["severity"].(string)
	filter, err := readLogFilterExpr(args)
	if err != nil {
		return nil, err
	}
	filterExpr := buildLogFilterExpr(filter, service, severity, "", "")

	reqData, err := parseAggregateArgs(args, "logs", filterExpr)
	if err != nil {
		return nil, err
	}
	reqData.Source, reqData.SourceIgnored = readLogSource(args)
	reqData.FilterExpression = filterForLogSource(reqData.FilterExpression, reqData.Source)
	return reqData, nil
}

// SearchLogsRequest holds the parsed parameters for a log search query.
type SearchLogsRequest struct {
	FilterExpression string
	// SearchScopeIgnored is set when searchScope arrived without searchText;
	// the scope only says where text matches, so the call runs without it.
	SearchScopeIgnored bool
	// BodyOnlySearchText is set when searchText matched only the log body, so an
	// empty first page can point at the wider scopes.
	BodyOnlySearchText bool
	// SelectFields is nil when the caller named none, so SigNoz returns each
	// row's complete record.
	SelectFields []querybuilder.FieldKey
	OrderKey     string
	OrderDir     string
	Limit        int
	LimitClamped bool
	Offset       int
	StartTime    int64
	EndTime      int64
	// Source is "audit" for SigNoz audit events and "" for application logs;
	// SourceIgnored is set when an unsupported value was dropped.
	Source        string
	SourceIgnored bool
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

	limit, err := intArg(args, "limit", defaultSearchLimit)
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
	source, sourceIgnored := readLogSource(args)

	return &SearchLogsRequest{
		FilterExpression:   filterForLogSource(filterExpr, source),
		SearchScopeIgnored: searchScope != "" && searchText == "",
		BodyOnlySearchText: searchText != "" && (searchScope == "" || searchScope == "body"),
		SelectFields:       selectFields,
		OrderKey:           orderKey,
		OrderDir:           orderDir,
		Limit:              limit,
		LimitClamped:       clamped,
		Offset:             offset,
		StartTime:          startTime,
		EndTime:            endTime,
		Source:             source,
		SourceIgnored:      sourceIgnored,
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

// parseLogSelectFields returns the fields the caller named, the same contract
// as signoz_search_traces. It accepts an array of names or a comma-separated
// string; a resource., attribute., log., or body. prefix sets the field
// context, and an unprefixed name is left for SigNoz to resolve.
func parseLogSelectFields(raw any) ([]querybuilder.FieldKey, error) {
	names, err := parseFieldNameList("selectFields", raw)
	if err != nil {
		return nil, err
	}
	var fields []querybuilder.FieldKey
	seen := make(map[string]string, len(names))
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
	}
	if len(fields) > maxSelectFields {
		return nil, fmt.Errorf(`"selectFields" names %d fields; request at most %d per search`, len(fields), maxSelectFields)
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
		parts = append(parts, serviceShortcutPredicate(service))
	}
	if severity != "" {
		parts = append(parts, severityPredicate(severity))
	}
	if searchText != "" {
		parts = append(parts, logSearchTextPredicate(searchText, searchScope))
	}
	return strings.Join(parts, " AND ")
}

// serviceShortcutPredicate filters the log and trace service shortcuts on the
// resource attribute. A role scoped to services grants a query only through a
// resource.-prefixed atom, and SigNoz denies a bare service.name with a 403.
func serviceShortcutPredicate(service string) string {
	return "resource.service.name = " + querybuilder.QuoteFilterLiteral(service)
}

// severityPredicate matches the severity shortcut in any case and as a prefix:
// sources write one level as ERROR, error, or Error, and as WARN or WARNING.
// LIKE metacharacters in the value stay literal.
func severityPredicate(severity string) string {
	return "severity_text ILIKE " + querybuilder.QuoteFilterLiteral(logContainsPatternEscaper.Replace(severity)+"%")
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

// bodyOnlySearchTextNote explains an empty first page from a body-scoped
// searchText: text kept in an attribute value never matches body CONTAINS.
const bodyOnlySearchTextNote = "note: searchText searched only the log body. Set searchScope to attribute to search attribute values, or all to search every searchable field; those scopes scan more, so narrow the time range if SigNoz rejects the query."

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
