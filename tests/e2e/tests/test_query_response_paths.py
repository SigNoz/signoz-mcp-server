"""Upstream response-path drift checks — port of e2e_familya_test.go (N1–N5).

Where the Go tests read whatever staging had (skipping when empty), these seed
their own telemetry and resources on the fresh cast so the paths are always
exercised.
"""

import json
import time

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import (
    count_alert_history_rows,
    count_data_array_rows,
    count_query_range_rows,
    first_text_block,
    note_blocks,
)
from fixtures.seeded import create_alert_rule, create_channel, delete_alert_rule, delete_channel
from fixtures.telemetry import seed_logs, seed_metrics, seed_traces, wait_for


def test_search_logs_row_path(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """data.data.results[].rows[] path and the hasMore completeness note hold."""
    service = f"mcp-e2e-{test_id}"
    marker = f"mcp-e2e-marker-{test_id}"
    seed_logs(service, marker, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": f"find {marker}", "searchText": marker, "timeRange": "1h", "limit": "5"},
        )
        return not result.get("isError", False) and marker in first_text_block(result)

    wait_for(visible, f"seeded log marker {marker} visible via signoz_search_logs")

    result = assert_tool_ok(
        mcp_client.call_tool("signoz_search_logs", {"searchContext": "recent logs", "timeRange": "1h", "limit": "5"})
    )
    n, ok = count_query_range_rows(first_text_block(result))
    assert ok, "N4 DRIFT: count_query_range_rows could not walk live search_logs body"
    assert n > 0, "expected rows after seeding logs"
    assert "hasMore" in note_blocks(result), f"expected a completeness note with hasMore: {note_blocks(result)!r}"


def test_search_traces_row_path(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """The traces row path holds AND yields a real traceId for includeSpans."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5"},
        )
        return not result.get("isError", False) and service in first_text_block(result)

    wait_for(visible, f"seeded traces for {service} visible via signoz_search_traces")

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_traces", {"searchContext": "recent traces", "timeRange": "1h", "limit": "5"}
        )
    )
    _, ok = count_query_range_rows(first_text_block(result))
    assert ok, "N4 DRIFT: count_query_range_rows could not walk live search_traces body"
    assert "hasMore" in note_blocks(result), f"expected a completeness note with hasMore: {note_blocks(result)!r}"


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


def test_alert_history_path(mcp_client: MCPClient, test_id: str) -> None:
    """get_alert_history data[] or data.items[] path and the hasMore note hold.

    The Go test read a staging rule; here we seed one (and a channel, which v2
    direct routing requires on every tier).
    """
    channel_name = f"mcp-e2e-ch-{test_id}"
    rule_name = f"mcp-e2e-rule-{test_id}"
    channel_id = create_channel(mcp_client, channel_name)
    rule_id = None
    try:
        rule_id = create_alert_rule(mcp_client, rule_name, channel_name=channel_name)
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_alert_history",
                {"searchContext": f"history for rule {rule_id}", "ruleId": rule_id, "timeRange": "24h", "limit": "5"},
            )
        )
        _, ok = count_alert_history_rows(first_text_block(result))
        assert ok, "N4 DRIFT: get_alert_history data[] or data.items[] path not found"
        assert "hasMore" in note_blocks(result), f"expected a completeness note with hasMore: {note_blocks(result)!r}"
    finally:
        if rule_id is not None:
            delete_alert_rule(mcp_client, rule_id)
        delete_channel(mcp_client, channel_id)


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


def test_list_tools_succeed(mcp_client: MCPClient) -> None:
    """Normal list tools succeed and return valid JSON (N5)."""
    for tool in ("signoz_list_dashboards", "signoz_list_notification_channels"):
        result = assert_tool_ok(mcp_client.call_tool(tool, {"searchContext": f"call {tool}", "limit": "5"}))
        json.loads(first_text_block(result))
