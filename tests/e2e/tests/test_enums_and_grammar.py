"""Enum values and grammar — port of e2e_familyd_test.go.

The signal values get_field_keys teaches, the advertised aggregation set and
both request types against the backend, and the top-operations tag filter.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block
from fixtures.telemetry import seed_traces, wait_for

# The aggregation set the tools advertise (aggregate_helper.go); count and rate
# need no aggregateOn field.
AGGREGATIONS = ["count", "count_distinct", "avg", "sum", "min", "max", "p50", "p75", "p90", "p95", "p99", "rate"]
AGGREGATIONS_WITHOUT_FIELD = {"count", "rate"}


def test_signal_enum_values(mcp_client: MCPClient) -> None:
    """metrics, traces, and logs are accepted on get_field_keys (N15)."""
    for signal in ("metrics", "traces", "logs"):
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_field_keys", {"searchContext": f"field keys for {signal}", "signal": signal}
            )
        )


def test_aggregation_set_matches_backend(mcp_client: MCPClient) -> None:
    """Every advertised aggregation, and both request types on both signals, run against the backend (N15)."""
    cases = [("traces", aggregation, "scalar") for aggregation in AGGREGATIONS]
    cases += [("logs", "count", "scalar"), ("logs", "count", "time_series"), ("traces", "count", "time_series")]
    rejected = []
    for signal, aggregation, request_type in cases:
        args = {
            "searchContext": f"aggregate {signal} with {aggregation}",
            "aggregation": aggregation,
            "timeRange": "30m",
            "requestType": request_type,
        }
        if aggregation not in AGGREGATIONS_WITHOUT_FIELD:
            args["aggregateOn"] = "duration_nano"
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

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_services", {"searchContext": "services", "timeRange": "1h", "limit": "1000"}
        )
        return not result.get("isError", False) and service in first_text_block(result)

    wait_for(visible, f"service {service} visible")

    # A matching tag filter is not pinned: on the pinned release it returns no operations (SigNoz/nerve-pod#440).
    for tags in (None, "[]"):
        args = {"searchContext": f"top operations for {service}", "service": service, "timeRange": "1h"}
        if tags is not None:
            args["tags"] = tags
        body = first_text_block(assert_tool_ok(mcp_client.call_tool("signoz_get_service_top_operations", args)))
        assert "e2e-operation" in body, f"tags={tags!r}: seeded operation missing: {body[:300]}"
