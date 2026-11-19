# Plan: Zero Buckets in Count Series

Status: In Progress
Issue:
PR:

## Context

`signoz_aggregate_logs` and `signoz_aggregate_traces` with `requestType: time_series` pass
SigNoz's series through, and SigNoz leaves out every bucket in which no rows matched. A reader
cannot tell "nothing happened in this bucket" from "the result has no entry for this step", and a
service that goes quiet shows up as a shorter series instead of a drop to zero. Questions such as
"when did this service stop logging?" or "did errors stop after the deploy?" depend on that
difference.

No tracker issue covers this change. For latency series the opposite rule already holds:
[SigNoz/nerve-pod#622](https://github.com/SigNoz/nerve-pod/issues/622) keeps p95 gaps absent,
because a zero there would read as a measurement.

## Approach

- For a time series, an aggregation whose value over no rows is zero by definition gets a 0 point
  for each bucket of the requested window it has no point for. Every other aggregation keeps its
  gaps, including in a request that mixes the two.
- The server fills the series after SigNoz answers, on the step SigNoz reports in
  `data.meta.stepIntervals`. Only series SigNoz returned are filled. Members outside the filled
  `values` arrays keep their order and bytes; SigNoz sends compact JSON, so the payload differs
  from SigNoz's only in those arrays.
- A note names the filled series. A response without a step or results, with a point off the
  bucket grid, or with two points in one bucket passes through unchanged with a WARN log, so no
  point SigNoz sent is ever dropped.
- The `aggregations` description of both tools and the README state the rule; `requestType`
  points to it.

## Files to Modify

- `internal/handler/tools/zero_fill.go`: which aggregations qualify, the fill, and its notes.
- `internal/handler/tools/logs.go`, `internal/handler/tools/traces.go`: call the fill.
- `internal/handler/tools/query_param_descriptions.go`: the `aggregations` and `requestType`
  descriptions of both aggregate tools.
- `internal/mcp-server/testdata/wire-catalog/tools-list.json`: those four entries.
- `README.md`: the `requestType` line of both tools.
- `internal/handler/tools/zero_fill_test.go`: handler tests against a stub SigNoz.
- `tests/e2e/tests/test_result_shaping.py`: the fill against the pinned release.

## Key Decisions

### 2026-11-19 — Fill in the server, not with SigNoz's `formatOptions.fillGaps`

- Decision: keep sending `fillGaps: false` and fill qualifying series in the server.
- Rationale: `fillGaps` applies to every builder query and formula of the request, so a request
  that mixes `count()` with `p99(duration_nano)` would get zeros in the p99 series. Its fill loop
  also runs to `end` inclusive: on a local SigNoz v0.150.0-rc.1 instance, a window from 20:00 to
  22:00 with a 600 s step came back with a 22:00 bucket set to 0, although that bucket starts at
  `end`, lies outside the window, and holds 34 error spans. On an empty result it adds an
  unlabeled series of zeros even for a grouped query. Filling in the server applies the rule per
  aggregation, stays inside the window, and lets the server bound the growth.
- Alternatives rejected: setting `fillGaps` only when every aggregation qualifies. Mixed requests
  would keep their count gaps, the extra bucket at `end` would remain, and growth could not be
  bounded.

### 2026-11-19 — Which aggregations read as 0

- Decision: an expression qualifies when it is one call to `count`, `countIf`, `count_distinct`,
  `count_distinct_if`, `sum`, `sumIf`, `rate`, `rateIf`, or `rate_sum` (any case, with SigNoz's
  aliases). `avg`, `min`, `max`, percentiles, the `rate_avg`/`rate_min`/`rate_max` forms, and any
  expression that combines calls, such as `countIf(has_error = true) / count()`, keep their gaps.
  A request with `having`, or with a function other than `timeShift`, is left as SigNoz returned it,
  and so is one with more than one `timeShift` or a shift that is not a whole number of seconds:
  SigNoz moves the read window by the first shift in whole seconds but each point by every shift,
  so the grid is known only for one whole-second shift.
- Rationale: a count, a distinct count, and a sum over no rows are 0 by definition, and a rate is
  a count or sum divided by the step. The others have no value over no rows. SigNoz agrees: a
  scalar request over a filter matching no spans returned 0 for `count()`, `countIf(...)`,
  `count_distinct(name)`, `sum(duration_nano)`, `rate()`, and `rate_sum(duration_nano)`, and NaN
  for `avg`, `p99`, and the ratio. It also returned 0 for `min` and `max`, a storage default rather
  than a defined value, so they keep their gaps. `having` can drop a bucket that had rows, and
  functions such as `cumulativeSum` or `log2` turn the count into another value; `timeShift` only
  moves the points.
- The rule depends on the aggregation, so it goes on the `aggregations` description of both tools
  (SUR-9, DSC-3), and `requestType` says in one sentence that empty buckets read as 0 for counts,
  sums, and rates. The full rule does not fit the 512-byte target `guardrails.RequestTypeTargetBytes`
  sets for `requestType`. The README states it on the `requestType` line.

### 2026-11-19 — Bound the growth of grouped series

- Decision: a result gains at most 1,500 zero points (`maxZeroFillPoints`). Above that nothing is
  filled, and a note gives the number of points the fill would add and the next step: raise
  `stepInterval`, lower `limit`, or narrow the filter or time range (OUT-3). A group with no rows
  in the window has no series and stays absent.
- Rationale: a zero point is about 38 characters, so the bound adds at most about 57,000
  characters to a result, near the 50,000-character result limit of some MCP clients reported in
  [SigNoz/signoz-mcp-server#452](https://github.com/SigNoz/signoz-mcp-server/issues/452). An
  ungrouped count is filled at SigNoz's default resolution (about 300 buckets) and at one-minute
  buckets over a day (1,440). All or nothing keeps the result readable as one rule; a partial fill
  would leave the reader to work out which series carry zeros.
- Measured on a local SigNoz v0.150.0-rc.1 instance, error spans from 14:00 to 24:00 on
  2026-11-12, characters of the result text. "Filled would be" comes from the same build with the
  bound lifted:

  | Query | Before | After |
  |---|---|---|
  | count by `service.name`, 9 groups, 600 s step | 4,156 | 22,029 (filled, 468 zeros) |
  | count by `service.name`, 9 groups, 120 s default step | 7,457 | 7,654 (note; filled would be 104,142 with 2,542 zeros) |
  | count by `name`, 33 groups, 600 s step | 10,657 | 10,854 (note; filled would be 80,096 with 1,825 zeros) |
  | count by `name`, 33 groups, 120 s default step | 16,290 | 16,487 (note; filled would be 381,065 with 9,597 zeros) |

### 2026-11-19 — Which buckets

- Decision: every bucket start from `floor(start / step) * step` to `floor((end - 1) / step) * step`,
  on the step in `data.meta.stepIntervals`. With `timeShift` of `s`, the grid is
  `floor((start - s) / step) * step + s` to `floor((end - 1 - s) / step) * step + s`. A zero in a
  bucket the window covers only in part carries `"partial": true`.
- Rationale: SigNoz counts the rows of `[start, end)` and labels each bucket with
  `toStartOfInterval`, so the first and last buckets can be partial. Both are in the window, and a
  zero there means no rows in the part of the bucket the window covers; SigNoz marks its own
  points in those buckets `partial`, and the zeros follow the same rule. With `timeShift`, SigNoz
  queries `[start - s, end - s)` and adds `s` to each timestamp, so a shift that is not a multiple
  of the step (a week against the 285 s default step of a day) lands off the unshifted grid. The
  step is the one SigNoz applied, which can differ from the requested `stepInterval` when SigNoz
  raises it to its minimum.

### 2026-11-19 — Telling the reader

- Decision: when zeros are added, a note names each filled series as `__result_N (expression)`
  and the step, and in a mixed request says that the other aggregations are unchanged, so a bucket
  missing from their series has no value.
  Nothing is said when no bucket was missing.
- Rationale: the result is SigNoz's payload with points added, and OUT-5 rules out reshaping a
  passthrough silently. Notes are how the aggregate tools already report what the server did to a
  result (the group limit and the scalar `functions` note). The other members keep their bytes so
  the only change is the added points.
- Rejected in review: splicing the zeros into the original bytes at the `values` offsets so that
  formatted JSON or escaped keys also survive. SigNoz renders responses with `json.Marshal`, which
  writes compact JSON with plain keys, and the client passes the body through, so re-encoding the
  path from the root to each filled series yields SigNoz's bytes.

## Reference Links

- SigNoz `pkg/querier/postprocess.go` (`fillGaps`) and
  `pkg/types/querybuildertypes/querybuildertypesv5/functions.go` (`funcFillZero`), v0.150.0-rc.1
- SigNoz `pkg/querybuilder/agg_funcs.go`: the aggregation functions and aliases

## Verification

- `go test ./internal/handler/tools -run 'ZeroFill|ZeroWhenEmpty|EmptyBucket|TimeShift'`: pass.
  With the call removed from `handleAggregateTraces`, the traces cases fail; without the shifted
  grid, the duplicate check, the partial flag, or the single whole-second shift rule, the matching
  cases fail.
- `GOTOOLCHAIN=go1.26.11 make ci`: see the PR.
- Against a local SigNoz v0.150.0-rc.1 instance, through the built server over stdio, with explicit
  windows on 2026-11-12 (before is `origin/main`, after is this branch):
  - `count()` of `payment` error spans, 20:00 to 23:00, 600 s step: 6 points, then 18, with 0
    before 21:20 and after 22:10.
  - `p99(duration_nano)` of the same spans: 6 points before and after.
  - `["count()", "p99(duration_nano)"]`: the count has 18 points, the p99 keeps 6.
  - The same pair for `checkout`, 20:03:20 to 22:06:40: the count runs from the partial 20:00
    bucket (0) to the partial 22:00 bucket (34); the p99 is unchanged.
  - Grouped counts: the table above.
  - `timeShift` of one day: filled on the current window's buckets. `timeShift` of one day and
    17 minutes on 2026-11-13, 20:00 to 23:00, 600 s step: 19 points from 19:57 to 22:57, the two
    edge zeros marked partial. `cumulativeSum` and
    `having: count() > 1`: unchanged. `["countIf(has_error = true)", "countIf(has_error = true) / count()"]`:
    the first filled, the ratio unchanged. Scalar: unchanged.
  - No WARN about an unexpected response in the server log.
- e2e: `test_aggregate_logs_count_series_is_zero_in_empty_buckets` runs in the PR's e2e job.

## Outcome

Count-like time series from both aggregate tools now carry a 0 for each empty bucket of the
window, with a note, and other aggregations keep their gaps. Grouped results that would gain more
than 1,500 zero points are left unfilled with a note that says how to get a smaller series.
