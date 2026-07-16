package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SigNoz/signoz-mcp-server/pkg/timeutil"
	"github.com/SigNoz/signoz-mcp-server/pkg/types"
)

var validAggregations = map[string]bool{
	"count":          true,
	"count_distinct": true,
	"avg":            true,
	"sum":            true,
	"min":            true,
	"max":            true,
	"p50":            true,
	"p75":            true,
	"p90":            true,
	"p95":            true,
	"p99":            true,
	"rate":           true,
}

var aggregationsWithoutField = map[string]bool{
	"count": true,
	"rate":  true,
}

const allowedAggregations = "avg, count, count_distinct, max, min, p50, p75, p90, p95, p99, rate, sum"

const conflictingFilterAliasError = "both 'filter' and 'query' were provided with different values; use only 'filter' (the 'query' alias is legacy)"

var traceGroupByFieldMetadata = map[string]types.SelectField{
	"trace_id":             {Name: "trace_id", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"span_id":              {Name: "span_id", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"parent_span_id":       {Name: "parent_span_id", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"name":                 {Name: "name", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"duration_nano":        {Name: "duration_nano", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
	"timestamp":            {Name: "timestamp", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
	"has_error":            {Name: "has_error", FieldDataType: "bool", Signal: "traces", FieldContext: "span"},
	"status_code":          {Name: "status_code", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
	"status_code_string":   {Name: "status_code_string", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"http_method":          {Name: "http_method", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"http_url":             {Name: "http_url", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"kind_string":          {Name: "kind_string", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"kind":                 {Name: "kind", FieldDataType: "number", Signal: "traces", FieldContext: "span"},
	"response_status_code": {Name: "response_status_code", FieldDataType: "string", Signal: "traces", FieldContext: "span"},
	"status_message":       {Name: "status_message", FieldDataType: "string", Signal: "traces", FieldContext: "span"},

	"service.name":            {Name: "service.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"cloud.account.id":        {Name: "cloud.account.id", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"cloud.platform":          {Name: "cloud.platform", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"cloud.provider":          {Name: "cloud.provider", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"cloud.region":            {Name: "cloud.region", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"deployment.environment":  {Name: "deployment.environment", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"host.name":               {Name: "host.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.cluster.name":        {Name: "k8s.cluster.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.namespace.name":      {Name: "k8s.namespace.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.node.name":           {Name: "k8s.node.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.pod.name":            {Name: "k8s.pod.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.pod.start_time":      {Name: "k8s.pod.start_time", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.pod.uid":             {Name: "k8s.pod.uid", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"k8s.statefulset.name":    {Name: "k8s.statefulset.name", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"service.version":         {Name: "service.version", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"signoz.deployment.tier":  {Name: "signoz.deployment.tier", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"signoz.workload":         {Name: "signoz.workload", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},
	"signoz.workspace.key.id": {Name: "signoz.workspace.key.id", FieldDataType: "string", Signal: "traces", FieldContext: "resource"},

	"client.address":            {Name: "client.address", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"http.request.method":       {Name: "http.request.method", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"http.response.body.size":   {Name: "http.response.body.size", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"http.response.status_code": {Name: "http.response.status_code", FieldDataType: "number", Signal: "traces", FieldContext: "tag"},
	"http.route":                {Name: "http.route", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"rpc.method":                {Name: "rpc.method", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"network.peer.address":      {Name: "network.peer.address", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"network.peer.port":         {Name: "network.peer.port", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"network.protocol.version":  {Name: "network.protocol.version", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"server.address":            {Name: "server.address", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"url.path":                  {Name: "url.path", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"url.scheme":                {Name: "url.scheme", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"db.operation":              {Name: "db.operation", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"db.statement":              {Name: "db.statement", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"db.system":                 {Name: "db.system", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
	"messaging.system":          {Name: "messaging.system", FieldDataType: "string", Signal: "traces", FieldContext: "tag"},
}

// timeRangeDesc builds the shared "timeRange" description. All time-windowed
// tools use one parser (timeutil.ParseTimeRange), so only the per-tool default
// window differs — defaultDesc is that trailing sentence (e.g. "Defaults to
// '1h'."). The full Go-duration grammar is accepted; we advertise the m/h/d
// subset because it covers every realistic observability window.
func timeRangeDesc(defaultDesc string) string {
	return "Relative time range. Format: <number><unit> where unit is 'm' (minutes), 'h' (hours), or 'd' (days). " +
		"Examples: '30m', '1h', '2h', '6h', '24h', '3d', '7d'. " +
		"Ignored when both start and end are provided. " + defaultDesc
}

// stepIntervalDesc is the shared "stepInterval" description (seconds value with
// backend auto-selection).
const stepIntervalDesc = "Time bucket size in seconds for time_series mode (optional). " +
	"When omitted, the backend auto-selects an appropriate interval. " +
	"Only set this if the user explicitly requests a specific granularity. " +
	"Examples: '60' (1 min), '3600' (1 hour), '86400' (1 day)."

// readFilterExpr returns the QB filter expression, accepting the canonical
// "filter" key and the legacy "query" alias. TrimSpace is used only to decide
// presence/equality; the returned expression preserves the caller's original
// text. Never log filter expressions here because they can contain user data.
func readFilterExpr(args map[string]any) (string, error) {
	filterRaw := stringValue(args["filter"])
	queryRaw := stringValue(args["query"])
	filterTrimmed := strings.TrimSpace(filterRaw)
	queryTrimmed := strings.TrimSpace(queryRaw)

	if filterTrimmed != "" && queryTrimmed != "" && filterTrimmed != queryTrimmed {
		return "", errors.New(conflictingFilterAliasError)
	}
	if filterTrimmed != "" {
		return filterRaw, nil
	}
	if queryTrimmed != "" {
		return queryRaw, nil
	}
	return "", nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

// parseBoolArg parses a boolean tool argument. It accepts a real JSON bool, or
// the case-insensitive strings "true"/"false" (so legacy string-typed callers
// keep working after the schema is declared as a real boolean). Any other
// non-empty value is a typed error so the LLM gets a correctable message rather
// than the value being silently dropped.
//
// Returns (value, present, error):
//   - present is false when the key is absent or an empty string (treat as "not set")
//   - error is non-nil only when a present value cannot be interpreted as a bool
func parseBoolArg(args map[string]any, key string) (bool, bool, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return false, false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, true, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return false, false, nil
		}
		switch strings.ToLower(s) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		default:
			return false, false, fmt.Errorf(`invalid %q value %q: must be a boolean (true or false)`, key, v)
		}
	default:
		return false, false, fmt.Errorf(`invalid %q value: must be a boolean (true or false)`, key)
	}
}

// AggregateRequest keeps parameters for any aggregation query.
type AggregateRequest struct {
	AggregationExpr  string
	FilterExpression string
	GroupBy          []types.SelectField
	OrderExpr        string
	OrderDir         string
	Limit            int
	LimitClamped     bool
	StartTime        int64
	EndTime          int64
	RequestType      string // "scalar" (default) or "time_series"
	StepInterval     *int64 // nil = let backend auto-select
	// StepIntervalWarning is set when a stepInterval value was provided but could
	// not be parsed as a positive integer. The handler logs it (WARN) so a
	// silently-dropped value is detectable rather than vanishing.
	StepIntervalWarning string
}

// parseAggregateArgs validates and parses  aggregate arguments.
// this is crucial as the input is provided by llm and if there is an error it must be suggested how to correct
func parseAggregateArgs(args map[string]any, signal string, filterExpr string) (*AggregateRequest, error) {
	aggregation, _ := args["aggregation"].(string)
	if aggregation == "" {
		return nil, fmt.Errorf(
			"%s \"aggregation\" is required. Supported values: %s. "+
				"Tip: for simple totals use {\"aggregation\": \"count\", \"groupBy\": \"service.name\"}",
			validationErrorPrefix, allowedAggregations)
	}
	if !validAggregations[aggregation] {
		return nil, fmt.Errorf(
			"%s \"aggregation\" is invalid (%q). Supported values: %s. "+
				"Tip: for counting use \"count\", for averages use \"avg\"",
			validationErrorPrefix, aggregation, allowedAggregations)
	}

	aggregateOn, _ := args["aggregateOn"].(string)
	if !aggregationsWithoutField[aggregation] && aggregateOn == "" {
		return nil, fmt.Errorf(
			"%s \"aggregateOn\" is required for %q aggregation. Specify the field to aggregate, "+
				"e.g. {\"aggregation\": \"%s\", \"aggregateOn\": \"duration\"}",
			validationErrorPrefix, aggregation, aggregation)
	}

	var aggregationExpr string
	if aggregateOn != "" {
		aggregationExpr = fmt.Sprintf("%s(%s)", aggregation, aggregateOn)
	} else {
		aggregationExpr = fmt.Sprintf("%s()", aggregation)
	}

	var groupByFields []types.SelectField
	if groupByStr, _ := args["groupBy"].(string); groupByStr != "" {
		for _, field := range strings.Split(groupByStr, ",") {
			field = strings.TrimSpace(field)
			if field != "" {
				groupByFields = append(groupByFields, aggregateGroupByField(signal, field))
			}
		}
	}

	orderByRaw, _ := args["orderBy"].(string)
	orderByStr := strings.TrimSpace(orderByRaw)
	orderExpr, orderDir := aggregationExpr, "desc"
	if orderByStr != "" {
		lower := strings.ToLower(orderByStr)
		switch {
		case strings.HasSuffix(lower, " asc"):
			orderExpr = strings.TrimSpace(orderByStr[:len(orderByStr)-4])
			orderDir = "asc"
		case strings.HasSuffix(lower, " desc"):
			orderExpr = strings.TrimSpace(orderByStr[:len(orderByStr)-5])
		default:
			orderExpr = orderByStr
		}
	}

	limit, err := intArg(args, "limit", types.DefaultAggregateQueryLimit)
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
	}, nil
}

func aggregateGroupByField(signal, name string) types.SelectField {
	if signal == "traces" {
		if field, ok := traceGroupByFieldMetadata[name]; ok {
			return field
		}
	}
	return types.SelectField{Name: name, Signal: signal}
}

func resolveTimestamps(args map[string]any, defaultRange string) (int64, int64, error) {
	// Reject a present-but-malformed start/end LOUDLY before falling through to
	// the default window. GetTimestampsWithDefaults silently defaults on a bad
	// value, which would hand back the wrong time range with no error.
	if err := timeutil.ValidateExplicitTimestamps(args); err != nil {
		return 0, 0, err
	}
	// Inject the tool's advertised default window when there is no usable explicit
	// time input — no usable timeRange string AND no usable explicit start. A
	// present-but-empty start (which timeutil treats as absent) must NOT block the
	// default, or GetTimestampsWithDefaults silently falls back to its generic 6h
	// window. A present-but-non-string timeRange is already rejected loudly by
	// ValidateExplicitTimestamps above; an empty-string timeRange means "use default".
	if tr, ok := args["timeRange"].(string); !ok || tr == "" {
		if !timeutil.HasUsableTimestamp(args, "start") {
			args["timeRange"] = defaultRange
		}
	}
	start, end := timeutil.GetTimestampsWithDefaults(args, "ms")
	var startTime, endTime int64
	if err := json.Unmarshal([]byte(start), &startTime); err != nil {
		return 0, 0, fmt.Errorf("invalid start timestamp: use timeRange instead (e.g., \"1h\", \"24h\")")
	}
	if err := json.Unmarshal([]byte(end), &endTime); err != nil {
		return 0, 0, fmt.Errorf("invalid end timestamp: use timeRange instead (e.g., \"1h\", \"24h\")")
	}
	return startTime, endTime, nil
}

// parseStepInterval parses an optional stepInterval (seconds) argument for the
// aggregate tools. It accepts a real JSON number OR a string that is ENTIRELY a
// positive integer. It deliberately does NOT use parseIntLoose for the string
// case: parseIntLoose scans a numeric prefix via Sscanf("%d"), so "1h"/"60s"
// would silently become 1/60 — a wrong bucket size. A present-but-invalid value
// (non-numeric, suffixed, or <= 0) yields a nil interval and a warning so the
// backend auto-selects rather than applying a silently-wrong granularity.
//
// Returns (nil, "") when the argument is absent or an empty string.
func parseStepInterval(raw any) (*int64, string) {
	if raw == nil {
		return nil, ""
	}
	warn := func() (*int64, string) {
		return nil, fmt.Sprintf(
			"stepInterval %v could not be parsed as a positive integer number of seconds; letting the backend auto-select the bucket size",
			raw)
	}
	switch v := raw.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, "" // treat empty string as not set
		}
		// Require the ENTIRE string to be a base-10 integer (no "1h"/"60s"/hex).
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return warn()
		}
		return &n, ""
	case float64:
		// JSON numbers decode as float64. Reject non-integral values (e.g.
		// 60.9) rather than truncating to 60 — the contract is "positive
		// integer seconds", so a fractional value is invalid → auto-select.
		if v != float64(int64(v)) || v <= 0 {
			return warn()
		}
		n := int64(v)
		return &n, ""
	case json.Number:
		// A json.Number preserves the literal; require it parse as a positive
		// integer (Int64 errors on a fractional literal like "60.9").
		n, err := v.Int64()
		if err != nil || n <= 0 {
			return warn()
		}
		return &n, ""
	case int:
		if v <= 0 {
			return warn()
		}
		n := int64(v)
		return &n, ""
	case int64:
		if v <= 0 {
			return warn()
		}
		return &v, ""
	default:
		return warn()
	}
}

// MaxRawResultLimit caps how many raw rows search_logs / search_traces will
// request from the backend. Each row is read fully into memory, decoded, and
// re-marshaled into the tool response (~1.6 MiB per 1000 log rows measured
// against a real backend), so an uncapped limit is an unbounded single-request
// memory vector on the shared, memory-limited multi-tenant pod. Callers that
// need more rows should paginate via offset.
const MaxRawResultLimit = 10000

// clampLimit bounds a parsed limit to MaxRawResultLimit. It returns the
// effective limit and whether clamping occurred so handlers can surface it.
func clampLimit(n int) (int, bool) {
	if n > MaxRawResultLimit {
		return MaxRawResultLimit, true
	}
	return n, false
}

// extractBackendWarningMessages parses non-fatal QB v5 warning messages from a
// success response. It fails open: malformed or unexpected response shapes
// simply produce no notes and never block returning the raw backend payload.
func extractBackendWarningMessages(response json.RawMessage) []string {
	var resp struct {
		Data struct {
			Warning struct {
				Warnings []struct {
					Message string `json:"message"`
				} `json:"warnings"`
			} `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return nil
	}
	var messages []string
	for _, warning := range resp.Data.Warning.Warnings {
		if strings.TrimSpace(warning.Message) != "" {
			messages = append(messages, warning.Message)
		}
	}
	return messages
}

func backendWarningsNote(messages []string) string {
	var b strings.Builder
	b.WriteString("note: SigNoz backend returned non-fatal warnings:")
	for _, message := range messages {
		b.WriteString("\n- ")
		b.WriteString(message)
	}
	return b.String()
}

func warnBackendWarnings(ctx context.Context, logger *slog.Logger, toolName string, messages []string) {
	if len(messages) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"SigNoz query builder returned non-fatal warnings",
		slog.String("tool", toolName),
		slog.Int("warningCount", len(messages)),
	)
}

// warnUnparsedWarningEnvelope detects QB warning-envelope drift: it counts
// data.warning.warnings entries and warns only when entries exist but zero
// messages were extracted (a renamed message field). Empty/absent envelopes
// stay silent. Fails open.
func warnUnparsedWarningEnvelope(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, extractedCount int) {
	if extractedCount > 0 {
		return
	}
	var probe struct {
		Data struct {
			Warning struct {
				Warnings []json.RawMessage `json:"warnings"`
			} `json:"warning"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return
	}
	entries := 0
	for _, raw := range probe.Data.Warning.Warnings {
		switch string(bytes.TrimSpace(raw)) {
		case "", "null", "{}":
			continue // empty/degenerate entry — not drift
		}
		entries++
	}
	if entries == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"qb warning envelope unparsed",
		slog.String("tool", toolName),
	)
}

// resultWithNotes wraps a raw JSON payload as a tool result. The JSON is always
// the first (parseable) content block; notes are appended as separate blocks
// rather than prepended into the JSON.
func resultWithNotes(payload []byte, notes ...string) *mcp.CallToolResult {
	res := mcp.NewToolResultText(string(payload))
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res
}

// structuredResultWithNotes is structuredResult plus trailing advisory note
// blocks (as resultWithNotes appends them). For code-controlled tools that must
// surface a human-readable warning (e.g. a post-create test-send failure)
// without dropping the stable StructuredContent shape.
func structuredResultWithNotes(payload []byte, notes ...string) *mcp.CallToolResult {
	res := structuredResult(payload)
	for _, note := range notes {
		if strings.TrimSpace(note) == "" {
			continue
		}
		res.Content = append(res.Content, mcp.NewTextContent(note))
	}
	return res
}

// countQueryRangeRows sums the number of rows across all results in a QB v5
// query_range passthrough body. The expected nesting is
// data.data.results[].rows[] (a render.Success envelope wrapping a
// QueryRangeResponse), the same shape util.InjectRowsWebURL walks. It fails
// open: it returns (0, false) on any shape it cannot walk so a completeness
// note is simply omitted rather than wrong.
func countQueryRangeRows(payload []byte) (int, bool) {
	// Walk with json.RawMessage at each level so we can distinguish a MISSING /
	// null / non-array leaf from a genuinely-empty array. A missing or null
	// "results"/"rows" key means we could not locate the rows — we must NOT
	// claim count=0 (which would assert a misleading hasMore=false); fail open
	// to (0, false) so the caller emits the generic "more may exist" note.
	var envelope struct {
		Data struct {
			Data struct {
				Results json.RawMessage `json:"results"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return 0, false
	}
	// A present-but-null "results" is a normal empty response (matching the
	// InjectRowsWebURL contract), so count it as zero. Only a MISSING or
	// non-array, non-null "results" is uncountable drift.
	results, ok := decodeArrayOrNull(envelope.Data.Data.Results)
	if !ok {
		return 0, false
	}
	total := 0
	for _, rawResult := range results {
		var result struct {
			// Use RawMessage (not []json.RawMessage) so a missing "rows" key on
			// any single result is detectable rather than silently 0.
			Rows json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(rawResult, &result); err != nil {
			return 0, false
		}
		// A present "rows":null is a normal empty result per the v5 /
		// InjectRowsWebURL contract — count it as zero rows. Only a MISSING
		// "rows" key (always emitted by the contract) or a non-array, non-null
		// value is drift → fail open.
		if len(result.Rows) == 0 {
			return 0, false // "rows" key absent — contract drift
		}
		rows, ok := decodeArrayOrNull(result.Rows)
		if !ok {
			return 0, false // "rows" present but not an array or null — drift
		}
		total += len(rows)
	}
	return total, true
}

// countAlertHistoryRows counts the alert history rows in either response shape:
// the v2 GET /api/v2/rules/{id}/history/timeline body (data.items[]) or a bare
// data[] array. A present null at data or data.items is a known empty
// collection; missing or unrecognized shapes fail open.
func countAlertHistoryRows(payload []byte) (int, bool) {
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return 0, false
	}
	if len(resp.Data) == 0 {
		return 0, false // data key absent
	}
	if arr, ok := decodeArrayOrNull(resp.Data); ok {
		return len(arr), true // data[] or data:null
	}

	var dataObj map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &dataObj); err != nil {
		return 0, false // data present but neither array nor object
	}
	raw, present := dataObj["items"]
	if !present || len(raw) == 0 {
		return 0, false // items key absent
	}
	arr, ok := decodeArrayOrNull(raw)
	if !ok {
		return 0, false // items present but neither array nor null
	}
	return len(arr), true
}

// countDataArrayRows counts the elements of a JSON array nested at data.<key>
// in a passthrough body (e.g. data.metrics for list_metrics, data.samples for
// top_metrics). A present-but-null leaf counts as zero rows (a normal empty
// collection). It fails open — returns (0, false) — only when the key is ABSENT
// or its value is neither an array nor null, so a misleading hasMore=false is
// never asserted on an uncountable shape.
func countDataArrayRows(payload []byte, key string) (int, bool) {
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return 0, false
	}
	raw, present := resp.Data[key]
	if !present || len(raw) == 0 {
		return 0, false // key absent
	}
	arr, ok := decodeArrayOrNull(raw)
	if !ok {
		return 0, false // present but neither array nor null
	}
	return len(arr), true
}

// decodeArrayOrNull decodes raw as a JSON array OR a literal null, returning the
// elements (nil for null) and true. A present null is a NORMAL empty collection
// (matching the InjectRowsWebURL contract), so it counts as zero rows rather
// than as uncountable drift. It returns (nil, false) only when raw is
// absent/empty or is a non-array, non-null value. Callers handle the
// absent-key case separately (len(raw)==0) when "missing" must be distinguished
// from "present null".
func decodeArrayOrNull(raw json.RawMessage) ([]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false // key absent
	}
	if string(bytesTrimSpace(raw)) == "null" {
		return nil, true // literal null == normal empty collection
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false // not an array
	}
	if arr == nil {
		// Defensive: json.Unmarshal into a slice leaves it nil for "null",
		// which we already handled above; any other nil here is treated as
		// empty so a genuinely-empty array is never mistaken for drift.
		return nil, true
	}
	return arr, true
}

// bytesTrimSpace trims ASCII whitespace from a json.RawMessage without
// allocating a new string, so the "null" literal check tolerates surrounding
// whitespace.
func bytesTrimSpace(b json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(b)))
}

// completenessNote builds a pagination/completeness advisory for raw-passthrough
// tools that accept limit/offset but expose no hasMore signal of their own. When
// the returned row count is known it infers hasMore from returnedRows == limit
// and reports the nextOffset; otherwise it falls back to a generic "limit
// applied; more may exist" note so the LLM is never left assuming completeness.
func completenessNote(returnedRows, limit, offset int, rowsKnown bool) string {
	if !rowsKnown {
		return fmt.Sprintf(
			"note: limit %d applied; this tool cannot count returned rows, so more results may exist. Paginate with \"offset\" (or narrow the query) to be sure.",
			limit)
	}
	hasMore := limit > 0 && returnedRows >= limit
	nextOffset := offset + returnedRows
	if hasMore {
		return fmt.Sprintf(
			"note: returned %d rows (limit %d) — more results likely exist (hasMore=true). Fetch the next page with offset=%d.",
			returnedRows, limit, nextOffset)
	}
	return fmt.Sprintf(
		"note: returned %d rows (limit %d) — all matching results returned (hasMore=false).",
		returnedRows, limit)
}

// alertHistoryCompletenessNote is the completeness advisory for the v2 rule
// state-history timeline, which paginates with an opaque cursor rather than an
// offset. When the response carries a non-empty data.nextCursor, more pages
// exist and the caller must pass it back as "cursor"; otherwise it reports the
// page as complete. If the cursor field can't be read (unexpected shape), it
// falls back to the row-count heuristic without asserting a cursor.
func alertHistoryCompletenessNote(payload []byte, returnedRows, limit int, rowsKnown bool, start, end int64, order string) string {
	var resp struct {
		Data struct {
			NextCursor string `json:"nextCursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &resp); err == nil && resp.Data.NextCursor != "" {
		continuation := fmt.Sprintf(
			"Fetch the next page with cursor=%q, start=%d, end=%d, order=%q, and the same state and filter.",
			resp.Data.NextCursor, start, end, order)
		if rowsKnown {
			if limit == 0 {
				return fmt.Sprintf(
					"note: returned %d rows — more results exist (hasMore=true). %s",
					returnedRows, continuation)
			}
			return fmt.Sprintf(
				"note: returned %d rows (limit %d) — more results exist (hasMore=true). %s",
				returnedRows, limit, continuation)
		}
		return fmt.Sprintf(
			"note: more results exist (hasMore=true). %s",
			continuation)
	}
	if !rowsKnown {
		if limit == 0 {
			return "note: this tool cannot count returned rows, so more results may exist. Paginate with \"cursor\" (or narrow the query) to be sure."
		}
		return fmt.Sprintf(
			"note: limit %d applied; this tool cannot count returned rows, so more results may exist. Paginate with \"cursor\" (or narrow the query) to be sure.",
			limit)
	}
	if limit == 0 {
		return fmt.Sprintf(
			"note: returned %d rows — all matching results returned (hasMore=false).",
			returnedRows)
	}
	return fmt.Sprintf(
		"note: returned %d rows (limit %d) — all matching results returned (hasMore=false).",
		returnedRows, limit)
}

// limitOnlyCompletenessNote is the completeness advisory for list tools that
// expose a `limit` but NO offset pagination (e.g. signoz_list_metrics). It must
// not tell callers to "fetch the next page with offset" — there is no offset
// param, so the caller would loop on the same page. Instead it advises
// narrowing the result set (searchText / time range / source). narrowHint names
// the concrete params for the tool.
func limitOnlyCompletenessNote(returnedRows, limit int, rowsKnown bool, narrowHint string) string {
	if !rowsKnown {
		return fmt.Sprintf(
			"note: limit %d applied; this tool cannot count returned rows, so more results may exist. Narrow the result set (%s) to be sure.",
			limit, narrowHint)
	}
	if limit > 0 && returnedRows >= limit {
		return fmt.Sprintf(
			"note: returned %d rows (limit %d) — more results likely exist (hasMore=true). This tool has no offset paging; narrow the result set (%s) to surface the rest.",
			returnedRows, limit, narrowHint)
	}
	return fmt.Sprintf(
		"note: returned %d rows (limit %d) — all matching results returned (hasMore=false).",
		returnedRows, limit)
}

// isTrivialBody reports whether a payload is effectively empty ("", {}, [], null)
// so the drift WARN doesn't fire on a legitimately empty response.
func isTrivialBody(payload []byte) bool {
	switch string(bytes.TrimSpace(payload)) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

// warnRowCountUnknown warns when the row counter couldn't locate the rows array
// on a non-trivial body (likely a renamed "rows" field). Fails open.
func warnRowCountUnknown(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, rowsKnown bool) {
	if rowsKnown || isTrivialBody(payload) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx,
		"row count unknown on non-empty body",
		slog.String("tool", toolName),
	)
}

// rawSearchResult is the result wrapper for raw row tools (search_logs /
// search_traces), which support offset pagination. It appends a completeness
// note (hasMore + nextOffset) inferred from the returned row count so callers
// never silently assume a truncated page is complete.
func rawSearchResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limit, offset int, limitClamped bool) *mcp.CallToolResult {
	var notes []string
	if limitClamped {
		notes = append(notes, fmt.Sprintf(
			"note: result limited to %d rows to bound server memory; paginate with \"offset\" (or narrow the time range/filters) for more.",
			MaxRawResultLimit))
	}
	returnedRows, rowsKnown := countQueryRangeRows(payload)
	warnRowCountUnknown(ctx, logger, toolName, payload, rowsKnown)
	notes = append(notes, completenessNote(returnedRows, limit, offset, rowsKnown))
	warnings := extractBackendWarningMessages(payload)
	warnBackendWarnings(ctx, logger, toolName, warnings)
	warnUnparsedWarningEnvelope(ctx, logger, toolName, payload, len(warnings))
	if len(warnings) > 0 {
		notes = append(notes, backendWarningsNote(warnings))
	}
	return resultWithNotes(payload, notes...)
}

// aggregateResult is the result wrapper for aggregation tools. Aggregations
// have no offset pagination, so the note advises narrowing the query instead.
func aggregateResult(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, limitClamped bool) *mcp.CallToolResult {
	var notes []string
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
