"""signoz_get_service_map against the dependency graph of the pinned SigNoz.

Pins what no fixture can: SigNoz still answers with a bare edge array, an edge
appears from both ends, latency is in nanoseconds, and errorRate is a
percentage, as the tool description and units note say, and environment filters the graph
by the deployment.environment SigNoz stored on the edge. SigNoz pairs a caller
and callee span only when it matches them at ingestion, so the call count is
bounded rather than exact.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, note_blocks
from fixtures.telemetry import seed_service_calls, wait_for

CALLS = 4
CALLEE_DURATION_NS = 200_000_000


def service_map(mcp_client: MCPClient, service: str, direction: str, environment: str | None = None) -> dict:
    args = {
        "searchContext": "which services call this one",
        "service": service,
        "direction": direction,
        "timeRange": "1h",
    }
    if environment is not None:
        args["environment"] = environment
    return assert_tool_ok(mcp_client.call_tool("signoz_get_service_map", args))


def test_service_map_reports_one_edge_from_both_ends(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    caller = f"mcp-e2e-{test_id}-caller"
    callee = f"mcp-e2e-{test_id}-callee"
    environment = f"mcp-e2e-{test_id}"
    seed_service_calls(caller, callee, count=CALLS, callee_duration_ns=CALLEE_DURATION_NS, environment=environment)

    def callee_edges() -> list[dict]:
        return first_block_json(service_map(mcp_client, caller, "downstream"))["data"]

    downstream = wait_for(callee_edges, f"edge {caller} -> {callee} in the dependency graph")
    assert [(edge["parent"], edge["child"]) for edge in downstream] == [(caller, callee)]
    edge = downstream[0]
    assert 1 <= edge["callCount"] <= CALLS
    assert edge["errorRate"] == 100, "errorRate is a percentage: every seeded call failed"
    assert edge["p99"] == CALLEE_DURATION_NS, "latency percentiles are nanoseconds of the callee span"
    assert edge["callRate"] > 0

    upstream = first_block_json(service_map(mcp_client, callee, "upstream"))["data"]
    assert [(e["parent"], e["child"], e["callCount"]) for e in upstream] == [(caller, callee, edge["callCount"])]

    in_environment = first_block_json(service_map(mcp_client, caller, "downstream", environment))["data"]
    assert [(e["parent"], e["child"]) for e in in_environment] == [(caller, callee)]
    elsewhere = service_map(mcp_client, caller, "downstream", f"{environment}-other")
    assert first_block_json(elsewhere)["data"] == []
    assert "the graph has no edges for this time range and filters" in note_blocks(elsewhere)

    absent = service_map(mcp_client, f"mcp-e2e-{test_id}-absent", "upstream")
    assert first_block_json(absent)["data"] == []
    assert "no edge in this window has the requested service" in note_blocks(absent)
