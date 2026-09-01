"""Enum values and grammar — port of e2e_familyd_test.go.

Stable enum sets (requestType, signal, order, alert-history state), the
advertised aggregation set against the backend, timeRange/stepInterval grammar,
search_docs param + legacy alias, and service top-operations tag passthrough.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, first_text_block
from fixtures.seeded import create_alert_rule, create_channel, delete_alert_rule, delete_channel
from fixtures.telemetry import seed_traces, wait_for

ALERT_HISTORY_STATES = ["inactive", "pending", "recovering", "firing", "nodata", "disabled"]

# The aggregation set the tools advertise (aggregate_helper.go); count and rate
# need no aggregateOn field.
AGGREGATIONS = ["count", "count_distinct", "avg", "sum", "min", "max", "p50", "p75", "p90", "p95", "p99", "rate"]
AGGREGATIONS_WITHOUT_FIELD = {"count", "rate"}


def test_request_type_enum_values(mcp_client: MCPClient) -> None:
    """scalar and time_series are accepted on both aggregate signals (N15)."""
    for signal in ("logs", "traces"):
        for request_type in ("scalar", "time_series"):
            assert_tool_ok(
                mcp_client.call_tool(
                    f"signoz_aggregate_{signal}",
                    {
                        "searchContext": f"aggregate {signal}",
                        "aggregation": "count",
                        "timeRange": "30m",
                        "requestType": request_type,
                    },
                )
            )


def test_signal_enum_values(mcp_client: MCPClient) -> None:
    """metrics, traces, and logs are accepted on get_field_keys (N15)."""
    for signal in ("metrics", "traces", "logs"):
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_field_keys", {"searchContext": f"field keys for {signal}", "signal": signal}
            )
        )


def test_alert_history_enums(mcp_client: MCPClient, test_id: str) -> None:
    """order (asc/desc) and every v2 state are accepted on get_alert_history."""
    channel_name = f"mcp-e2e-ch-{test_id}"
    channel_id = create_channel(mcp_client, channel_name)
    rule_id = None
    try:
        rule_id = create_alert_rule(mcp_client, f"mcp-e2e-rule-{test_id}", channel_name=channel_name)
        for order in ("asc", "desc"):
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_alert_history",
                    {"searchContext": f"history for {rule_id}", "ruleId": rule_id, "timeRange": "24h", "order": order},
                )
            )
        for state in ALERT_HISTORY_STATES:
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_alert_history",
                    {"searchContext": f"history for {rule_id}", "ruleId": rule_id, "timeRange": "24h", "state": state},
                )
            )
    finally:
        if rule_id is not None:
            delete_alert_rule(mcp_client, rule_id)
        delete_channel(mcp_client, channel_id)


def test_aggregation_set_matches_backend(mcp_client: MCPClient) -> None:
    """Every advertised aggregation operator runs against the backend (N15 split)."""
    rejected = []
    for aggregation in AGGREGATIONS:
        args = {
            "searchContext": f"aggregate traces with {aggregation}",
            "aggregation": aggregation,
            "timeRange": "30m",
            "requestType": "scalar",
        }
        if aggregation not in AGGREGATIONS_WITHOUT_FIELD:
            args["aggregateOn"] = "duration_nano"
        result = mcp_client.call_tool("signoz_aggregate_traces", args)
        if result.get("isError", False):
            rejected.append((aggregation, first_text_block(result)[:200]))
    assert not rejected, f"aggregation drift: backend rejected operators we advertise: {rejected}"


def test_time_range_and_step_interval_grammar(mcp_client: MCPClient) -> None:
    """The advertised timeRange grammar works: '2h', '30m', stepInterval seconds (N26)."""
    for time_range in ("2h", "30m"):
        assert_tool_ok(
            mcp_client.call_tool("signoz_get_top_metrics", {"searchContext": "top metrics", "timeRange": time_range})
        )

    assert_tool_ok(
        mcp_client.call_tool(
            "signoz_aggregate_logs",
            {
                "searchContext": "count logs hourly",
                "aggregation": "count",
                "timeRange": "1h",
                "requestType": "time_series",
                "stepInterval": "3600",
            },
        )
    )


def test_search_docs_param_and_alias(mcp_client: MCPClient) -> None:
    """The canonical searchText param and the permanent legacy query alias (N12)."""
    for key in ("searchText", "query"):
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_search_docs",
                {"searchContext": "docker collector logs docs", key: "docker collector logs", "limit": 5},
            )
        )
        results = result.get("structuredContent", {}).get("results", [])
        assert results, f"search_docs via {key!r} returned no results"


def test_service_top_operations_tags(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Empty tags and a real structured tag filter both round-trip (N14)."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_services", {"searchContext": "services", "timeRange": "1h", "limit": "50"}
        )
        return not result.get("isError", False) and service in first_text_block(result)

    wait_for(visible, f"service {service} visible")

    for tags in (
        None,
        "[]",
        '[{"key":"http.method","tagType":"SpanAttribute","operator":"In","stringValues":["GET"]}]',
    ):
        args = {"searchContext": f"top operations for {service}", "service": service, "timeRange": "1h"}
        if tags is not None:
            args["tags"] = tags
        result = assert_tool_ok(mcp_client.call_tool("signoz_get_service_top_operations", args))
        first_block_json(result)
