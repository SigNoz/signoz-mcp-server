"""query_metrics forwards aggregation combinations for SigNoz to validate.

Pins the combinations signoz://metrics-aggregation-guide teaches beyond the
type defaults: a running total from a counter, and histogram buckets read
with rate and sum.
"""

import json

from fixtures.mcpclient import MCPClient
from fixtures.results import aggregate_columns_and_row_count, first_text_block
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


def test_histogram_buckets_read_with_rate_and_sum(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    name = f"mcp_e2e_hist_{test_id.replace('-', '_')}"
    seed_metrics(f"mcp-e2e-{test_id}", name, count=2, kind="histogram")
    # SigNoz stores the buckets as <name>.bucket, as the guide teaches.
    metric = f"{name}.bucket"

    def buckets() -> list[str]:
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
        columns, row_count = aggregate_columns_and_row_count(first_text_block(result))
        return columns if row_count else []

    columns = wait_for(buckets, f"histogram {metric} visible")
    assert "le" in columns, f"rate and sum on histogram buckets should group by le; columns: {columns}"
