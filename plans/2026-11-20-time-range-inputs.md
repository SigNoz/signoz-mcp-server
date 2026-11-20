# Plan: Relative and RFC 3339 Time Inputs

Status: Done
Issue: https://github.com/SigNoz/nerve-pod/issues/235
PR: https://github.com/SigNoz/signoz-mcp-server/pull/467

## Context

Every time-windowed tool took `start` and `end` only as Unix epoch integers, with `timeRange`
as a Go duration plus whole days. Agents write dates as RFC 3339 or relative expressions and
get a validation error, then do epoch arithmetic themselves, which is where wrong-year windows
come from (SigNoz/nerve-pod#230).

Rejected `start`/`end` values in production `tools/call` spans over the 30 days to 2026-11-19,
grouped by `status_message`:

| Value written as | Rejected |
|---|---:|
| RFC 3339 with `Z` | 2,054 |
| RFC 3339 with an offset | 31 |
| Date-time without a zone | 0 |
| Date and time with a zone name (`2026-10-24 09:30 IST`) | 32 |
| `now`, `now-1h`, `now-7d` | 193 |
| `-24h` | 24 |
| Other | 67 |

Two silent behaviours on `main`: `start` without `end`, sent with a `timeRange`, was dropped;
`end` without `start` was dropped and the window ended at the current time, except on the three
service tools, `signoz_get_alert_history` and `signoz_query_metrics` called without `timeRange`,
where the window ran from six hours ago to `end`. A duration sent as
epochs (`start=0`, `end=2592000000`) passed validation and reached SigNoz as 1970 to 2052
(SigNoz/nerve-pod#217).

## Approach

One resolver, `timeutil.ResolveWindow(args, defaultRange, now)`, used by every tool that takes
a window. Handlers read the clock once per call and pass it in, so tests run on a fixed clock
and a caller-supplied reference time could be added later without changing the resolver.

- `start` and `end` accept an RFC 3339 date-time with `Z` or an offset, `now`, `now-<duration>`,
  or a Unix epoch of any magnitude (existing behaviour). Their descriptions list RFC 3339 and
  Unix ms only, so a relative window has one documented form, `timeRange`.
- `timeRange` accepts one or more `<number><unit>` terms with `s`, `m`, `h`, `d` or `w`, and
  fractions (`1h30m`, `2d12h`, `1.5d`, `2w`). It must be greater than zero. `now-24h` is read as
  `24h`.
- `timeRange` and `start`/`end` are separate windows. `start` set: the window runs from `start`
  to `end` or now, and `timeRange` is ignored. No `start`: the window is `timeRange` (or the
  tool default) long and ends now. `end` without `start` is rejected with the `start` to add;
  an `end` of now counts as no `end`.
- Rejected before any query, with the fix in the message: `end` without `start` (the message
  gives both bounds to send), an unparseable bound, a date-time without a zone, `-24h` (points
  to `now-24h`), two durations in `timeRange` (points to `start`/`end`), a non-string
  `timeRange`, a numeric epoch that looks like a duration (one read as seconds that is more than
  a year old, or any before 2001-09-09), a bound more than 24 hours after the current time, a
  window starting before 1970, and an empty or reversed window.
- Error messages build their RFC 3339 example from the current time, and the `start` description
  shows the format as `YYYY-MM-DDThh:mm:ssZ`, so a copied example never carries a stale year.
- Every result reports the window queried, in RFC 3339 UTC and Unix ms: a note block on text
  results (`window queried: 2026-11-19T09:00:00Z to 2026-11-19T10:00:00Z (Unix ms 1795078800000
  to 1795082400000)`), and a `window` field on the structured results of `signoz_list_services`
  and `signoz_get_service_map`.

Coverage:

| Tool | Default window | Sent to SigNoz as |
|---|---|---|
| `signoz_search_logs`, `signoz_aggregate_logs`, `signoz_search_traces`, `signoz_aggregate_traces`, `signoz_query_metrics`, `signoz_list_metrics` | 1h | Unix ms |
| `signoz_get_top_metrics`, `signoz_check_metric_cardinality` | 7d | Unix ms |
| `signoz_get_alert_history` | 6h | Unix ms |
| `signoz_list_services`, `signoz_get_service_top_operations`, `signoz_get_service_map` | 6h | Unix ns |

Not covered, by decision:

- `signoz_execute_builder_query`: `query.start` and `query.end` are fields of the `query_range`
  body this server forwards as written, and stay Unix ms. Server instruction rule 5 says so.
- `signoz_get_trace_details` takes no window.
- A reference time and timezone for relative ranges, which SigNoz/nerve-pod#235 asks for on
  behalf of Noz, is not added; see Key Decisions.

## Files to Modify

- `pkg/timeutil/time.go` — `ResolveWindow`, `ParseDuration`, `Window`; removes
  `GetTimestampsWithDefaults`, `ValidateExplicitTimestamps`, `HasUsableTimestamp`,
  `ParseTimeRange`.
- `pkg/timeutil/time_test.go` — fixed-clock tests for forms, calendar edges and rejections.
- `internal/handler/tools/params.go` — `resolveTimestamps` calls the resolver and prefixes errors.
- `internal/handler/tools/services.go`, `alerts.go`, `metrics_query.go` — use the resolver.
- `internal/handler/tools/params.go` and the twelve handlers (`logs.go`, `traces.go`,
  `metrics.go`, `metrics_top.go`, `metrics_query.go`, `metric_cardinality.go`, `alerts.go`,
  `services.go`) — report the window queried.
- `internal/handler/tools/query_param_descriptions.go` and the tools that had their own
  `start`/`end` text — one shared description each for `start`, `end` and `timeRange`.
- `pkg/instructions/instructions.go` — rule 5 names RFC 3339 for tool bounds within the
  2,048-byte budget.
- `internal/handler/tools/tool_error_codes_test.go`, `time_window_test.go` — rejection before
  SigNoz and the window sent upstream, per tool. `params_test.go` drops the helper-level window
  tests that `pkg/timeutil` covers.
- `internal/mcp-server/testdata/wire-catalog/` — the 36 changed parameter descriptions and the
  instructions.
- `README.md` — parameter references for the twelve tools, the window report, and the
  `signoz_list_services` and `signoz_get_service_map` responses.

## Key Decisions

### 2026-11-20 — `timeRange` and `start`/`end` are separate windows

- Decision: `timeRange` is a window ending now; `start` and `end` give a fixed window. `start`
  alone runs to now and ignores `timeRange`. `end` without `start` is rejected, naming the
  `start` that gives the `timeRange`-long window ending at `end`; an `end` of now counts as no
  `end`. The bound descriptions stop listing `now-<duration>`, which still parses.
- Rationale: the first revision of this PR let `end` alone set where `timeRange` ends. Its
  description needed a sentence on `end` ("Without start, the window is timeRange long and ends
  here") on top of the rule on `timeRange`, and "ends here" read as ambiguous. With separate
  windows one rule covers every call: `start` overrides `timeRange`, and `timeRange` ends now.
  An `end` sent alone costs one retry with the fix in the message instead of running a window
  the caller did not state, and the silent drop on `main` stays removed. `timeRange` declares a
  schema default, so a client that fills defaults sends it on every call; `start` overrides it
  rather than conflicting with it.
- Alternatives rejected: an `end`-anchored `timeRange` (the first revision); an error for
  `start` plus `timeRange`; `[start, start + timeRange]`.

### 2026-11-20 — Date-times need `Z` or an offset

- Decision: a date-time without a zone is rejected with "add Z for UTC or an offset".
- Rationale: none of the 2,085 rejected RFC 3339 values lacked a zone, and with a zone no
  timezone input is needed.

### 2026-11-20 — Guard against durations sent as epochs

- Decision: reject a numeric epoch read as seconds (10 digits or fewer) that is more than a
  year old, any numeric epoch before 2001-09-09, and any bound more than 24 hours after the
  current time. RFC 3339 dates, and millisecond epochs from 2001 on, have no age limit.
- Rationale: `start=0, end=2592000000` and `start=3600` read as 1970-based windows; the closing
  comment on SigNoz/nerve-pod#217 asked for `start <= 0` and far-future `end` to be rejected
  here. A floor alone misses durations of about 12 to 20 days in milliseconds: 14 days
  (`1209600000`) reads as seconds in 2008. Real epochs sent in seconds are almost always recent,
  and older times stay reachable in milliseconds or RFC 3339. Durations of about 20 days in
  milliseconds still read as a date within the last year and pass. 24 hours is far beyond clock
  skew, so skew never rejects a window.
- Alternatives rejected: a cap on window length, also asked for on SigNoz/nerve-pod#217. It would
  reject long windows people do ask for, needs a value SigNoz defines, and the checks above
  already reject the known duration-as-epoch shapes.

### 2026-11-20 — No reference-time or timezone parameter yet

- Decision: relative ranges resolve on this server's clock, read once per call.
- Rationale: the resolver already takes `now` as an argument, so a caller-supplied reference is a
  schema addition. Whether Noz adds one or sends absolute bounds stays open on
  SigNoz/nerve-pod#235.

### 2026-11-20 — Inputs that never produced a window are now errors

- Decision: zero and negative `timeRange` values, which `time.ParseDuration` accepted, are
  rejected. Input `time.ParseDuration` takes goes through it, as on `main`, so every positive
  form keeps its length; only forms with `d` or `w` are summed by this parser.
- Rationale: both produced an empty or reversed window that SigNoz rejected. This narrows an
  accepted format (CMP-1) with no working input lost; the PR records it.

### 2026-11-20 — An ignored `timeRange` draws no notice

- Decision: `timeRange` sent with `start` is ignored as its description says, without a result
  notice.
- Rationale: the ignored-arguments notice (`plans/2026-11-16-notice-ignored-tool-arguments.md`)
  covers only keys a schema does not declare, and `timeRange` is declared. Clients that fill
  schema defaults would also draw the notice on every call with `start`. The window report shows
  which window ran.

### 2026-11-20 — Report the window queried on every result

- Decision: text results append `window queried: <start> to <end> (Unix ms <start> to <end>)`;
  `signoz_list_services` and `signoz_get_service_map` add `window` (`start`, `end`, `startMs`,
  `endMs`) to their payloads.
- Rationale: SigNoz/nerve-pod#235 asks for the resolved bounds and units to be inspectable, and a
  caller can reuse the Unix ms values in `signoz_execute_builder_query`. Passthrough payloads stay
  verbatim (OUT-5), so the report is a separate block there. Claude Code shows the model only
  `structuredContent` when a result has it (SigNoz/nerve-pod#654), so on structured results the
  report is a payload field. Relative bounds resolve on this server's clock in UTC, so no separate
  reference time or zone is reported. The service routes take nanoseconds; the report gives
  milliseconds, the unit callers send.
- Alternatives rejected: a note on the structured results too (some clients would see it twice);
  echoing inside passthrough JSON.

### 2026-11-20 — Not marked as breaking

- Decision: `feat(time)`, not `feat(time)!`; the PR lists each changed behaviour (CMP-1: not
  silent).
- Rationale: the newly rejected inputs produced empty, reversed or unintended windows, and the
  partial-bound changes replace inputs that were dropped without an error. One exception: a real
  Unix seconds epoch more than a year old resolved correctly before and is now rejected, with an
  error naming milliseconds or RFC 3339.

### 2026-11-20 — No SigNoz/agent-skills change needed

- Decision: no companion change.
- Rationale: the skills pass `timeRange` and Unix-millisecond `start`/`end`, which resolve as
  before; `signoz-generating-queries` says valid Unix-ms bounds override `timeRange`, which still
  holds. Teaching RFC 3339 there is optional.

## Reference Links

- [SigNoz/nerve-pod#235](https://github.com/SigNoz/nerve-pod/issues/235)
- [SigNoz/nerve-pod#217](https://github.com/SigNoz/nerve-pod/issues/217)
- [SigNoz/nerve-pod#654](https://github.com/SigNoz/nerve-pod/issues/654)

## Verification

- `pkg/timeutil/time_test.go` on a fixed clock: 21 equivalent ways of writing one window resolve
  to the same bounds in ms and ns; relative ranges across a year boundary; local midnight and a
  daylight-saving change through explicit offsets; old RFC 3339 dates, old millisecond epochs
  and an `end` five minutes ahead of the clock accepted; 33 rejections, each checked for the fix
  its message names, including `end` without `start` (absolute and relative) and the `start`
  each message gives. An `end` of `now` with `timeRange` resolves like `timeRange` alone.
- `TestToolHandlers_RejectInvalidArgumentsBeforeCallingSigNoz` has one row per time-windowed
  tool; each is rejected with the validation prefix against an upstream that fails the test on
  any request. `signoz_query_metrics` resolves its window before the metric metadata lookup, so
  its row omits `metricType`.
- `TestTimeWindowedTools_SendTheResolvedWindowUpstream` sends RFC 3339 bounds with offsets to all
  twelve tools and reads the `start` and `end` SigNoz receives, from query parameters or the top
  of the JSON body: Unix ms, or Unix ns for the three service tools, so a wrong unit or swapped
  bounds fail it. Each result must succeed and report the window: the note block on ten tools,
  the `window` field, compared as JSON, on `signoz_list_services` and `signoz_get_service_map`.
  Called without bounds, each tool must query the `timeRange` default its schema advertises,
  ending at the call time; changing the alert-history default from 6h to 1h fails its row.
- e2e tests: none sends `start` without `end`, `end` without `start`, or an old or
  seconds-sized epoch, and none asserts the removed error strings.
- `GOTOOLCHAIN=go1.26.11 make ci` passed on 2026-11-20 for both revisions, including lint, race
  tests, guardrails, the wire-catalog goldens, protocol and conformance checks.

## Outcome

Implemented in SigNoz/signoz-mcp-server#467. All twelve time-windowed tools resolve their window
through `ResolveWindow` and report the window queried. A second revision made `timeRange` and
`start`/`end` separate windows and rejects `end` without `start`. `make ci` passed, and codex-loop approved
after three rounds; its one fix moved `signoz_query_metrics` window validation ahead of the
metric metadata lookup.

Deferred: a reference time and timezone, and the matching change to Noz's `<time_handling>`
prompt, stay on SigNoz/nerve-pod#235. A window-length cap was declined (SigNoz/nerve-pod#217).
Declined because agents do not send them: windows shorter than one millisecond, which collapse
to equal bounds on millisecond routes and show equal reported bounds on the service routes;
nanosecond rounding in `d` and `w` sums; and spellings such as `+1d`.
