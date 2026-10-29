"""query_metrics forwards aggregation combinations for SigNoz to validate.

Pins the combinations signoz://metrics-aggregation-guide teaches beyond the
type defaults: a running total from a counter, and histogram buckets read
with rate and sum.
"""

import json

import pytest

from fixtures.mcpclient import MCPClient
from fixtures.results import first_text_block, note_blocks, result_code
from fixtures.telemetry import seed_metrics, wait_for


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
            {"searchContext": f"query {gauge}", "metricName": gauge, "timeRange": "1h", "requestType": "scalar"},
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
