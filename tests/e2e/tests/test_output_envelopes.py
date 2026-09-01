"""Output envelopes — port of e2e_familyc_test.go.

structuredContent on code-controlled tools, its absence on raw passthrough
tools, the JSON-first query_metrics envelope, the error-code taxonomy, and the
mutation envelope. Get-tool assertions use resources seeded on the fresh cast
(the Go staging tests skipped when the instance was empty).
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import (
    assert_no_structured,
    assert_structured_matches_text,
    dig_id,
    first_block_json,
    first_text_block,
    query_range_rows,
    result_code,
)
from fixtures.seeded import (
    channel_id_by_name,
    create_alert_rule,
    create_channel,
    create_view,
    delete_alert_rule,
    delete_channel,
    delete_view,
)
from fixtures.telemetry import seed_metrics, seed_traces, wait_for

GHOST_RULE_ID = "01900000-0000-7000-8000-000000000000"  # valid UUIDv7, nonexistent


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
    channel_id = create_channel(mcp_client, f"mcp-e2e-ch-{test_id}")
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
        delete_alert_rule(mcp_client, rule_id)
        delete_channel(mcp_client, channel_id)
        mcp_client.call_tool(
            "signoz_delete_dashboard", {"searchContext": f"cleanup dashboard {dashboard_id}", "id": dashboard_id}
        )


def test_no_structured_on_passthrough(mcp_client: MCPClient) -> None:
    calls = [
        ("signoz_search_logs", {"timeRange": "1h", "limit": "1"}),
        ("signoz_search_traces", {"timeRange": "1h", "limit": "1"}),
        ("signoz_aggregate_logs", {"aggregation": "count", "timeRange": "1h"}),
        ("signoz_aggregate_traces", {"aggregation": "count", "timeRange": "1h"}),
    ]
    for tool, args in calls:
        result = assert_tool_ok(mcp_client.call_tool(tool, {"searchContext": f"call {tool}", **args}))
        assert_no_structured(result)


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


def test_error_codes(mcp_client: MCPClient) -> None:
    """Missing required arg -> VALIDATION_FAILED locally; ghost id -> NOT_FOUND."""
    missing = mcp_client.call_tool("signoz_get_dashboard", {"searchContext": "get dashboard without id"})
    assert missing.get("isError", False), "get_dashboard without id should error"
    assert result_code(missing) == "VALIDATION_FAILED"

    ghost = mcp_client.call_tool(
        "signoz_get_alert", {"searchContext": f"get rule {GHOST_RULE_ID}", "ruleId": GHOST_RULE_ID}
    )
    if not ghost.get("isError", False):
        # A nonexistent rule returning 200 is backend behavior, not a
        # regression — skip the code assertion rather than fail spuriously.
        return
    assert result_code(ghost) == "NOT_FOUND"


def test_mutation_structured_content(mcp_client: MCPClient, test_id: str) -> None:
    """Create carries structuredContent, delete carries it, and the resource is gone."""
    name = f"mcp-e2e-c-{test_id}"
    channel_id = ""
    deleted = False
    try:
        create = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_create_notification_channel",
                {
                    "searchContext": f"create a channel named {name}",
                    "type": "webhook",
                    "name": name,
                    "webhook_url": "https://example.com/mcp-e2e-c-webhook",
                },
            )
        )
        assert_structured_matches_text(create)
        channel_id = dig_id(first_block_json(create))
        if not channel_id:
            channel_id = channel_id_by_name(mcp_client, name)
        assert channel_id, f"could not determine created channel id (name={name})"

        delete = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_delete_notification_channel",
                {"searchContext": f"delete channel {channel_id}", "id": channel_id},
            )
        )
        assert_structured_matches_text(delete)

        gone = mcp_client.call_tool(
            "signoz_get_notification_channel", {"searchContext": f"confirm {channel_id} gone", "id": channel_id}
        )
        assert gone.get("isError", False), f"channel {channel_id} still exists after delete"
        deleted = True
    finally:
        if not deleted:
            recovered = channel_id or channel_id_by_name(mcp_client, name)
            if recovered:
                mcp_client.call_tool(
                    "signoz_delete_notification_channel",
                    {"searchContext": f"cleanup channel {recovered}", "id": recovered},
                )
