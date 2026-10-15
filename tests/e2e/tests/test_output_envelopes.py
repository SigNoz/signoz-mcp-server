"""Output envelopes — port of e2e_familyc_test.go.

structuredContent on code-controlled list and get tools and the JSON-first
query_metrics envelope. Get-tool assertions use resources seeded on the fresh
cast (the Go staging tests skipped when the instance was empty). Mutation
envelopes are checked by the seeded channel helpers; the passthrough half of
the structuredContent rule is pinned in Go.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import (
    assert_no_structured,
    assert_structured_matches_text,
    dig_id,
    first_block_json,
    first_text_block,
    query_range_rows,
)
from fixtures.seeded import (
    alert_rule_gone,
    channel_gone,
    create_alert_rule,
    create_channel,
    create_view,
    delete_alert_rule,
    delete_channel,
    delete_view,
    view_gone,
)
from fixtures.telemetry import seed_metrics, seed_traces, wait_for


def test_structured_content_on_list_tools(mcp_client: MCPClient) -> None:
    calls = [
        ("signoz_list_services", {"timeRange": "1h"}),
        ("signoz_list_alerts", {}),
        ("signoz_list_alert_rules", {}),
        ("signoz_list_dashboards", {}),
        ("signoz_list_views", {"source": "traces"}),
        ("signoz_list_notification_channels", {}),
    ]
    for tool, args in calls:
        result = assert_tool_ok(mcp_client.call_tool(tool, {"searchContext": f"call {tool}", **args}))
        assert_structured_matches_text(result)


def test_structured_content_on_get_tools(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    # Seed one resource of each kind so every get path runs (never skipped).
    dashboard = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_create_dashboard",
            {
                "searchContext": f"create dashboard mcp-e2e-{test_id}",
                "schemaVersion": "v6",
                "tags": [],
                "spec": {"display": {"name": f"mcp-e2e-{test_id}"}, "variables": [], "panels": {}, "layouts": []},
            },
        )
    )
    dashboard_id = dig_id(first_block_json(dashboard))
    channel_id = create_channel(
        mcp_client,
        f"mcp-e2e-ch-{test_id}",
        {"kind": "webhook", "spec": {"url": "http://example.invalid/no-send"}},
    )
    rule_id = create_alert_rule(mcp_client, f"mcp-e2e-rule-{test_id}", channel_name=f"mcp-e2e-ch-{test_id}")
    view_id = create_view(mcp_client, f"mcp-e2e-view-{test_id}")
    seed_traces(f"mcp-e2e-{test_id}", count=1)

    def trace_visible() -> str:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": "recent traces", "service": f"mcp-e2e-{test_id}", "timeRange": "1h", "limit": "5"},
        )
        if result.get("isError", False):
            return ""
        rows = query_range_rows(first_text_block(result))
        return rows[0].get("trace_id", "") if rows else ""

    trace_id = wait_for(trace_visible, "seeded trace visible")

    try:
        for tool, args in (
            ("signoz_get_dashboard", {"id": dashboard_id}),
            ("signoz_get_alert", {"id": rule_id}),
            ("signoz_get_view", {"id": view_id}),
            ("signoz_get_notification_channel", {"id": channel_id}),
            ("signoz_get_trace_details", {"traceId": trace_id, "timeRange": "1h"}),
        ):
            result = assert_tool_ok(mcp_client.call_tool(tool, {"searchContext": f"call {tool}", **args}))
            assert_structured_matches_text(result)
    finally:
        delete_view(mcp_client, view_id)
        assert view_gone(mcp_client, view_id), f"view {view_id} remained after cleanup"
        delete_alert_rule(mcp_client, rule_id)
        assert alert_rule_gone(mcp_client, rule_id), f"alert rule {rule_id} remained after cleanup"
        delete_channel(mcp_client, channel_id)
        assert channel_gone(mcp_client, channel_id), f"channel {channel_id} remained after cleanup"
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_delete_dashboard",
                {"searchContext": f"cleanup dashboard {dashboard_id}", "id": dashboard_id},
            )
        )
        dashboard_gone = mcp_client.call_tool(
            "signoz_get_dashboard",
            {"searchContext": f"confirm dashboard {dashboard_id} gone", "id": dashboard_id},
        )
        assert dashboard_gone.get("isError", False), f"dashboard {dashboard_id} remained after cleanup"


def test_query_metrics_json_first(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Block 0 is parseable JSON; decisions/warnings are a SEPARATE note block."""
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool("signoz_list_metrics", {"searchContext": f"find {metric}", "limit": "1000"})
        return not result.get("isError", False) and metric in first_text_block(result)

    wait_for(visible, f"seeded metric {metric} visible")

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_query_metrics", {"searchContext": f"query {metric}", "metricName": metric, "timeRange": "1h"}
        )
    )
    assert_no_structured(result)
    first_block_json(result)  # block 0 must be valid JSON with no prose preamble
    if len(result.get("content", [])) >= 2:
        assert result["content"][1].get("type") == "text", "query_metrics block 1 should be a text note"
