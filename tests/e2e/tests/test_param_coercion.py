"""Tolerant parameter handling — ports of Family A N2–N3 and Family E K1/K3.

Numbers-as-strings, booleans-as-strings, and timestamp magnitude auto-detect
must all keep working; garbage values must be rejected as parameter errors.
"""

import json
import time

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block, note_blocks, query_range_rows
from fixtures.telemetry import seed_metrics, seed_traces, wait_for


def test_step_interval_number_and_string(mcp_client: MCPClient) -> None:
    """stepInterval as JSON number AND string both honored (N2)."""
    for step in (60, "60"):
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_aggregate_logs",
                {
                    "searchContext": "count logs over time",
                    "aggregation": "count",
                    "timeRange": "1h",
                    "requestType": "time_series",
                    "stepInterval": step,
                },
            )
        )
        json.loads(first_text_block(result))


def test_search_traces_error_bool(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """error accepts real bool + legacy string; garbage is rejected (N3)."""
    seed_traces(f"mcp-e2e-{test_id}", count=1)
    for value in (True, False, "true", "false"):
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": "traces by error flag", "timeRange": "1h", "limit": "2", "error": value},
        )
        assert not result.get("isError", False), (
            f"search_traces error={value!r} unexpectedly failed: {first_text_block(result)[:300]}"
        )

    result = mcp_client.call_tool(
        "signoz_search_traces",
        {"searchContext": "traces by error flag", "timeRange": "1h", "error": "maybe"},
    )
    assert result.get("isError", False), "expected an error result for garbage error value"


def _first_visible_trace_id(mcp_client: MCPClient, service: str) -> str:
    def visible() -> str:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5"},
        )
        if result.get("isError", False):
            return ""
        rows = query_range_rows(first_text_block(result))
        return rows[0].get("trace_id", "") if rows else ""

    return wait_for(visible, f"a visible trace id for {service}")


def test_get_trace_details_include_spans(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """includeSpans accepts real bool + legacy string; garbage is rejected (N3)."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=1)
    trace_id = _first_visible_trace_id(mcp_client, service)

    for value in (True, False, "true", "false"):
        result = mcp_client.call_tool(
            "signoz_get_trace_details",
            {"searchContext": f"trace {trace_id}", "traceId": trace_id, "timeRange": "1h", "includeSpans": value},
        )
        assert not result.get("isError", False), (
            f"get_trace_details includeSpans={value!r} failed: {first_text_block(result)[:300]}"
        )

    result = mcp_client.call_tool(
        "signoz_get_trace_details",
        {"searchContext": f"trace {trace_id}", "traceId": trace_id, "includeSpans": "perhaps"},
    )
    assert result.get("isError", False), "expected an error result for garbage includeSpans"


def test_query_metrics_is_monotonic(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """isMonotonic accepts real bool + legacy string; garbage is rejected (N3)."""
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool("signoz_list_metrics", {"searchContext": f"find {metric}", "limit": "1000"})
        return not result.get("isError", False) and metric in first_text_block(result)

    wait_for(visible, f"seeded metric {metric} visible")

    for value in (True, False, "true", "false"):
        # query_metrics may legitimately error on a metric-type/aggregation
        # combo, but it must not error on isMonotonic parsing alone.
        mcp_client.call_tool(
            "signoz_query_metrics",
            {"searchContext": f"query {metric}", "metricName": metric, "timeRange": "1h", "isMonotonic": value},
        )

    result = mcp_client.call_tool(
        "signoz_query_metrics",
        {"searchContext": f"query {metric}", "metricName": metric, "isMonotonic": "yep"},
    )
    assert result.get("isError", False), "expected an error result for garbage isMonotonic"


def test_timestamp_auto_detect(mcp_client: MCPClient) -> None:
    """start/end as seconds, millis, and nanos strings all work (K1)."""
    end_ms = int(time.time() * 1000)
    start_ms = end_ms - 3_600_000
    for start, end in (
        (str(start_ms // 1000), str(end_ms // 1000)),
        (str(start_ms), str(end_ms)),
        (str(start_ms * 1_000_000), str(end_ms * 1_000_000)),
    ):
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_search_logs", {"searchContext": "logs in a window", "start": start, "end": end, "limit": "5"}
            )
        )
        json.loads(first_text_block(result))


def test_services_ns_and_ms_windows(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """list_services and top_operations work with ns AND ms windows (K1)."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_services", {"searchContext": "services", "timeRange": "1h", "limit": "50"}
        )
        return not result.get("isError", False) and service in first_text_block(result)

    wait_for(visible, f"service {service} visible")

    end_ms = int(time.time() * 1000)
    start_ms = end_ms - 6 * 3_600_000
    windows = {
        "ns": {"start": str(start_ms * 1_000_000), "end": str(end_ms * 1_000_000)},
        "ms": {"start": str(start_ms), "end": str(end_ms)},
    }
    for window in windows.values():
        assert_tool_ok(mcp_client.call_tool("signoz_list_services", {"searchContext": "services", **window}))
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_service_top_operations",
                {"searchContext": f"top operations for {service}", "service": service, **window},
            )
        )

    assert_tool_ok(
        mcp_client.call_tool("signoz_list_metrics", {"searchContext": "metrics", "timeRange": "1h", "limit": "5"})
    )


def test_docs_limit_number_and_string(mcp_client: MCPClient) -> None:
    """search_docs limit as a JSON number AND a string (K3)."""
    for limit in (3, "3"):
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_search_docs", {"searchContext": "docker docs", "query": "docker", "limit": limit}
            )
        )


def test_list_limit_clamp(mcp_client: MCPClient) -> None:
    """limit above MaxLimit clamps to 1000 and surfaces a clamp note (K3)."""
    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_list_alert_rules", {"searchContext": "list rules with a huge limit", "limit": "999999"}
        )
    )
    page = json.loads(first_text_block(result))
    effective = page.get("pagination", {}).get("limit")
    assert effective == 1000, f"effective limit = {effective}, want clamped to 1000"
    assert "clamped" in note_blocks(result), f"expected a clamp note block: {note_blocks(result)!r}"
