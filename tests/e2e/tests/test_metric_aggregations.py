"""query_metrics forwards aggregation combinations for SigNoz to validate.

Pins the combinations signoz://metrics-aggregation-guide teaches beyond the
type defaults: a running total from a counter, and histogram buckets read
with rate and sum. Also pins what scalar reducers measure, which the result
notes state: per-step percentiles and summed rates are not window values.
"""

import json
import time

import pytest

from fixtures.mcpclient import MCPClient
from fixtures.results import first_text_block, note_blocks, result_code
from fixtures.telemetry import seed_histogram_minutes, seed_metrics, seed_steady_counter, wait_for


def _scalar_values(body: str) -> list[float]:
    """The aggregation value (last column) of every scalar result row."""
    payload = json.loads(body)
    return [row[-1] for result in payload["data"]["data"]["results"] for row in result.get("data") or []]


# metricType is passed so the call skips the metadata lookup, which cannot find
# the metric until ingestion lands; the aggregation passthrough is under test.
def _scalar(mcp_client: MCPClient, metric: str, extra: dict) -> dict:
    return mcp_client.call_tool(
        "signoz_query_metrics",
        {"searchContext": f"query {metric}", "metricName": metric, "timeRange": "1h", "requestType": "scalar", **extra},
    )


def test_counter_max_reads_the_running_total(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    metric = f"mcp_e2e_counter_{test_id.replace('-', '_')}"
    # Aged samples sit in completed buckets, so the newest point is never trimmed.
    seed_metrics(f"mcp-e2e-{test_id}", metric, value=40.0, count=3, kind="counter", age_seconds=120)

    def total() -> list[float]:
        result = _scalar(
            mcp_client,
            metric,
            {
                "metricType": "sum",
                "isMonotonic": True,
                "timeAggregation": "max",
                "spaceAggregation": "sum",
                "reduceTo": "max",
            },
        )
        assert not result.get("isError", False), (
            f"counter with timeAggregation max was rejected: {first_text_block(result)[:400]}"
        )
        values = _scalar_values(first_text_block(result))
        return values if values == [42] else []

    # max on the counter reads its running total (42), not a per-second rate.
    wait_for(total, f"counter {metric} running total of 42")


def _bucket_rates(body: str) -> dict[str, float]:
    """The scalar value of every row, keyed by its le label."""
    rates = {}
    for result in json.loads(body)["data"]["data"]["results"]:
        names = [column["name"] for column in result.get("columns") or []]
        for row in result.get("data") or []:
            rates[row[names.index("le")]] = row[-1]
    return rates


def test_histogram_buckets_read_with_rate_and_sum(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    name = f"mcp_e2e_hist_{test_id.replace('-', '_')}"
    # One observation per bucket, so the cumulative le buckets hold 1, 2, and 3.
    seed_metrics(f"mcp-e2e-{test_id}", name, count=2, kind="histogram")
    # SigNoz stores the buckets as <name>.bucket, as the guide teaches.
    metric = f"{name}.bucket"

    def buckets() -> dict[str, float]:
        result = _scalar(
            mcp_client,
            metric,
            {
                "metricType": "histogram",
                "timeAggregation": "rate",
                "spaceAggregation": "sum",
                "reduceTo": "sum",
                "groupBy": ["le"],
            },
        )
        assert not result.get("isError", False), (
            f"histogram with rate and sum was rejected: {first_text_block(result)[:400]}"
        )
        rates = _bucket_rates(first_text_block(result))
        return rates if len(rates) == 3 and all(rate > 0 for rate in rates.values()) else {}

    rates = wait_for(buckets, f"histogram {metric} rates for all three buckets")
    # Ratios hold for any window and step; +Inf is the total observation rate.
    lowest = rates["10"]
    assert rates["100"] == pytest.approx(2 * lowest), f"bucket rates: {rates}"
    assert rates["+Inf"] == pytest.approx(3 * lowest), f"bucket rates: {rates}"


def test_metadata_lookup_matches_the_exact_name(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """A longer neighbor never lends its metadata, and a missing name gets the search instruction."""
    prefix = f"mcp_e2e_exact_{test_id.replace('-', '_')}"
    gauge = f"{prefix}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", gauge, value=7.0, count=2)

    def resolved() -> dict:
        result = mcp_client.call_tool(
            "signoz_query_metrics",
            {
                "searchContext": f"query {gauge}",
                "metricName": gauge,
                "timeRange": "1h",
                "requestType": "scalar",
                "reduceTo": "last",
            },
        )
        return {} if result.get("isError", False) else result

    found = wait_for(resolved, f"metadata for {gauge} visible")
    assert "metricType: gauge (auto-fetched" in note_blocks(found), (
        f"exact name did not resolve its own metadata: {note_blocks(found)}"
    )

    missing = mcp_client.call_tool(
        "signoz_query_metrics",
        {"searchContext": f"query {prefix}", "metricName": prefix, "timeRange": "1h"},
    )
    assert missing.get("isError", False), f"the prefix of {gauge} resolved as a metric: {note_blocks(missing)}"
    assert result_code(missing) == "VALIDATION_FAILED", f"unexpected code: {missing}"
    assert "Search for the right name with signoz_list_metrics" in first_text_block(missing), first_text_block(missing)


def test_scalar_requires_an_explicit_reduce_to(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Pins the reduceTo contract the tool teaches: no default, and SigNoz rejects a scalar query without it."""
    result = mcp_client.call_tool(
        "signoz_query_metrics",
        {
            "searchContext": "scalar query without a reducer",
            "metricName": f"mcp_e2e_reduce_{test_id.replace('-', '_')}",
            "metricType": "gauge",
            "requestType": "scalar",
            "timeRange": "1h",
        },
    )
    assert result.get("isError", False), f"a scalar query without reduceTo ran: {first_text_block(result)[:400]}"
    assert "reduceTo is required" in first_text_block(result), first_text_block(result)[:400]


# Nine fast minutes and one slow one, in buckets (0,10], (10,50], (50,100], (100,500], +Inf.
# Each minute's p95 is 16.667 (fast) or 96.875 (slow); the p95 of all 1,000
# observations is 68.75, which the mean of the step values (24.687) misses.
HISTOGRAM_BOUNDS = [10.0, 50.0, 100.0, 500.0]
HISTOGRAM_MINUTES = [[94, 6, 0, 0, 0]] * 9 + [[0, 20, 80, 0, 0]]
WINDOW_P95 = 68.75


def _execute(mcp_client: MCPClient, query: str) -> list[float]:
    now = int(time.time() * 1000)
    request = {
        "schemaVersion": "v1",
        "start": now - 3_600_000,
        "end": now,
        "requestType": "scalar",
        "compositeQuery": {"queries": [{"type": "promql", "spec": {"name": "A", "query": query}}]},
    }
    result = mcp_client.call_tool("signoz_execute_builder_query", {"searchContext": "window p95", "query": request})
    assert not result.get("isError", False), f"PromQL was rejected: {first_text_block(result)[:400]}"
    return _scalar_values(first_text_block(result))


def test_window_percentile_is_not_the_mean_of_step_percentiles(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """Pins the percentile note and its bucket-count route, and the guide's PromQL for a cumulative histogram."""
    service = f"mcp-e2e-{test_id}"
    slug = test_id.replace("-", "_")
    cumulative, delta = f"mcp_e2e_latency_c_{slug}", f"mcp_e2e_latency_d_{slug}"
    first_minute_ns = seed_histogram_minutes(service, cumulative, HISTOGRAM_MINUTES, HISTOGRAM_BOUNDS, cumulative=True)
    seed_histogram_minutes(service, delta, HISTOGRAM_MINUTES, HISTOGRAM_BOUNDS, cumulative=False)

    def p95_with(metric: str, extra: dict) -> dict:
        return mcp_client.call_tool(
            "signoz_query_metrics",
            {
                "searchContext": f"p95 of {metric}",
                "metricName": f"{metric}.bucket",
                "filter": f"service.name = '{service}'",
                "spaceAggregation": "p95",
                "requestType": "scalar",
                **extra,
            },
        )

    def p95(metric: str, reduce_to: str) -> dict:
        return p95_with(metric, {"reduceTo": reduce_to, "timeRange": "1h"})

    def all_minutes(metric: str) -> dict:
        result = p95(metric, "avg")
        return (
            result if not result.get("isError", False) and _scalar_values(first_text_block(result)) == [24.687] else {}
        )

    for metric in (cumulative, delta):
        averaged = wait_for(lambda m=metric: all_minutes(m), f"all ten minutes of {metric}")
        notes = note_blocks(averaged)
        assert "reduceTo avg combined the p95 of each 60s step" in notes, notes
        assert "groupBy le, which returns each bucket's count over the same steps" in notes, notes
        low = _scalar_values(first_text_block(p95(metric, "min")))
        high = _scalar_values(first_text_block(p95(metric, "max")))
        assert low == [16.667] and high == [96.875], f"{metric}: min {low}, max {high}"
        assert low[0] <= WINDOW_P95 <= high[0], "the window p95 lies between the step minimum and maximum"

    # The metrics guide's PromQL returns the window's p95 for a cumulative histogram, and nothing for
    # a delta one.
    guide_query = 'histogram_quantile(0.95, sum by (le) (increase({{"{}.bucket", "service.name"="{}"}}[3600s])))'
    assert _execute(mcp_client, guide_query.format(cumulative, service)) == [WINDOW_P95]
    assert _execute(mcp_client, guide_query.format(delta, service)) == []

    # Each bucket's count over the window, which the note's same-tool route reads, gives the
    # window's p95 by interpolation for either temporality: 950 falls in (50, 100] at 68.75.
    for metric in (cumulative, delta):
        buckets = p95_with(
            metric,
            {
                "spaceAggregation": "sum",
                "timeAggregation": "increase",
                "reduceTo": "sum",
                "groupBy": ["le"],
                "timeRange": "1h",
            },
        )
        counts = {}
        for result in json.loads(first_text_block(buckets))["data"]["data"]["results"]:
            names = [column["name"] for column in result.get("columns") or []]
            for row in result.get("data") or []:
                counts[row[names.index("le")]] = row[-1]
        assert counts == {"10": 846, "50": 920, "100": 1000, "500": 1000, "+Inf": 1000}, f"{metric} bucket counts"

    # A grouped percentile keeps its groups: le joins the caller's groupBy keys.
    grouped = p95_with(
        cumulative,
        {
            "spaceAggregation": "sum",
            "timeAggregation": "increase",
            "reduceTo": "sum",
            "groupBy": ["service.name", "le"],
            "timeRange": "1h",
        },
    )
    per_group = {}
    for result in json.loads(first_text_block(grouped))["data"]["data"]["results"]:
        names = [column["name"] for column in result.get("columns") or []]
        for row in result.get("data") or []:
            per_group[(row[names.index("service.name")], row[names.index("le")])] = row[-1]
    assert per_group[(service, "50")] == 920 and per_group[(service, "+Inf")] == 1000, f"grouped counts: {per_group}"

    # Steps start at multiples of the step since the Unix epoch, so a window-long step that starts
    # mid-step does not cover the window: the cumulative histogram's first partial step has no
    # earlier sample and is left out, and the delta histogram's first step reaches back before it.
    # The window straddles a 300s boundary with samples on both sides.
    first_s = first_minute_ns // 1_000_000_000
    boundary_s = -(-(first_s + 180) // 300) * 300
    window = {"start": str((boundary_s - 150) * 1000), "end": str((boundary_s + 150) * 1000), "stepInterval": "300"}

    def step_timestamps(metric: str) -> list[int]:
        result = mcp_client.call_tool(
            "signoz_query_metrics",
            {
                "searchContext": "p95 over five minutes",
                "metricName": f"{metric}.bucket",
                "spaceAggregation": "p95",
                "requestType": "time_series",
                **window,
            },
        )
        return [
            point["timestamp"]
            for result in json.loads(first_text_block(result))["data"]["data"]["results"]
            for aggregation in result.get("aggregations") or []
            for series in aggregation.get("series") or []
            for point in series.get("values") or []
        ]

    boundary_ms = boundary_s * 1000
    assert step_timestamps(cumulative) == [boundary_ms], f"cumulative steps over a window around {boundary_ms}"
    assert step_timestamps(delta) == [boundary_ms - 300_000, boundary_ms], f"delta steps around {boundary_ms}"
    peak = note_blocks(p95_with(cumulative, {"reduceTo": "max", **window}))
    assert "so this 300s step does not cover the window as one step: for this cumulative histogram" in peak, peak


def test_counter_reducers_say_what_they_measured(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Pins the rate and running-total notes: their fixes return the count and the rate in the window."""
    service = f"mcp-e2e-{test_id}"
    metric = f"mcp_e2e_requests_{test_id.replace('-', '_')}"
    # 0.5 per second for 70 minutes, so the 1h window starts mid-series.
    seed_steady_counter(service, metric, per_second=0.5, minutes=70)

    def scalar(time_aggregation: str | None, reduce_to: str) -> dict:
        args = {"searchContext": f"requests of {metric}", "metricName": metric, "reduceTo": reduce_to}
        args.update({"requestType": "scalar", "timeRange": "1h"})
        if time_aggregation:
            args["timeAggregation"] = time_aggregation
        return mcp_client.call_tool("signoz_query_metrics", args)

    def ingested() -> dict:
        result = scalar(None, "avg")
        values = [] if result.get("isError", False) else _scalar_values(first_text_block(result))
        return result if values and values[0] == pytest.approx(0.5, rel=0.05) else {}

    wait_for(ingested, f"counter {metric} at 0.5 per second")

    summed = scalar(None, "sum")
    assert "reduceTo sum added up the per-second rate of each 60s step" in note_blocks(summed), note_blocks(summed)
    assert _scalar_values(first_text_block(summed))[0] > 20, "a sum of per-step rates grows with the step count"
    total = _scalar_values(first_text_block(scalar("increase", "sum")))
    # The window holds about 3,540 seconds of the series; SigNoz aligns it to the step.
    assert total[0] == pytest.approx(0.5 * 3540, rel=0.05), f"increase with sum: {total}"

    latest = scalar("latest", "last")
    assert f"{metric} is a cumulative counter, so timeAggregation latest reads its running total" in note_blocks(latest)
    assert _scalar_values(first_text_block(latest))[0] == pytest.approx(0.5 * 70 * 60, rel=0.01), "the running total"
    samples = scalar("count", "sum")
    assert "counts the counter's samples" in note_blocks(samples), note_blocks(samples)

    # A window-long step that starts mid-step reaches back before the window for a counter rate.
    boundary_s = int(time.time() - 1800) // 600 * 600
    window = {"start": str((boundary_s - 300) * 1000), "end": str((boundary_s + 300) * 1000), "stepInterval": "600"}
    series = mcp_client.call_tool(
        "signoz_query_metrics",
        {"searchContext": f"rate of {metric}", "metricName": metric, "requestType": "time_series", **window},
    )
    timestamps = [
        point["timestamp"]
        for result in json.loads(first_text_block(series))["data"]["data"]["results"]
        for aggregation in result.get("aggregations") or []
        for one in aggregation.get("series") or []
        for point in one.get("values") or []
    ]
    assert timestamps == [(boundary_s - 600) * 1000, boundary_s * 1000], f"counter steps around {boundary_s}"
    note = note_blocks(
        mcp_client.call_tool(
            "signoz_query_metrics",
            {
                "searchContext": f"requests of {metric}",
                "metricName": metric,
                "timeAggregation": "increase",
                "reduceTo": "sum",
                "requestType": "scalar",
                **window,
            },
        )
    )
    assert "the first step starts at" in note and "before the window, and counts data from before it" in note, note
