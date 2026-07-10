package types

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

const (
	DefaultRawQueryLimit          = 100
	DefaultAggregateQueryLimit    = 100
	MaxQueryLimit                 = 10000
	DefaultFormulaInputQueryLimit = MaxQueryLimit
)

// QueryPayload is struct used as payload the Query Builder v5 JSON schema
type QueryPayload struct {
	SchemaVersion  string               `json:"schemaVersion"`
	Start          int64                `json:"start"`
	End            int64                `json:"end"`
	RequestType    string               `json:"requestType"`
	CompositeQuery CompositeQuery       `json:"compositeQuery"`
	FormatOptions  FormatOptions        `json:"formatOptions"`
	Variables      map[string]any       `json:"variables"`
	NoCache        bool                 `json:"noCache,omitempty"`
	AppliedBounds  []AppliedQueryBounds `json:"-"`
}

// AppliedQueryBounds records defaults injected during validation so raw Query
// Builder callers can see the effective bounded request in the tool result.
type AppliedQueryBounds struct {
	QueryIndex     int
	QueryName      string
	Limit          int
	Order          []Order
	LimitDefaulted bool
	OrderDefaulted bool
	FormulaInput   bool
}

type CompositeQuery struct {
	Queries []Query `json:"queries"`
}

// Query is a single entry in CompositeQuery.Queries. Spec's concrete type
// depends on Type:
//   - "builder_query"          -> QuerySpec
//   - "builder_formula"        -> FormulaSpec
//   - "promql"                 -> PromQLSpec
//   - "clickhouse_sql"         -> ClickHouseSQLSpec
//   - anything else (e.g. "builder_trace_operator", "builder_sub_query",
//     "builder_join") -> json.RawMessage, preserved byte-for-byte so the
//     backend sees the caller's original spec.
//
// Note: builder_formula is a sibling envelope type, not a kind of builder_query.
// Formulas reference other queries' results by name (e.g. "A / B * 100") and
// carry name/expression/legend/disabled plus their own limit/order bounds.
type Query struct {
	Type string `json:"type"`
	Spec any    `json:"spec"`
}

// PromQLSpec is the spec for a query envelope with Type="promql".
// Mirrors signoz/pkg/types/querybuildertypes/querybuildertypesv5.PromQuery —
// Step is left as any so callers can pass either a Go-duration string
// ("60s", "1m") or a number of seconds, matching the backend's OneOf schema.
type PromQLSpec struct {
	Name     string `json:"name"`
	Query    string `json:"query"`
	Disabled bool   `json:"disabled,omitempty"`
	Step     any    `json:"step,omitempty"`
	Stats    bool   `json:"stats,omitempty"`
	Legend   string `json:"legend,omitempty"`
}

// ClickHouseSQLSpec is the spec for a query envelope with Type="clickhouse_sql".
type ClickHouseSQLSpec struct {
	Name     string `json:"name"`
	Query    string `json:"query"`
	Disabled bool   `json:"disabled,omitempty"`
	Legend   string `json:"legend,omitempty"`
}

// UnmarshalJSON decodes Spec into the right concrete type based on Type, so
// PromQL / ClickHouse SQL query strings survive the typed round-trip in
// signoz_execute_builder_query instead of being silently dropped.
func (q *Query) UnmarshalJSON(data []byte) error {
	var shadow struct {
		Type string          `json:"type"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(data, &shadow); err != nil {
		return err
	}
	q.Type = shadow.Type

	if len(shadow.Spec) == 0 || string(shadow.Spec) == "null" {
		q.Spec = nil
		return nil
	}

	switch shadow.Type {
	case "promql":
		var spec PromQLSpec
		if err := json.Unmarshal(shadow.Spec, &spec); err != nil {
			return fmt.Errorf("invalid promql spec: %w", err)
		}
		q.Spec = spec
	case "clickhouse_sql":
		var spec ClickHouseSQLSpec
		if err := json.Unmarshal(shadow.Spec, &spec); err != nil {
			return fmt.Errorf("invalid clickhouse_sql spec: %w", err)
		}
		q.Spec = spec
	case "builder_formula":
		var spec FormulaSpec
		if err := json.Unmarshal(shadow.Spec, &spec); err != nil {
			return fmt.Errorf("invalid builder_formula spec: %w", err)
		}
		q.Spec = spec
	case "builder_query":
		var spec QuerySpec
		if err := json.Unmarshal(shadow.Spec, &spec); err != nil {
			return fmt.Errorf("invalid builder_query spec: %w", err)
		}
		q.Spec = spec
	default:
		// Preserve unknown / less-common envelope types (builder_trace_operator,
		// builder_sub_query, builder_join, future additions) as raw JSON so
		// their fields survive the round-trip byte-for-byte. Decoding into
		// QuerySpec would drop type-specific fields like `expression`,
		// `returnSpansFrom`, and inject zero-valued builder fields the
		// backend's DisallowUnknownFields decoder would reject.
		spec := make(json.RawMessage, len(shadow.Spec))
		copy(spec, shadow.Spec)
		q.Spec = spec
	}
	return nil
}

type QuerySpec struct {
	Name                  string            `json:"name"`
	Signal                string            `json:"signal"`
	Source                string            `json:"source,omitempty"`
	StepInterval          *int64            `json:"stepInterval,omitempty"`
	Disabled              bool              `json:"disabled"`
	Filter                *Filter           `json:"filter,omitempty"`
	Limit                 int               `json:"limit"`
	LimitBy               json.RawMessage   `json:"limitBy,omitempty"`
	Offset                int               `json:"offset"`
	Cursor                string            `json:"cursor,omitempty"`
	Order                 []Order           `json:"order"`
	Having                Having            `json:"having"`
	SelectFields          []SelectField     `json:"selectFields"`
	Aggregations          []any             `json:"aggregations,omitempty"`
	SecondaryAggregations []json.RawMessage `json:"secondaryAggregations,omitempty"`
	GroupBy               []SelectField     `json:"groupBy,omitempty"`
	Functions             []json.RawMessage `json:"functions,omitempty"`
	Legend                string            `json:"legend,omitempty"`
}

// UnmarshalJSON accepts integer-like strings for nested bounds because MCP
// clients are inconsistent about encoding numeric values. The wire contract is
// still canonicalized back to JSON numbers before the request is sent upstream.
func (s *QuerySpec) UnmarshalJSON(data []byte) error {
	normalized, err := normalizeSpecIntegerFields(data, "limit", "offset")
	if err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &fields); err != nil {
		return err
	}
	if raw, ok := fields["orderBy"]; ok && strings.TrimSpace(string(raw)) != "null" {
		return fmt.Errorf(`spec.orderBy is a dashboard/editor field, not a Query Builder v5 wire field; use spec.order, for example [{"key":{"name":"timestamp"},"direction":"desc"}]`)
	}

	type querySpecAlias QuerySpec
	var decoded querySpecAlias
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return err
	}
	*s = QuerySpec(decoded)
	return nil
}

func normalizeSpecIntegerFields(data []byte, fieldNames ...string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, fieldName := range fieldNames {
		raw, ok := fields[fieldName]
		if !ok {
			continue
		}
		value, err := parseSpecInteger(raw)
		if err != nil {
			return nil, fmt.Errorf(`spec.%s must be an integer or numeric string, for example %s: %w`, fieldName, integerFieldExample(fieldName), err)
		}
		fields[fieldName] = json.RawMessage(strconv.Itoa(value))
	}
	return json.Marshal(fields)
}

func parseSpecInteger(raw json.RawMessage) (int, error) {
	value := strings.TrimSpace(string(raw))
	if value == "null" {
		return 0, nil
	}
	if strings.HasPrefix(value, `"`) {
		var stringValue string
		if err := json.Unmarshal(raw, &stringValue); err != nil {
			return 0, fmt.Errorf("received %s", value)
		}
		value = strings.TrimSpace(stringValue)
	}
	if len(value) > 32 {
		return 0, fmt.Errorf("received a %d-character value; integer tokens must be at most 32 characters", len(value))
	}
	parsed, ok := new(big.Rat).SetString(value)
	if !ok || strings.Contains(value, "/") || !parsed.IsInt() {
		return 0, fmt.Errorf("received %s; fractional, empty, and non-numeric values are not accepted", strings.TrimSpace(string(raw)))
	}
	if !parsed.Num().IsInt64() {
		return 0, fmt.Errorf("received %s; value is outside the supported integer range", strings.TrimSpace(string(raw)))
	}
	integer64 := parsed.Num().Int64()
	integer := int(integer64)
	if int64(integer) != integer64 {
		return 0, fmt.Errorf("received %s; value is outside the supported integer range", strings.TrimSpace(string(raw)))
	}
	return integer, nil
}

func integerFieldExample(fieldName string) string {
	if fieldName == "offset" {
		return `0 or "0"`
	}
	return `100 or "100"; omit it or use 0 to apply the request-type default`
}

type Order struct {
	Key       Key    `json:"key"`
	Direction string `json:"direction"`
}

type Key struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Unit          string `json:"unit,omitempty"`
	Signal        string `json:"signal,omitempty"`
	FieldContext  string `json:"fieldContext,omitempty"`
	FieldDataType string `json:"fieldDataType,omitempty"`
}

type Having struct {
	Expression string `json:"expression"`
}

type Filter struct {
	Expression string `json:"expression"`
}

type SelectField struct {
	Name          string `json:"name"`
	FieldDataType string `json:"fieldDataType"`
	Signal        string `json:"signal"`
	FieldContext  string `json:"fieldContext,omitempty"`
}

type FormatOptions struct {
	FormatTableResultForUI bool `json:"formatTableResultForUI"`
	FillGaps               bool `json:"fillGaps"`
}

// QueryAggregation represents an aggregation expression for QB v5 queries (logs, traces).
// Example expressions: "count()", "avg(duration)", "p99(duration_nano)", "count_distinct(user_id)"
type QueryAggregation struct {
	Expression string `json:"expression"`
}

// Validate performs necessary validation for required fields
// this indirectly helps LLMs to build right payload.
// if there is an error LLM checks the error and fix.
func (q *QueryPayload) Validate() error {
	q.AppliedBounds = nil
	if q.SchemaVersion == "" {
		q.SchemaVersion = "v1"
	}

	if q.Start == 0 || q.End == 0 {
		return fmt.Errorf("missing start or end timestamp")
	}
	if len(q.CompositeQuery.Queries) == 0 {
		return fmt.Errorf("missing or empty compositeQuery.queries")
	}
	if q.RequestType == "" {
		q.RequestType = inferDefaultRequestType(q.CompositeQuery.Queries)
	}

	for i, query := range q.CompositeQuery.Queries {
		switch query.Type {
		case "promql":
			spec, ok := query.Spec.(PromQLSpec)
			if !ok {
				return fmt.Errorf("query at position %d: promql envelope has wrong spec type %T", i+1, query.Spec)
			}
			if spec.Query == "" {
				name := spec.Name
				if name == "" {
					name = fmt.Sprintf("query at position %d", i+1)
				}
				return fmt.Errorf("%s: missing query string for promql query", name)
			}
			continue
		case "clickhouse_sql":
			spec, ok := query.Spec.(ClickHouseSQLSpec)
			if !ok {
				return fmt.Errorf("query at position %d: clickhouse_sql envelope has wrong spec type %T", i+1, query.Spec)
			}
			if spec.Query == "" {
				name := spec.Name
				if name == "" {
					name = fmt.Sprintf("query at position %d", i+1)
				}
				return fmt.Errorf("%s: missing query string for clickhouse_sql query", name)
			}
			continue
		case "builder_formula":
			spec, ok := query.Spec.(FormulaSpec)
			if !ok {
				return fmt.Errorf("query at position %d: builder_formula envelope has wrong spec type %T", i+1, query.Spec)
			}
			queryName := queryDisplayName(spec.Name, i)
			if strings.TrimSpace(spec.Expression) == "" {
				return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.expression is required for builder_formula; provide an expression such as "A / B * 100"`, queryName, i)
			}
			if q.RequestType != "scalar" && q.RequestType != "time_series" {
				return fmt.Errorf(`%s: builder_formula requires requestType "scalar" or "time_series"; received %q`, queryName, q.RequestType)
			}
			continue
		case "builder_query":
			// fall through to builder validation below
		default:
			// builder_trace_operator, builder_sub_query, builder_join, etc.
			// Pass through unchanged; backend will validate.
			continue
		}

		spec, ok := query.Spec.(QuerySpec)
		if !ok {
			return fmt.Errorf("query at position %d: builder_query envelope has wrong spec type %T", i+1, query.Spec)
		}
		signal := spec.Signal
		queryName := queryDisplayName(spec.Name, i)

		switch signal {
		case "metrics":
			// Default an unset requestType, but reject an explicit unknown value
			// instead of silently coercing it (a coerced value can return a
			// different result shape than the caller asked for).
			switch q.RequestType {
			case "time_series", "scalar":
				// ok
			default:
				return fmt.Errorf("%s: unsupported requestType %q for metrics; use \"time_series\" or \"scalar\"", queryName, q.RequestType)
			}

		case "traces":
			// Traces support both raw queries and time series aggregations.
			// Don't force requestType=raw, since that breaks aggregation queries.
			switch q.RequestType {
			case "raw", "trace":
				spec.StepInterval = nil
			case "scalar":
				spec.StepInterval = nil
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for scalar traces query", queryName)
				}
			case "time_series":
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for time_series traces query", queryName)
				}
			default:
				return fmt.Errorf("%s: unsupported requestType '%s' for traces", queryName, q.RequestType)
			}

		case "logs":
			// Logs support both raw queries and time series aggregations.
			// Don't force requestType=raw, since that breaks count()/groupBy queries.
			switch q.RequestType {
			case "raw":
				spec.StepInterval = nil
			case "scalar":
				spec.StepInterval = nil
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for scalar logs query", queryName)
				}
			case "time_series":
				if len(spec.Aggregations) == 0 {
					return fmt.Errorf("%s: missing aggregations for time_series logs query", queryName)
				}
			default:
				return fmt.Errorf("%s: unsupported requestType '%s' for logs", queryName, q.RequestType)
			}

		default:
			return fmt.Errorf("%s: unknown signal type '%s'", queryName, signal)
		}

		q.CompositeQuery.Queries[i].Spec = spec
	}

	return q.ApplyBuilderBounds()
}

func inferDefaultRequestType(queries []Query) string {
	for _, query := range queries {
		switch query.Type {
		case "promql", "builder_formula":
			return "time_series"
		case "builder_query":
			if spec, ok := query.Spec.(QuerySpec); ok && spec.Signal == "metrics" {
				return "time_series"
			}
		}
	}
	return "raw"
}

// ApplyBuilderBounds normalizes and validates limit/order fields without
// requiring the outer time range. Callers with nested Query Builder envelopes,
// such as alert rules, can reuse the same contract without running the full
// QueryPayload validation path.
func (q *QueryPayload) ApplyBuilderBounds() error {
	q.AppliedBounds = nil
	if q.RequestType == "" {
		q.RequestType = inferDefaultRequestType(q.CompositeQuery.Queries)
	}
	formulaInputs := formulaInputQueryNames(q.CompositeQuery.Queries)
	for i, query := range q.CompositeQuery.Queries {
		switch query.Type {
		case "builder_query":
			spec, ok := query.Spec.(QuerySpec)
			if !ok {
				continue
			}
			queryName := queryDisplayName(spec.Name, i)
			guide := guideForSignal(spec.Signal)
			if spec.Limit < 0 {
				return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.limit received %d; use a positive integer, or omit/use 0 to apply the %s default. See %s`, queryName, i, spec.Limit, requestTypeLimitDescription(q.RequestType), guide)
			}
			if spec.Limit > MaxQueryLimit {
				return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.limit received %d; the Query Builder maximum is %d. Reduce the limit to %d or less, or narrow the filters/grouping. See %s`, queryName, i, spec.Limit, MaxQueryLimit, MaxQueryLimit, guide)
			}
			if spec.Offset < 0 {
				return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.offset received %d; use a non-negative integer such as 0. See %s`, queryName, i, spec.Offset, guide)
			}

			applied := AppliedQueryBounds{QueryIndex: i, QueryName: queryName}
			if spec.Limit == 0 {
				if formulaInputs[strings.ToLower(strings.TrimSpace(spec.Name))] {
					spec.Limit = DefaultFormulaInputQueryLimit
					applied.FormulaInput = true
				} else {
					spec.Limit = defaultLimitForRequestType(q.RequestType)
				}
				applied.LimitDefaulted = true
			}
			if len(spec.Order) == 0 {
				order, err := defaultOrderForQuery(spec, q.RequestType, i)
				if err != nil {
					return err
				}
				spec.Order = order
				applied.OrderDefaulted = true
			} else if err := validateAuthoredOrder(spec.Order, queryName, i, guide); err != nil {
				return err
			}
			applied.Limit = spec.Limit
			applied.Order = append([]Order(nil), spec.Order...)
			if applied.LimitDefaulted || applied.OrderDefaulted {
				q.AppliedBounds = append(q.AppliedBounds, applied)
			}
			q.CompositeQuery.Queries[i].Spec = spec

		case "builder_formula":
			spec, ok := query.Spec.(FormulaSpec)
			if !ok {
				continue
			}
			queryName := queryDisplayName(spec.Name, i)
			if spec.Limit < 0 {
				return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.limit received %d; builder_formula limits must be positive, or omitted/0 for the 100-series default. See signoz://metrics-aggregation-guide`, queryName, i, spec.Limit)
			}
			applied := AppliedQueryBounds{QueryIndex: i, QueryName: queryName}
			if spec.Limit == 0 {
				spec.Limit = DefaultAggregateQueryLimit
				applied.LimitDefaulted = true
			}
			if len(spec.Order) == 0 {
				spec.Order = resultDescendingOrder()
				applied.OrderDefaulted = true
			} else if err := validateAuthoredOrder(spec.Order, queryName, i, "signoz://metrics-aggregation-guide"); err != nil {
				return err
			}
			applied.Limit = spec.Limit
			applied.Order = append([]Order(nil), spec.Order...)
			if applied.LimitDefaulted || applied.OrderDefaulted {
				q.AppliedBounds = append(q.AppliedBounds, applied)
			}
			q.CompositeQuery.Queries[i].Spec = spec
		}
	}
	return nil
}

// formulaInputQueryNames returns the builder-query names referenced by any
// formula in the same composite request. SigNoz applies each builder query's
// limit before it evaluates formulas, including disabled intermediate formulas,
// and filters disabled results only afterward. Omitted formula inputs therefore
// need a wider bound than standalone aggregate results.
func formulaInputQueryNames(queries []Query) map[string]bool {
	builderNames := make([]string, 0, len(queries))
	formulaExpressions := make([]string, 0, len(queries))
	for _, query := range queries {
		switch spec := query.Spec.(type) {
		case QuerySpec:
			if name := strings.TrimSpace(spec.Name); name != "" {
				builderNames = append(builderNames, name)
			}
		case FormulaSpec:
			formulaExpressions = append(formulaExpressions, spec.Expression)
		}
	}

	inputs := make(map[string]bool)
	for _, name := range builderNames {
		for _, expression := range formulaExpressions {
			if formulaReferencesQuery(expression, name) {
				inputs[strings.ToLower(name)] = true
				break
			}
		}
	}
	return inputs
}

func formulaReferencesQuery(expression, queryName string) bool {
	expression = strings.ToLower(expression)
	queryName = strings.ToLower(strings.TrimSpace(queryName))
	if queryName == "" {
		return false
	}
	for start := 0; start < len(expression); {
		index := strings.Index(expression[start:], queryName)
		if index < 0 {
			return false
		}
		index += start
		end := index + len(queryName)
		leftBoundary := index == 0 || !isFormulaIdentifierByte(expression[index-1])
		rightBoundary := end == len(expression) || !isFormulaIdentifierByte(expression[end])
		if leftBoundary && rightBoundary {
			return true
		}
		start = index + 1
	}
	return false
}

func isFormulaIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func queryDisplayName(name string, index int) string {
	if strings.TrimSpace(name) != "" {
		return fmt.Sprintf("query %q", name)
	}
	return fmt.Sprintf("query at position %d", index+1)
}

func defaultLimitForRequestType(requestType string) int {
	if requestType == "raw" || requestType == "trace" {
		return DefaultRawQueryLimit
	}
	return DefaultAggregateQueryLimit
}

func requestTypeLimitDescription(requestType string) string {
	if requestType == "raw" || requestType == "trace" {
		return fmt.Sprintf("%d-row %s", DefaultRawQueryLimit, requestType)
	}
	return fmt.Sprintf("%d-group %s", DefaultAggregateQueryLimit, requestType)
}

func defaultOrderForQuery(spec QuerySpec, requestType string, index int) ([]Order, error) {
	switch requestType {
	case "raw", "trace":
		switch spec.Signal {
		case "logs":
			return []Order{
				{Key: Key{Name: "timestamp"}, Direction: "desc"},
				{Key: Key{Name: "id"}, Direction: "desc"},
			}, nil
		case "traces":
			return []Order{{Key: Key{Name: "timestamp"}, Direction: "desc"}}, nil
		}
	case "scalar", "time_series":
		if spec.Signal == "metrics" {
			return resultDescendingOrder(), nil
		}
		if spec.Signal == "logs" || spec.Signal == "traces" {
			expression := primaryAggregationExpression(spec.Aggregations)
			if expression == "" {
				return nil, fmt.Errorf(`%s: compositeQuery.queries[%d].spec.order is missing and aggregations[0].expression could not be determined; add an explicit spec.order or provide an aggregation expression such as "count()". See %s`, queryDisplayName(spec.Name, index), index, guideForSignal(spec.Signal))
			}
			return []Order{{Key: Key{Name: expression}, Direction: "desc"}}, nil
		}
	}
	return nil, fmt.Errorf(`%s: no safe default order exists for signal %q and requestType %q; provide spec.order explicitly`, queryDisplayName(spec.Name, index), spec.Signal, requestType)
}

func primaryAggregationExpression(aggregations []any) string {
	if len(aggregations) == 0 {
		return ""
	}
	var expression string
	switch aggregation := aggregations[0].(type) {
	case QueryAggregation:
		expression = aggregation.Expression
	case *QueryAggregation:
		if aggregation != nil {
			expression = aggregation.Expression
		}
	case map[string]any:
		if value, ok := aggregation["expression"].(string); ok {
			expression = value
		}
	}
	expression = strings.TrimSpace(expression)
	if aliasIndex := topLevelAliasIndex(expression); aliasIndex > 0 {
		expression = strings.TrimSpace(expression[:aliasIndex])
	}
	return expression
}

// topLevelAliasIndex returns the byte offset of the last SQL-style " AS "
// outside quoted strings and parentheses. Query Builder aggregation expressions
// may contain quoted text with the same bytes, which must not be mistaken for a
// display alias.
func topLevelAliasIndex(expression string) int {
	aliasIndex := -1
	depth := 0
	var quote byte
	escaped := false
	for i := 0; i < len(expression); i++ {
		ch := expression[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && i+4 <= len(expression) && strings.EqualFold(expression[i:i+4], " as ") {
				aliasIndex = i
				i += 3
			}
		}
	}
	return aliasIndex
}

func resultDescendingOrder() []Order {
	return []Order{{Key: Key{Name: "__result"}, Direction: "desc"}}
}

func validateAuthoredOrder(order []Order, queryName string, queryIndex int, guide string) error {
	for orderIndex := range order {
		keyName := strings.TrimSpace(order[orderIndex].Key.Name)
		if keyName == "" {
			return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.order[%d].key.name is empty; provide an order key such as "timestamp", "count()", or "__result". See %s`, queryName, queryIndex, orderIndex, guide)
		}
		direction := strings.ToLower(strings.TrimSpace(order[orderIndex].Direction))
		if direction != "asc" && direction != "desc" {
			return fmt.Errorf(`%s: compositeQuery.queries[%d].spec.order[%d].direction received %q; valid values are "asc" or "desc". See %s`, queryName, queryIndex, orderIndex, order[orderIndex].Direction, guide)
		}
		order[orderIndex].Key.Name = keyName
		order[orderIndex].Direction = direction
	}
	return nil
}

func guideForSignal(signal string) string {
	switch signal {
	case "logs":
		return "signoz://logs/query-builder-guide"
	case "traces":
		return "signoz://traces/query-builder-guide"
	default:
		return "signoz://metrics-aggregation-guide"
	}
}

// BuildLogsQueryPayload creates a QueryPayload for logs queries
func BuildLogsQueryPayload(startTime, endTime int64, filterExpression string, limit int, offset int) *QueryPayload {
	return &QueryPayload{
		SchemaVersion: "v1",
		Start:         startTime,
		End:           endTime,
		RequestType:   "raw",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:     "A",
						Signal:   "logs",
						Disabled: false,
						Filter:   &Filter{Expression: filterExpression},
						Limit:    limit,
						Offset:   offset,
						Order: []Order{
							{Key: Key{Name: "timestamp"}, Direction: "desc"},
							{Key: Key{Name: "id"}, Direction: "desc"},
						},
						Having: Having{Expression: ""},
					},
				},
			},
		},
		FormatOptions: FormatOptions{
			FormatTableResultForUI: false,
			FillGaps:               false,
		},
		Variables: map[string]any{},
	}
}

// BuildAggregateQueryPayload creates a QueryPayload for aggregation queries, signal is "logs" or "traces".
// aggregationExpr is a QB v5 expression like "count()", "avg(duration)", "p99(duration_nano)".
// groupBy is a list of fields to group by.
// orderByExpr is the expression to order by (e.g. "count()"), orderDir is "asc" or "desc".
func BuildAggregateQueryPayload(signal string, startTime, endTime int64, aggregationExpr string, filterExpression string, groupBy []SelectField, orderByExpr string, orderDir string, limit int, requestType string, stepInterval *int64) *QueryPayload {
	if requestType == "" {
		requestType = "scalar"
	}
	return &QueryPayload{
		SchemaVersion: "v1",
		Start:         startTime,
		End:           endTime,
		RequestType:   requestType,
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:         "A",
						Signal:       signal,
						StepInterval: stepInterval,
						Disabled:     false,
						Filter:       &Filter{Expression: filterExpression},
						Limit:        limit,
						Offset:       0,
						Order: []Order{
							{Key: Key{Name: orderByExpr}, Direction: orderDir},
						},
						Having:       Having{Expression: ""},
						GroupBy:      groupBy,
						Aggregations: []any{QueryAggregation{Expression: aggregationExpr}},
					},
				},
			},
		},
		FormatOptions: FormatOptions{
			FormatTableResultForUI: false,
			FillGaps:               false,
		},
		Variables: map[string]any{},
	}
}

// MetricAggregation represents a metric-specific aggregation in the v5 payload.
type MetricAggregation struct {
	MetricName       string `json:"metricName"`
	Temporality      string `json:"temporality,omitempty"`
	TimeAggregation  string `json:"timeAggregation,omitempty"`
	SpaceAggregation string `json:"spaceAggregation"`
	ReduceTo         string `json:"reduceTo,omitempty"`
}

// MetricsQuerySpec describes a single metric query or formula within a composite query.
type MetricsQuerySpec struct {
	Name        string
	Aggregation MetricAggregation
	Filter      string
	GroupBy     []SelectField
	IsFormula   bool   // if true, Expression is used instead of Aggregation
	Expression  string // formula: "A / B * 100"
	Legend      string
}

// FormulaSpec is the spec shape for builder_formula queries. Formula bounds
// share the builder wire contract, while the expression replaces the
// signal/aggregation/filter fields used by QuerySpec.
type FormulaSpec struct {
	Name       string            `json:"name"`
	Expression string            `json:"expression"`
	Legend     string            `json:"legend,omitempty"`
	Disabled   bool              `json:"disabled"`
	Limit      int               `json:"limit"`
	Order      []Order           `json:"order"`
	Having     *Having           `json:"having,omitempty"`
	Functions  []json.RawMessage `json:"functions,omitempty"`
}

func (s *FormulaSpec) UnmarshalJSON(data []byte) error {
	normalized, err := normalizeSpecIntegerFields(data, "limit")
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &fields); err != nil {
		return err
	}
	if raw, ok := fields["orderBy"]; ok && strings.TrimSpace(string(raw)) != "null" {
		return fmt.Errorf(`spec.orderBy is a dashboard/editor field, not a Query Builder v5 formula field; use spec.order, for example [{"key":{"name":"__result"},"direction":"desc"}]`)
	}
	type formulaSpecAlias FormulaSpec
	var decoded formulaSpecAlias
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return err
	}
	*s = FormulaSpec(decoded)
	return nil
}

// BuildMetricsQueryPayloadJSON builds the metrics payload and returns the
// marshalled JSON. It handles formula specs that need a different shape.
// source is an optional data-source filter (e.g. "meter" for Cost Meter queries);
// pass an empty string for the default SigNoz metrics store.
func BuildMetricsQueryPayloadJSON(startTime, endTime, stepInterval int64, queries []MetricsQuerySpec, requestType, source string) ([]byte, error) {
	if requestType == "" {
		requestType = "time_series"
	}

	var qbQueries []Query
	for _, q := range queries {
		if q.IsFormula {
			qbQueries = append(qbQueries, Query{
				Type: "builder_formula",
				Spec: FormulaSpec{
					Name:       q.Name,
					Expression: q.Expression,
					Legend:     q.Legend,
					Disabled:   false,
				},
			})
			continue
		}

		spec := QuerySpec{
			Name:         q.Name,
			Signal:       "metrics",
			Source:       source,
			Disabled:     false,
			Aggregations: []any{q.Aggregation},
			GroupBy:      q.GroupBy,
			Having:       Having{Expression: ""},
		}
		if stepInterval > 0 {
			step := stepInterval
			spec.StepInterval = &step
		}
		if q.Filter != "" {
			spec.Filter = &Filter{Expression: q.Filter}
		}

		qbQueries = append(qbQueries, Query{
			Type: "builder_query",
			Spec: spec,
		})
	}

	payload := QueryPayload{
		SchemaVersion:  "v1",
		Start:          startTime,
		End:            endTime,
		RequestType:    requestType,
		CompositeQuery: CompositeQuery{Queries: qbQueries},
		FormatOptions: FormatOptions{
			FormatTableResultForUI: false,
			FillGaps:               false,
		},
		Variables: map[string]any{},
	}
	if err := payload.Validate(); err != nil {
		return nil, fmt.Errorf("invalid generated metrics query payload: %w", err)
	}

	return json.Marshal(payload)
}

func BuildTracesQueryPayload(startTime, endTime int64, filterExpression string, limit int, offset int) *QueryPayload {
	return &QueryPayload{
		SchemaVersion: "v1",
		Start:         startTime,
		End:           endTime,
		RequestType:   "raw",
		CompositeQuery: CompositeQuery{
			Queries: []Query{
				{
					Type: "builder_query",
					Spec: QuerySpec{
						Name:     "A",
						Signal:   "traces",
						Disabled: false,
						Filter:   &Filter{Expression: filterExpression},
						Limit:    limit,
						Offset:   offset,
						Order: []Order{
							{Key: Key{Name: "timestamp"}, Direction: "desc"},
						},
						Having: Having{Expression: ""},
						SelectFields: []SelectField{
							// Top-level span fields
							{Name: "trace_id", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "span_id", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "parent_span_id", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "links", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "name", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "duration_nano", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
							{Name: "timestamp", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
							{Name: "has_error", FieldDataType: "bool", Signal: "traces", FieldContext: "span"},
							{Name: "status_code", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
							{Name: "status_code_string", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "http_method", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "http_url", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "kind_string", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "kind", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
							{Name: "response_status_code", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							{Name: "status_message", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
							// Resource attributes
							{Name: "service.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "cloud.account.id", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "cloud.platform", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "cloud.provider", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "cloud.region", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "deployment.environment", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "host.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.cluster.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.namespace.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.node.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.pod.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.pod.start_time", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.pod.uid", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "k8s.statefulset.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "service.version", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "signoz.deployment.tier", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "signoz.workload", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
							{Name: "signoz.workspace.key.id", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},

							// Span attributes
							{Name: "client.address", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "http.request.method", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "http.response.body.size", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "http.response.status_code", FieldDataType: "number", Signal: "traces", FieldContext: "tag"},
							{Name: "http.route", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "rpc.method", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "network.peer.address", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "network.peer.port", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "network.protocol.version", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "server.address", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "url.path", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "url.scheme", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "db.operation", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "db.statement", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
							{Name: "db.system", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
						},
					},
				},
			},
		},
		FormatOptions: FormatOptions{
			FormatTableResultForUI: false,
			FillGaps:               false,
		},
		Variables: map[string]any{},
	}
}
