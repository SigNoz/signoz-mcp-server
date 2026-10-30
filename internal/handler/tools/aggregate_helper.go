package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

// legacyAggregationParamsError names the replacement for the retired
// aggregation and aggregateOn inputs (the CMP-2 compatibility path).
func legacyAggregationParamsError() error {
	return fmt.Errorf(`%s "aggregation" and "aggregateOn" were replaced by "aggregations", which takes the full expressions; for example {"aggregations": ["count()", "p99(duration_nano)"]}`, validationErrorPrefix)
}

// AggregateRequest keeps parameters for any aggregation query.
type AggregateRequest struct {
	// Aggregations are the expressions sent, in caller order; AggregationExpr is
	// the first one, which the default order and the log lines use.
	Aggregations     []string
	AggregationExpr  string
	FilterExpression string
	GroupBy          []querybuilder.FieldKey
	OrderExpr        string
	OrderDir         string
	Limit            int
	LimitClamped     bool
	StartTime        int64
	EndTime          int64
	RequestType      string // "scalar" (default) or "time_series"
	QueryType        string // traces only; builder_ai_query selects the AI envelope
	StepInterval     *int64 // nil = let backend auto-select
	Having           string
	Functions        []querybuilder.Function
	// StepIntervalWarning is set when a stepInterval value was provided but could
	// not be parsed as a positive integer. The handler logs it (WARN) so a
	// silently-dropped value is detectable rather than vanishing.
	StepIntervalWarning string
}

// parseAggregateArgs validates and parses  aggregate arguments.
// this is crucial as the input is provided by llm and if there is an error it must be suggested how to correct
func parseAggregateArgs(args map[string]any, signal string, filterExpr string) (*AggregateRequest, error) {
	aggregations, err := parseAggregationsArg(args)
	if err != nil {
		return nil, err
	}
	aggregationExpr := aggregations[0]

	groupByNames, err := parseFieldNameList("groupBy", args["groupBy"])
	if err != nil {
		return nil, fmt.Errorf("%s %w", validationErrorPrefix, err)
	}
	groupByFields := make([]querybuilder.FieldKey, 0, len(groupByNames))
	for _, name := range groupByNames {
		groupByFields = append(groupByFields, aggregateGroupByField(signal, name))
	}

	orderExpr, orderDir := aggregationExpr, "desc"
	if orderByStr := strings.TrimSpace(stringValue(args["orderBy"])); orderByStr != "" {
		orderExpr, orderDir = parseOrderBy(orderByStr)
	}

	functions, err := parseFunctionsArg(args["functions"])
	if err != nil {
		return nil, err
	}

	limit, err := intArg(args, "limit", querybuilder.DefaultAggregateQueryLimit)
	if err != nil {
		return nil, err
	}
	// Bound the group limit (high-cardinality groupBy). Surfaced via
	// aggregateResult's note since aggregations have no offset pagination.
	limit, limitClamped := clampLimit(limit)

	startTime, endTime, err := resolveTimestamps(args, "1h")
	if err != nil {
		return nil, err
	}

	requestType, err := readRequestType(args)
	if err != nil {
		return nil, err
	}
	if err := validateRequestType(requestType); err != nil {
		return nil, err
	}
	if requestType == "" {
		requestType = "scalar"
	}

	stepInterval, stepIntervalWarning := parseStepInterval(args["stepInterval"])

	return &AggregateRequest{
		Aggregations:        aggregations,
		AggregationExpr:     aggregationExpr,
		FilterExpression:    filterExpr,
		GroupBy:             groupByFields,
		OrderExpr:           orderExpr,
		OrderDir:            orderDir,
		Limit:               limit,
		LimitClamped:        limitClamped,
		StartTime:           startTime,
		EndTime:             endTime,
		RequestType:         requestType,
		StepInterval:        stepInterval,
		StepIntervalWarning: stepIntervalWarning,
		Having:              strings.TrimSpace(stringValue(args["having"])),
		Functions:           functions,
	}, nil
}

// aggregateNotes returns the advisory notes an aggregate request earns before
// the result is read: today only that functions do nothing on scalar results.
func aggregateNotes(reqData *AggregateRequest) []string {
	var notes []string
	if len(reqData.Functions) > 0 && reqData.RequestType == "scalar" {
		notes = append(notes, functionsIgnoredOnScalarNote)
	}
	return notes
}

// parseAggregationsArg reads aggregations: one expression string, an array of
// expression strings, or a JSON-encoded array. Expressions are forwarded
// verbatim for SigNoz to validate. The retired aggregation and aggregateOn
// inputs get a coded error naming their replacement.
func parseAggregationsArg(args map[string]any) ([]string, error) {
	if _, legacy := args["aggregation"]; legacy {
		return nil, legacyAggregationParamsError()
	}
	if _, legacy := args["aggregateOn"]; legacy {
		return nil, legacyAggregationParamsError()
	}
	var items []string
	switch v := args["aggregations"].(type) {
	case nil:
	case string:
		if trimmed := strings.TrimSpace(v); strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
				return nil, fmt.Errorf(`%s "aggregations" is not a valid JSON array of expression strings: %v`, validationErrorPrefix, err)
			}
		} else {
			items = []string{v}
		}
	case []any:
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf(`%s "aggregations" items must be expression strings such as "count()" or "p99(duration_nano)", not %v`, validationErrorPrefix, item)
			}
			items = append(items, text)
		}
	case []string:
		items = v
	default:
		return nil, fmt.Errorf(`%s "aggregations" must be an expression string or an array of expression strings, not %v`, validationErrorPrefix, v)
	}

	expressions := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			expressions = append(expressions, item)
		}
	}
	if len(expressions) == 0 {
		return nil, fmt.Errorf(
			`%s "aggregations" is required: one or more aggregation expressions such as "count()", "count_distinct(user_id)", "avg(duration)", or "p99(duration_nano)". Tip: for simple totals use {"aggregations": ["count()"], "groupBy": "service.name"}`,
			validationErrorPrefix)
	}
	return expressions, nil
}

// aggregateGroupByField sends a bare name; SigNoz resolves its context and
// type against the workspace's field metadata.
func aggregateGroupByField(signal, name string) querybuilder.FieldKey {
	return querybuilder.NewFieldKey(name, signal, "", "")
}

// aggregateResult is the result wrapper for aggregation tools. Aggregations
// have no offset pagination, so the note advises narrowing the query instead.
func aggregateResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limitClamped bool, extraNotes ...string) *mcp.CallToolResult {
	notes := append([]string(nil), extraNotes...)
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d groups to bound server memory; narrow the time range, filters, or groupBy cardinality for fewer, more-specific groups.",
			MaxRawResultLimit))
	}
	warnings := extractBackendWarningMessages(payload)
	warnBackendWarnings(ctx, logger, toolName, warnings)
	warnUnparsedWarningEnvelope(ctx, logger, toolName, payload, len(warnings))
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(payload, notes...)
}
