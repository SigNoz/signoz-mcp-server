"""signoz_get_service_map against the dependency graph of the pinned SigNoz.

Pins what no fixture can: SigNoz still answers with a bare edge array, an edge
appears from both ends, latency is in nanoseconds of the callee's span, and
errorRate is a percentage, as the tool description and units note say, and the
caller's own client spans, the route the note gives, report a different latency.
Client spans to two callees share a span name, so grouping them by name pools
the destinations and grouping by server.address keeps each one, as the note
says. Environment filters the graph by the deployment.environment SigNoz stored
on the edge. SigNoz pairs a caller and callee span only when it matches them at
ingestion, so the call count is bounded rather than exact.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, note_blocks
from fixtures.telemetry import seed_service_calls, wait_for

CALLS = 4
CALLEE_DURATION_NS = 200_000_000
OTHER_DURATION_NS = 50_000_000
# seed_service_calls gives each caller client span the callee span's duration plus 10 ms.
CLIENT_OVERHEAD_NS = 10_000_000


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
    other = f"mcp-e2e-{test_id}-callee2"
    environment = f"mcp-e2e-{test_id}"
    durations = {callee: CALLEE_DURATION_NS, other: OTHER_DURATION_NS}
    for child, duration in durations.items():
        seed_service_calls(
            caller, child, count=CALLS, callee_duration_ns=duration, environment=environment, server_address=child
        )

    def callee_edges() -> dict[str, dict]:
        edges = first_block_json(service_map(mcp_client, caller, "downstream"))["data"]
        return {edge["child"]: edge for edge in edges} if len(edges) == len(durations) else {}

    downstream = wait_for(callee_edges, f"edges from {caller} to {callee} and {other} in the dependency graph")
    assert {(edge["parent"], child) for child, edge in downstream.items()} == {(caller, callee), (caller, other)}
    for child, edge in downstream.items():
        assert 1 <= edge["callCount"] <= CALLS
        assert edge["errorRate"] == 100, "errorRate is a percentage: every seeded call failed"
        assert edge["p99"] == durations[child], "latency percentiles are nanoseconds of the callee span"
        assert edge["callRate"] > 0
    edge = downstream[callee]

    def client_spans(group_by: str, aggregation: str) -> dict[str, int]:
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_aggregate_traces",
                {
                    "searchContext": "how long the caller waited on its outgoing calls",
                    "service": caller,
                    "filter": "kind_string = 'Client'",
                    "aggregations": aggregation,
                    "groupBy": group_by,
                    "timeRange": "1h",
                },
            )
        )
        rows = [row for r in first_block_json(result)["data"]["data"]["results"] for row in r.get("data") or []]
        # A percentile over no spans comes back as "NaN".
        return {row[0]: int(float(row[-1])) for row in rows if row[-1] != "NaN"}

    def p99_per_destination() -> dict[str, int]:
        found = client_spans("server.address", "p99(duration_nano)")
        return found if len(found) == len(durations) else {}

    caller_side = wait_for(p99_per_destination, f"client spans of {caller} to each destination")
    assert caller_side == {child: duration + CLIENT_OVERHEAD_NS for child, duration in durations.items()}, (
        "grouped by server.address, the caller's client spans report the latency it saw from each destination"
    )
    assert caller_side[callee] != edge["p99"], "the edge reports the callee's latency, not the caller's"
    assert client_spans("name", "count()") == {"POST /reserve": 2 * CALLS}, (
        "grouped by span name, client spans to both destinations pool into one row"
    )

    upstream = first_block_json(service_map(mcp_client, callee, "upstream"))["data"]
    assert [(e["parent"], e["child"], e["callCount"]) for e in upstream] == [(caller, callee, edge["callCount"])]

    in_environment = service_map(mcp_client, caller, "downstream", environment)
    assert {(e["parent"], e["child"]) for e in first_block_json(in_environment)["data"]} == {
        (caller, callee),
        (caller, other),
    }
    notes = note_blocks(in_environment)
    assert "come from the callee's spans" in notes, "the units note says whose spans an edge's latency comes from"
    assert "such as server.address" in notes, "the units note says how to keep one destination"
    elsewhere = service_map(mcp_client, caller, "downstream", f"{environment}-other")
    assert first_block_json(elsewhere)["data"] == []
    assert "the graph has no edges for this time range and filters" in note_blocks(elsewhere)

    absent = service_map(mcp_client, f"mcp-e2e-{test_id}-absent", "upstream")
    assert first_block_json(absent)["data"] == []
    assert "no edge in this window has the requested service" in note_blocks(absent)
