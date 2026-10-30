"""Enum values and grammar — port of e2e_familyd_test.go.

The signal values get_field_keys teaches, the advertised aggregation set and
both request types against the backend, and the top-operations tag filter.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block, result_code
from fixtures.telemetry import seed_traces, wait_for

# Aggregation expressions the tool descriptions use as examples; each must run
# against the backend, which is the only validator of the expression grammar.
AGGREGATIONS = [
    "count()",
    "count_distinct(name)",
    "avg(duration_nano)",
    "sum(duration_nano)",
    "min(duration_nano)",
    "max(duration_nano)",
    "p50(duration_nano)",
    "p75(duration_nano)",
    "p90(duration_nano)",
    "p95(duration_nano)",
    "p99(duration_nano)",
    "rate()",
    "countIf(has_error = true)",
]


def test_signal_enum_values(mcp_client: MCPClient) -> None:
    """metrics, traces, and logs are accepted on get_field_keys (N15)."""
    for signal in ("metrics", "traces", "logs"):
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_field_keys", {"searchContext": f"field keys for {signal}", "signal": signal}
            )
        )


def test_aggregation_set_matches_backend(mcp_client: MCPClient) -> None:
    """Every example aggregation expression, and both request types on both signals, run against the backend (N15)."""
    cases = [("traces", aggregation, "scalar") for aggregation in AGGREGATIONS]
    cases += [("logs", "count()", "scalar"), ("logs", "count()", "time_series"), ("traces", "count()", "time_series")]
    rejected = []
    for signal, aggregation, request_type in cases:
        args = {
            "searchContext": f"aggregate {signal} with {aggregation}",
            "aggregations": [aggregation],
            "timeRange": "30m",
            "requestType": request_type,
        }
        result = mcp_client.call_tool(f"signoz_aggregate_{signal}", args)
        if result.get("isError", False):
            rejected.append((signal, aggregation, request_type, first_text_block(result)[:200]))
    assert not rejected, f"aggregation drift: backend rejected what we advertise: {rejected}"


def test_service_top_operations_lists_the_seeded_operation(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """Omitted and empty tags both return the seeded operation."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    # Top operations come from a table SigNoz fills after the spans are queryable.
    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_get_service_top_operations", {"searchContext": "top operations", "service": service}
        )
        return not result.get("isError", False) and "e2e-operation" in first_text_block(result)

    wait_for(visible, f"operation of {service} visible")

    # A matching tag filter is not pinned: on the pinned release it returns no operations (SigNoz/nerve-pod#440).
    for tags in (None, "[]"):
        args = {"searchContext": f"top operations for {service}", "service": service, "timeRange": "1h"}
        if tags is not None:
            args["tags"] = tags
        body = first_text_block(assert_tool_ok(mcp_client.call_tool("signoz_get_service_top_operations", args)))
        assert "e2e-operation" in body, f"tags={tags!r}: seeded operation missing: {body[:300]}"


def test_promql_matcher_error_names_regexp(mcp_client: MCPClient) -> None:
    """SigNoz rejects a PromQL =~ matcher without naming REGEXP; the tool error adds the replacement."""
    result = mcp_client.call_tool(
        "signoz_aggregate_logs",
        {
            "searchContext": "count logs for checkout pods",
            "aggregations": "count()",
            "filter": "k8s.pod.name =~ 'checkout-.*'",
            "timeRange": "15m",
        },
    )
    body = first_text_block(result)
    assert result.get("isError", False), f"a =~ filter should fail in SigNoz, got: {body[:300]}"
    assert result_code(result) == "VALIDATION_FAILED"
    assert "but got '~'" in body, f"SigNoz's parse error changed: {body[:300]}"
    assert "Query Builder filters use REGEXP and NOT REGEXP instead" in body


def test_order_keys_the_guides_teach(mcp_client: MCPClient, test_id: str) -> None:
    """An aggregation written as in aggregations, a groupBy key, and an aggregation index all order results."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def ordered(order_by: str) -> dict:
        return mcp_client.call_tool(
            "signoz_aggregate_traces",
            {
                "searchContext": f"p99 latency of {service} by operation",
                "aggregations": "p99(duration_nano)",
                "groupBy": "name",
                "service": service,
                "orderBy": order_by,
                "timeRange": "1h",
            },
        )

    wait_for(lambda: "e2e-operation" in first_text_block(ordered("p99(duration_nano) desc")), f"{service} aggregated")
    for order_by in ("p99(duration_nano) desc", "name asc", "0 desc"):
        body = first_text_block(assert_tool_ok(ordered(order_by)))
        assert "e2e-operation" in body, f"orderBy {order_by!r} returned no groups: {body[:300]}"
