package querybuilder

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/internal/apiclient/apitypes"
)

// AppliedBounds records a limit or order this server inserted into one query,
// so a tool result can report the effective bounded request.
type AppliedBounds struct {
	QueryIndex     int
	QueryName      string
	Limit          int
	Order          []apitypes.Querybuildertypesv5OrderBy
	LimitDefaulted bool
	OrderDefaulted bool
}

// OrderText renders the inserted order as "key direction" pairs.
func (b AppliedBounds) OrderText() string {
	parts := make([]string, 0, len(b.Order))
	for _, item := range b.Order {
		var key, direction string
		if item.Key != nil {
			key = item.Key.Name
		}
		if item.Direction != nil {
			direction = string(*item.Direction)
		}
		parts = append(parts, key+" "+direction)
	}
	return strings.Join(parts, ", ")
}

// queryView is what bound selection reads from one query envelope.
type queryView struct {
	builder     bool
	ai          bool // builder_ai_query: SigNoz picks the order
	formula     bool
	name        string
	signal      string
	limit       *int
	hasOrder    bool
	aggregation string // primary aggregation expression; logs and traces only
	expression  string // formula expression
}

// ApplyBounds inserts signal-aware default limits and orders into a Query
// Builder v5 request decoded from JSON. It edits only spec.limit and spec.order
// in place, and only where the caller left them unset; builder queries a
// formula references keep no limit or order unless the caller set them, so the
// formula sees every input series. Every other field is
// forwarded verbatim for SigNoz, the only validator, to judge. A query whose
// shape cannot be read is left untouched. Numeric strings in spec.limit and
// spec.offset become JSON integers, because MCP clients are inconsistent about
// encoding nested numbers. A builder_ai_query gets the same default limit as a
// builder_query of its request type and no default order.
func ApplyBounds(request map[string]any) []AppliedBounds {
	composite, _ := request["compositeQuery"].(map[string]any)
	queries, _ := composite["queries"].([]any)
	if len(queries) == 0 {
		return nil
	}
	requestType, _ := request["requestType"].(string)

	specs := make([]map[string]any, len(queries))
	views := make([]queryView, len(queries))
	for i, raw := range queries {
		envelope, _ := raw.(map[string]any)
		spec, _ := envelope["spec"].(map[string]any)
		if spec == nil {
			continue
		}
		switch envelope["type"] {
		case string(apitypes.Querybuildertypesv5QueryTypeBuilderQuery), QueryTypeBuilderAI:
			normalizeIntegerStrings(spec, "limit", "offset")
		case string(apitypes.Querybuildertypesv5QueryTypeBuilderFormula):
			normalizeIntegerStrings(spec, "limit")
		default:
			continue
		}
		specs[i] = spec
		views[i] = readQuery(envelope)
	}

	inputs := formulaInputNames(views)
	var applied []AppliedBounds
	for i, view := range views {
		if !view.builder && !view.formula {
			continue
		}
		if view.builder && inputs[strings.ToLower(strings.TrimSpace(view.name))] {
			continue
		}
		bounds := AppliedBounds{QueryIndex: i, QueryName: displayName(view.name, i)}
		if view.limit == nil || *view.limit == 0 {
			if view.formula {
				bounds.Limit = DefaultAggregateQueryLimit
			} else {
				bounds.Limit = defaultLimit(requestType)
			}
			specs[i]["limit"] = bounds.Limit
			bounds.LimitDefaulted = true
		}
		if !view.hasOrder {
			if order := defaultOrder(view, requestType); order != nil {
				specs[i]["order"] = jsonValue(order)
				bounds.Order = order
				bounds.OrderDefaulted = true
			}
		}
		if bounds.LimitDefaulted || bounds.OrderDefaulted {
			applied = append(applied, bounds)
		}
	}
	return applied
}

// readQuery reads one envelope through the generated union types. An envelope
// that does not decode yields a zero view, so its query is left untouched.
func readQuery(envelope map[string]any) queryView {
	body, err := json.Marshal(envelope)
	if err != nil {
		return queryView{}
	}
	var union apitypes.Querybuildertypesv5QueryEnvelope
	if err := json.Unmarshal(body, &union); err != nil {
		return queryView{}
	}
	value, err := union.ValueByDiscriminator()
	if err != nil {
		return queryView{}
	}
	switch typed := value.(type) {
	case apitypes.Querybuildertypesv5QueryEnvelopeBuilder:
		if typed.Spec == nil {
			return queryView{}
		}
		spec, err := typed.Spec.ValueByDiscriminator()
		if err != nil {
			return queryView{}
		}
		switch q := spec.(type) {
		case logQuery:
			view := builderView(q.Name, "logs", q.Limit, q.Order)
			if q.Aggregations != nil && len(*q.Aggregations) > 0 {
				view.aggregation = deref((*q.Aggregations)[0].Expression)
			}
			return view
		case traceQuery:
			view := builderView(q.Name, "traces", q.Limit, q.Order)
			if q.Aggregations != nil && len(*q.Aggregations) > 0 {
				view.aggregation = deref((*q.Aggregations)[0].Expression)
			}
			return view
		case metricQuery:
			return builderView(q.Name, "metrics", q.Limit, q.Order)
		}
	case apitypes.Querybuildertypesv5QueryEnvelopeBuilderAI:
		if typed.Spec == nil {
			return queryView{}
		}
		view := builderView(typed.Spec.Name, "traces", typed.Spec.Limit, typed.Spec.Order)
		view.ai = true
		return view
	case apitypes.Querybuildertypesv5QueryEnvelopeFormula:
		if typed.Spec == nil {
			return queryView{}
		}
		return queryView{
			formula:    true,
			name:       deref(typed.Spec.Name),
			limit:      typed.Spec.Limit,
			hasOrder:   typed.Spec.Order != nil && len(*typed.Spec.Order) > 0,
			expression: deref(typed.Spec.Expression),
		}
	}
	return queryView{}
}

func builderView(name *string, signal string, limit *int, order *[]orderBy) queryView {
	return queryView{
		builder:  true,
		name:     deref(name),
		signal:   signal,
		limit:    limit,
		hasOrder: order != nil && len(*order) > 0,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func displayName(name string, index int) string {
	if strings.TrimSpace(name) != "" {
		return fmt.Sprintf("query %q", name)
	}
	return fmt.Sprintf("query at position %d", index+1)
}

func defaultLimit(requestType string) int {
	if requestType == "raw" || requestType == "trace" {
		return DefaultRawQueryLimit
	}
	return DefaultAggregateQueryLimit
}

// defaultOrder returns the signal-safe order for a query that omitted one, or
// nil when none applies; SigNoz then judges the query as sent. A
// builder_ai_query gets none: SigNoz orders AI trace rows by last activity, and
// a span-level default such as timestamp is not a trace-list order key.
func defaultOrder(view queryView, requestType string) []orderBy {
	if view.ai {
		return nil
	}
	if view.formula {
		return resultDescending()
	}
	switch requestType {
	case "raw", "trace":
		switch view.signal {
		case "logs":
			return []orderBy{newOrderBy("timestamp", "desc"), newOrderBy("id", "desc")}
		case "traces":
			return []orderBy{newOrderBy("timestamp", "desc")}
		}
	case "scalar", "time_series":
		if view.signal == "metrics" {
			return resultDescending()
		}
		if expression := primaryAggregationExpression(view.aggregation); expression != "" {
			return []orderBy{newOrderBy(expression, "desc")}
		}
	}
	return nil
}

func resultDescending() []orderBy {
	return []orderBy{newOrderBy("__result", "desc")}
}

func jsonValue(v any) any {
	body, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return v
	}
	return decoded
}

// formulaInputNames returns the builder-query names any formula in the same
// request references. SigNoz applies each builder query's limit before it
// evaluates formulas, including disabled intermediate formulas, so a limit on
// an input silently drops series from the formula result.
func formulaInputNames(views []queryView) map[string]bool {
	inputs := make(map[string]bool)
	for _, builder := range views {
		name := strings.TrimSpace(builder.name)
		if !builder.builder || name == "" {
			continue
		}
		for _, formula := range views {
			if formula.formula && formulaReferencesQuery(formula.expression, name) {
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

// primaryAggregationExpression strips a trailing SQL-style display alias so the
// default order key is the aggregation expression itself.
func primaryAggregationExpression(expression string) string {
	expression = strings.TrimSpace(expression)
	if aliasIndex := topLevelAliasIndex(expression); aliasIndex > 0 {
		expression = strings.TrimSpace(expression[:aliasIndex])
	}
	return expression
}

// topLevelAliasIndex returns the byte offset of the last " AS " outside quoted
// strings and parentheses. Aggregation expressions may quote text containing
// the same bytes, which must not be mistaken for a display alias.
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

// normalizeIntegerStrings rewrites numeric strings such as "100" in the named
// spec fields as JSON integers. Any other value is left for SigNoz to judge.
func normalizeIntegerStrings(spec map[string]any, fields ...string) {
	for _, field := range fields {
		text, ok := spec[field].(string)
		if !ok {
			continue
		}
		if value, ok := parseInteger(text); ok {
			spec[field] = value
		}
	}
}

func parseInteger(text string) (int, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 32 || strings.Contains(text, "/") {
		return 0, false
	}
	parsed, ok := new(big.Rat).SetString(text)
	if !ok || !parsed.IsInt() || !parsed.Num().IsInt64() {
		return 0, false
	}
	value := parsed.Num().Int64()
	if int64(int(value)) != value {
		return 0, false
	}
	return int(value), true
}
