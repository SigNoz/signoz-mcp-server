"""Parameter validation errors — ports of Family B (validation half) and K4.

Canonical strings keep the capital-P "Parameter validation failed:" prefix, and
machine-readable codes come from structuredContent.
"""

from fixtures.mcpclient import MCPClient
from fixtures.results import first_text_block, result_code

PREFIX = "Parameter validation failed:"

VALIDATION_CASES = [
    # (tool, arguments, expected substring)
    ("signoz_get_alert", {}, '"id" is required'),
    # A wrong-typed legacy alias is treated as absent, so the canonical id error.
    ("signoz_get_alert", {"ruleId": 12345}, '"id" is required'),
    ("signoz_get_dashboard", {}, '"id" is required'),
    ("signoz_get_dashboard", {"uuid": True}, '"id" is required'),
    ("signoz_get_view", {}, '"id" is required'),
    ("signoz_get_notification_channel", {}, '"id" cannot be empty'),
    ("signoz_execute_builder_query", {}, '"query" must be a JSON object'),
]


def test_validation_strings(mcp_client: MCPClient) -> None:
    """Every case yields the canonical capital-P validation error (Family B)."""
    for tool, arguments, expected in VALIDATION_CASES:
        result = mcp_client.call_tool(tool, {"searchContext": f"validate {tool} inputs", **arguments})
        body = first_text_block(result)
        assert result.get("isError", False), f"{tool}{arguments}: expected an error result, got: {body[:300]}"
        assert expected in body, f"{tool}{arguments}: error text = {body!r}, want substring {expected!r}"
        assert body.startswith(PREFIX), f"{tool}{arguments}: error text = {body!r}, want canonical prefix"


def test_trace_timestamp_is_parameter_error(mcp_client: MCPClient) -> None:
    """A malformed start/end is a parameter error, never 'Internal error:' (N23)."""
    result = mcp_client.call_tool(
        "signoz_get_trace_details",
        {
            "searchContext": "trace details in a bad window",
            "traceId": "deadbeefdeadbeefdeadbeefdeadbeef",
            "start": "not-a-timestamp",
            "end": "also-bad",
        },
    )
    body = first_text_block(result)
    assert result.get("isError", False), f"expected an error result, got success: {body[:300]}"
    assert "Internal error:" not in body, f"trace timestamp parse must not be labeled Internal error: {body!r}"
    assert body.startswith(PREFIX), f"trace timestamp parse should be a parameter error: {body!r}"


def test_request_type_validation(mcp_client: MCPClient) -> None:
    """Unknown requestType is rejected with the VALIDATION_FAILED code (K4)."""
    valid = mcp_client.call_tool(
        "signoz_aggregate_logs",
        {"searchContext": "count logs", "aggregation": "count", "timeRange": "1h", "requestType": "scalar"},
    )
    assert not valid.get("isError", False), f"valid scalar aggregate_logs error: {first_text_block(valid)[:300]}"

    for tool, extra in (
        ("signoz_aggregate_logs", {"aggregation": "count", "timeRange": "1h"}),
        ("signoz_query_metrics", {"metricName": "system.cpu.time", "timeRange": "1h"}),
    ):
        result = mcp_client.call_tool(
            tool, {"searchContext": f"validate {tool} requestType", **extra, "requestType": "totally-bogus"}
        )
        body = first_text_block(result)
        assert result.get("isError", False), f"{tool}: unknown requestType should be rejected"
        assert "requestType" in body, f"{tool}: rejection should mention requestType: {body!r}"
        assert result_code(result) == "VALIDATION_FAILED", (
            f"{tool}: code = {result_code(result)!r}, want VALIDATION_FAILED"
        )
