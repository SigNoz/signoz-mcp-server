"""Upstream response-path drift checks — port of e2e_familya_test.go (N1–N5).

Where the Go tests read whatever staging had (skipping when empty), these seed
their own telemetry on the fresh cast so the paths are always exercised. The
search row paths and the alert-history envelope are pinned where those tools
are tested (test_logs.py, test_trace_fields.py, test_alerts.py).
"""

import json
import time

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import count_data_array_rows, first_text_block, note_blocks
from fixtures.telemetry import seed_metrics, wait_for


def test_list_metrics_path(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """data.metrics[] path and the hasMore completeness note hold."""
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_metrics", {"searchContext": f"find metric {metric}", "limit": "1000"}
        )
        return not result.get("isError", False) and metric in first_text_block(result)

    wait_for(visible, f"seeded metric {metric} visible via signoz_list_metrics")

    result = assert_tool_ok(
        mcp_client.call_tool("signoz_list_metrics", {"searchContext": "list metrics", "limit": "5"})
    )
    _, ok = count_data_array_rows(first_text_block(result), "metrics")
    assert ok, "N4 DRIFT: list_metrics data.metrics[] path not found"
    assert "hasMore" in note_blocks(result), f"expected a completeness note with hasMore: {note_blocks(result)!r}"


def test_top_metrics_path(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """data.samples[] path and the top-metrics completeness note hold."""
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool("signoz_get_top_metrics", {"searchContext": "top metrics", "timeRange": "24h"})
        return not result.get("isError", False) and metric in first_text_block(result)

    wait_for(visible, f"seeded metric {metric} visible via signoz_get_top_metrics")

    result = assert_tool_ok(
        mcp_client.call_tool("signoz_get_top_metrics", {"searchContext": "top metrics", "timeRange": "24h"})
    )
    _, ok = count_data_array_rows(first_text_block(result), "samples")
    assert ok, "N4 DRIFT: get_top_metrics data.samples[] path not found"
    assert "metrics" in note_blocks(result), f"expected a top-metrics completeness note: {note_blocks(result)!r}"


def test_execute_builder_query(mcp_client: MCPClient) -> None:
    """execute_builder_query succeeds and returns a JSON block 0."""
    now = int(time.time() * 1000)
    query = {
        "schemaVersion": "v1",
        "start": now - 3_600_000,
        "end": now,
        "requestType": "scalar",
        "compositeQuery": {
            "queries": [
                {
                    "type": "builder_query",
                    "spec": {"name": "A", "signal": "logs", "aggregations": [{"expression": "count()"}]},
                }
            ]
        },
    }
    result = assert_tool_ok(
        mcp_client.call_tool("signoz_execute_builder_query", {"searchContext": "count logs", "query": query})
    )
    json.loads(first_text_block(result))


def test_scalar_metric_builder_defaults_reducer(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Defaulting finds the exact gauge beyond substring hits and matches explicit avg."""
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_scalar_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, count=2, age_seconds=120)
    for i in range(11):
        seed_metrics(f"mcp-e2e-{test_id}", f"a{i}_{metric}", count=2, age_seconds=120)
    now = int(time.time() * 1000)
    aggregation = {"metricName": metric, "timeAggregation": "avg", "spaceAggregation": "sum"}
    query = {
        "schemaVersion": "v1",
        "start": now - 3_600_000,
        "end": now,
        "requestType": "scalar",
        "compositeQuery": {
            "queries": [
                {
                    "type": "builder_query",
                    "spec": {"name": "A", "signal": "metrics", "aggregations": [aggregation]},
                }
            ]
        },
    }

    def first_page_excludes_exact_metric() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_metrics",
            {
                "searchContext": "verify substring collisions for scalar metric lookup",
                "searchText": metric,
                "limit": 10,
                "start": query["start"],
                "end": query["end"],
            },
        )
        if result.get("isError", False):
            return False
        names = [row["metricName"] for row in json.loads(first_text_block(result))["data"]["metrics"]]
        return len(names) == 10 and metric not in names

    wait_for(first_page_excludes_exact_metric, "exact metric outside the first substring-search page")

    def visible() -> dict | None:
        result = mcp_client.call_tool(
            "signoz_execute_builder_query", {"searchContext": "average seeded gauge", "query": query}
        )
        if result.get("isError", False):
            return None
        results = json.loads(first_text_block(result))["data"]["data"]["results"]
        values = [value for item in results for row in item.get("data", []) for value in row]
        return result if any(isinstance(value, (int, float)) and value > 0 for value in values) else None

    defaulted = wait_for(visible, f"scalar metric {metric} available with default reducer")
    assert "reduceTo=avg" in note_blocks(defaulted)
    aggregation["reduceTo"] = "avg"
    explicit = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_execute_builder_query", {"searchContext": "explicit average seeded gauge", "query": query}
        )
    )
    default_data = json.loads(first_text_block(defaulted))["data"]["data"]["results"][0]["data"]
    explicit_data = json.loads(first_text_block(explicit))["data"]["data"]["results"][0]["data"]
    assert default_data == explicit_data
    assert "reduceTo=" not in note_blocks(explicit)
