package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/SigNoz/signoz-mcp-server/pkg/querybuilder"
)

// maxZeroFillPoints bounds how many zero points one aggregate result may gain
// (about 38 characters each), so a grouped query over many sparse groups
// cannot grow without limit. Above it nothing is filled and the result says
// how to ask for a smaller series.
const maxZeroFillPoints = 1500

// zeroWhenEmptyFunctions are the aggregation functions whose value over no
// rows is zero by definition: counts, sums, and rates (a count or sum divided
// by the step). Names are lower case, with SigNoz's aliases.
var zeroWhenEmptyFunctions = map[string]bool{
	"count": true, "countif": true, "count_if": true,
	"countdistinct": true, "count_distinct": true,
	"countdistinctif": true, "count_distinct_if": true,
	"sum": true, "sumif": true, "sum_if": true,
	"rate": true, "rateif": true, "rate_if": true, "rate_sum": true,
}

// zeroWhenEmpty reports whether an aggregation expression is a single call to
// one of zeroWhenEmptyFunctions. Anything else, such as a ratio of two counts,
// can have no value over no rows, so it keeps its gaps.
func zeroWhenEmpty(expression string) bool {
	expr := strings.TrimSpace(expression)
	open := strings.IndexByte(expr, '(')
	if open <= 0 || !zeroWhenEmptyFunctions[strings.ToLower(strings.TrimSpace(expr[:open]))] {
		return false
	}
	return closingParen(expr, open) == len(expr)-1
}

// closingParen returns the index of the parenthesis that closes the one at
// open, skipping quoted text, or -1 when it is never closed.
func closingParen(s string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// zeroFillIndexes returns the positions of the aggregations whose empty
// buckets read as zero, or nil when none qualify, and the timeShift in ms.
// having can drop a bucket that had rows, and functions other than timeShift
// turn a count into another value, so either one leaves every series as
// SigNoz returned it. SigNoz moves the read window by the first timeShift in
// whole seconds but each point by every timeShift, so the bucket grid is known
// only for a single whole-second shift.
func zeroFillIndexes(reqData *AggregateRequest) (indexes []int, shiftMs int64) {
	if reqData.RequestType != "time_series" || reqData.Having != "" {
		return nil, 0
	}
	for i, function := range reqData.Functions {
		if function.Name == nil || !strings.EqualFold(string(*function.Name), "timeShift") || i > 0 {
			return nil, 0
		}
		var whole bool
		if shiftMs, whole = timeShiftMs(function); !whole {
			return nil, 0
		}
	}
	for i, expression := range reqData.Aggregations {
		if zeroWhenEmpty(expression) {
			indexes = append(indexes, i)
		}
	}
	return indexes, shiftMs
}

// timeShiftMs reads a timeShift's first argument, a number or numeric string
// of seconds; ok is false unless it is a whole number of seconds.
func timeShiftMs(function querybuilder.Function) (ms int64, ok bool) {
	if function.Args == nil || len(*function.Args) == 0 || (*function.Args)[0].Value == nil {
		return 0, false
	}
	raw, err := json.Marshal((*function.Args)[0].Value)
	if err != nil {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(strings.Trim(string(raw), `"`), 64)
	if err != nil || seconds != math.Trunc(seconds) {
		return 0, false
	}
	return int64(seconds) * 1000, true
}

// fillEmptyBuckets gives each zero-when-empty series a 0 point for every bucket
// of the requested window it has no point for, on the step SigNoz reports it
// applied. A series SigNoz did not return, such as a group with no rows in the
// window, stays absent, and the members outside the filled series keep their
// order and bytes. The payload comes back unchanged when nothing qualifies or
// is missing, and also when the response is not the expected shape, which is
// logged as a WARN.
//
// SigNoz's formatOptions.fillGaps is not used: it zero-fills every aggregation
// of the request, percentiles included, and adds a zero bucket that starts at
// the window's end.
func fillEmptyBuckets(ctx context.Context, logger *slog.Logger, toolName string, payload []byte, reqData *AggregateRequest) ([]byte, []string) {
	indexes, shiftMs := zeroFillIndexes(reqData)
	if len(indexes) == 0 {
		return payload, nil
	}
	fill := map[int]bool{}
	for _, index := range indexes {
		fill[index] = true
	}
	plan, err := planZeroFill(payload, fill, reqData.StartTime, reqData.EndTime, shiftMs)
	if err != nil {
		logger.WarnContext(ctx, "time series left unfilled: unexpected query_range response",
			slog.String("tool", toolName), slog.String("reason", err.Error()))
		return payload, nil
	}
	if plan.added == 0 {
		return payload, nil
	}
	if plan.added > maxZeroFillPoints {
		return payload, []string{fmt.Sprintf(zeroFillSkippedNote, plan.added, maxZeroFillPoints)}
	}
	return plan.apply(), []string{zeroFilledNote(reqData.Aggregations, plan.filledIndexes(), plan.stepSeconds)}
}

const zeroFillSkippedNote = "note: empty buckets are left out, not filled with 0: filling them would add %d points, more than %d. " +
	"For zero-filled counts, raise stepInterval, lower limit, or narrow the filter or time range."

// zeroFilledNote names the aggregations whose series gained zeros, and says
// that the others are as SigNoz returned them.
func zeroFilledNote(aggregations []string, indexes []int, stepSeconds int64) string {
	names := make([]string, 0, len(indexes))
	for _, index := range indexes {
		names = append(names, fmt.Sprintf("__result_%d (%s)", index, aggregations[index]))
	}
	verb := "is"
	if len(names) > 1 {
		verb = "are"
	}
	note := fmt.Sprintf("note: %s %s 0 for each %ds bucket in the window with no matching rows.", strings.Join(names, ", "), verb, stepSeconds)
	if len(indexes) < len(aggregations) {
		note += " Other aggregations are unchanged; a bucket missing from their series has no value."
	}
	return note
}

// zeroFillPlan holds the decoded path from the payload down to the series
// that lack buckets, so apply can splice the zeros in and re-encode only that
// path.
type zeroFillPlan struct {
	root, data, inner *jsonObject
	results           []json.RawMessage
	edits             []resultEdit
	added             int
	stepSeconds       int64
	startMs, endMs    int64
}

type resultEdit struct {
	position     int
	result       *jsonObject
	aggregations []json.RawMessage
	edits        []aggregationEdit
}

type aggregationEdit struct {
	position    int
	index       int // the aggregation's __result_N
	aggregation *jsonObject
	series      []json.RawMessage
	gaps        []seriesGap
}

type seriesGap struct {
	position    int
	series      *jsonObject
	have        map[int64]json.RawMessage // bucket start in ms -> SigNoz's point
	first, last int64                     // first and last bucket start in ms
	stepMs      int64
}

func planZeroFill(payload []byte, fill map[int]bool, startMs, endMs, shiftMs int64) (*zeroFillPlan, error) {
	plan := &zeroFillPlan{startMs: startMs, endMs: endMs}
	var err error
	if plan.root, err = decodeJSONObject(payload); err != nil {
		return nil, fmt.Errorf("body: %w", err)
	}
	if plan.data, err = decodeJSONObject(plan.root.members["data"]); err != nil {
		return nil, fmt.Errorf("data: %w", err)
	}
	var meta struct {
		StepIntervals map[string]json.Number `json:"stepIntervals"`
	}
	if raw, ok := plan.data.members["meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("data.meta: %w", err)
		}
	}
	if plan.inner, err = decodeJSONObject(plan.data.members["data"]); err != nil {
		return nil, fmt.Errorf("data.data: %w", err)
	}
	rawResults, ok := plan.inner.members["results"]
	if !ok {
		return nil, fmt.Errorf("data.data.results is missing")
	}
	if plan.results, err = decodeJSONArray(rawResults); err != nil {
		return nil, fmt.Errorf("data.data.results: %w", err)
	}
	for r, rawResult := range plan.results {
		result, err := decodeJSONObject(rawResult)
		if err != nil {
			return nil, fmt.Errorf("result: %w", err)
		}
		aggregations, err := decodeJSONArray(result.members["aggregations"])
		if err != nil {
			return nil, fmt.Errorf("aggregations: %w", err)
		}
		if len(aggregations) == 0 {
			continue
		}
		var name string
		_ = json.Unmarshal(result.members["queryName"], &name)
		step, err := meta.StepIntervals[name].Int64()
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("no step interval for query %q", name)
		}
		plan.stepSeconds = step
		stepMs := step * 1000
		// SigNoz buckets the rows of [start, end) by toStartOfInterval, so the
		// first and last buckets can begin before start and run past end. With
		// timeShift it buckets the earlier window, then moves each point forward.
		first := floorTo(startMs-shiftMs, stepMs) + shiftMs
		last := floorTo(endMs-1-shiftMs, stepMs) + shiftMs
		buckets := int((last-first)/stepMs) + 1
		resultEdit := resultEdit{position: r, result: result, aggregations: aggregations}
		for a, rawAggregation := range aggregations {
			aggregation, err := decodeJSONObject(rawAggregation)
			if err != nil {
				return nil, fmt.Errorf("aggregation: %w", err)
			}
			var index int
			if err := json.Unmarshal(aggregation.members["index"], &index); err != nil {
				return nil, fmt.Errorf("aggregation index: %w", err)
			}
			if !fill[index] {
				continue
			}
			series, err := decodeJSONArray(aggregation.members["series"])
			if err != nil {
				return nil, fmt.Errorf("series: %w", err)
			}
			aggregationEdit := aggregationEdit{position: a, index: index, aggregation: aggregation, series: series}
			for s, rawSeries := range series {
				gap, err := seriesGapOf(rawSeries, first, last, stepMs)
				if err != nil {
					return nil, err
				}
				if gap == nil || len(gap.have) == buckets {
					continue
				}
				gap.position = s
				plan.added += buckets - len(gap.have)
				aggregationEdit.gaps = append(aggregationEdit.gaps, *gap)
			}
			if len(aggregationEdit.gaps) > 0 {
				resultEdit.edits = append(resultEdit.edits, aggregationEdit)
			}
		}
		if len(resultEdit.edits) > 0 {
			plan.edits = append(plan.edits, resultEdit)
		}
	}
	return plan, nil
}

// filledIndexes lists, in order, the aggregations with at least one series that gains zeros.
func (plan *zeroFillPlan) filledIndexes() []int {
	filled := map[int]bool{}
	for _, resultEdit := range plan.edits {
		for _, aggregationEdit := range resultEdit.edits {
			filled[aggregationEdit.index] = true
		}
	}
	indexes := make([]int, 0, len(filled))
	for index := range filled {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	return indexes
}

// seriesGapOf reads one series' points. It returns nil for a series with no
// points, and an error when a point is off the window's bucket grid or two
// points share a bucket (SigNoz can merge two groups whose sorted label values
// match), so no point is ever dropped.
func seriesGapOf(rawSeries json.RawMessage, first, last, stepMs int64) (*seriesGap, error) {
	series, err := decodeJSONObject(rawSeries)
	if err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}
	values, err := decodeJSONArray(series.members["values"])
	if err != nil {
		return nil, fmt.Errorf("values: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	have := make(map[int64]json.RawMessage, len(values))
	for _, rawPoint := range values {
		var point struct {
			Timestamp int64 `json:"timestamp"`
		}
		if err := json.Unmarshal(rawPoint, &point); err != nil {
			return nil, fmt.Errorf("point: %w", err)
		}
		ts := point.Timestamp
		if ts < first || ts > last || (ts-first)%stepMs != 0 {
			return nil, fmt.Errorf("point at %d is off the %d ms bucket grid starting at %d", ts, stepMs, first)
		}
		if _, seen := have[ts]; seen {
			return nil, fmt.Errorf("two points at %d in one series", ts)
		}
		have[ts] = rawPoint
	}
	return &seriesGap{series: series, have: have, first: first, last: last, stepMs: stepMs}, nil
}

// apply writes every bucket of each gapped series in time order, SigNoz's
// point where it returned one and a zero point otherwise, then re-encodes the
// path up to the root. A zero point for a bucket the window covers only in
// part is marked partial, as SigNoz marks its own points.
func (plan *zeroFillPlan) apply() []byte {
	for _, resultEdit := range plan.edits {
		for _, aggregationEdit := range resultEdit.edits {
			for _, gap := range aggregationEdit.gaps {
				values := make([]json.RawMessage, 0, (gap.last-gap.first)/gap.stepMs+1)
				for ts := gap.first; ts <= gap.last; ts += gap.stepMs {
					if point, ok := gap.have[ts]; ok {
						values = append(values, point)
					} else if ts < plan.startMs || ts+gap.stepMs > plan.endMs {
						values = append(values, json.RawMessage(fmt.Sprintf(`{"timestamp":%d,"partial":true,"value":0}`, ts)))
					} else {
						values = append(values, json.RawMessage(fmt.Sprintf(`{"timestamp":%d,"value":0}`, ts)))
					}
				}
				gap.series.members["values"] = encodeJSONArray(values)
				aggregationEdit.series[gap.position] = gap.series.encode()
			}
			aggregationEdit.aggregation.members["series"] = encodeJSONArray(aggregationEdit.series)
			resultEdit.aggregations[aggregationEdit.position] = aggregationEdit.aggregation.encode()
		}
		resultEdit.result.members["aggregations"] = encodeJSONArray(resultEdit.aggregations)
		plan.results[resultEdit.position] = resultEdit.result.encode()
	}
	plan.inner.members["results"] = encodeJSONArray(plan.results)
	plan.data.members["data"] = plan.inner.encode()
	plan.root.members["data"] = plan.data.encode()
	return plan.root.encode()
}

// floorTo rounds ms down to a multiple of step.
func floorTo(ms, step int64) int64 {
	floor := ms - ms%step
	if ms < 0 && ms%step != 0 {
		floor -= step
	}
	return floor
}

// jsonObject is a JSON object that keeps its members' order and raw bytes, so
// re-encoding it changes only the members that were replaced.
type jsonObject struct {
	keys    []string
	members map[string]json.RawMessage
}

func decodeJSONObject(raw json.RawMessage) (*jsonObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("not a JSON object")
	}
	object := &jsonObject{members: map[string]json.RawMessage{}}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if _, seen := object.members[key]; !seen {
			object.keys = append(object.keys, key)
		}
		object.members[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return object, nil
}

func (object *jsonObject) encode() json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range object.keys {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		out.Write(name)
		out.WriteByte(':')
		out.Write(object.members[key])
	}
	out.WriteByte('}')
	return out.Bytes()
}

// decodeJSONArray reads an array's items as raw bytes; a missing or null
// member reads as no items.
func decodeJSONArray(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("not a JSON array")
	}
	return items, nil
}

func encodeJSONArray(items []json.RawMessage) json.RawMessage {
	out := json.RawMessage{'['}
	for i, item := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, item...)
	}
	return append(out, ']')
}
