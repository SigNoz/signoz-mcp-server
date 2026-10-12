"""Metric usage and cardinality against real SigNoz, the only coverage these tools have.

check_metric_usage parses two upstream endpoints per metric name and treats a
metric-not-found as empty usage; check_metric_cardinality passes the upstream
envelope through. Both parse real responses here so upstream drift fails a
test, not a user.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json
from fixtures.telemetry import seed_metrics, wait_for


def test_metric_usage_unreferenced_metric_reports_empty_usage(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """Every requested name comes back as a key with empty dashboards/alerts and no per-metric error."""
    suffix = test_id.replace("-", "_")
    seeded = f"mcp_e2e_usage_{suffix}"
    missing = f"{seeded}_never_ingested"
    seed_metrics(f"mcp-e2e-{test_id}", seeded)

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_check_metric_usage",
            {
                "searchContext": "which dashboards or alerts use these metrics",
                "metricNames": [seeded, missing],
            },
        )
    )
    usage = first_block_json(result)
    for name in (seeded, missing):
        assert name in usage, f"usage missing requested metric {name}: {usage}"
        entry = usage[name]
        assert not entry.get("error"), f"usage for {name} carries a per-metric error: {entry}"
        assert not entry.get("dashboards") and not entry.get("alerts"), f"unreferenced metric reports usage: {entry}"


def test_metric_cardinality_resolves_seeded_metric(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """The cardinality passthrough resolves a seeded metric once its metadata lands."""
    metric = f"mcp_e2e_card_{test_id.replace('-', '_')}"
    seed_metrics(f"mcp-e2e-{test_id}", metric, count=3)

    def cardinality_ready():
        result = mcp_client.call_tool(
            "signoz_check_metric_cardinality",
            {"searchContext": f"cardinality of {metric}", "metricName": metric, "timeRange": "1h"},
        )
        if result.get("isError", False):
            return None
        payload = first_block_json(result)
        data = payload.get("data")
        return payload if isinstance(data, (dict, list)) and data else None

    payload = wait_for(cardinality_ready, f"cardinality for seeded metric {metric}")
    assert payload.get("status") == "success", f"cardinality envelope drifted: {payload}"
