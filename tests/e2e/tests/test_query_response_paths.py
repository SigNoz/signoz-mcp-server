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
