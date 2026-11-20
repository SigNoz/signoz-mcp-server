"""signoz_list_services and signoz_get_service_top_operations against the pinned SigNoz.

Services come back sorted by name, so offset pages neither skip nor repeat
one, and a name containing `@` keeps its statistics. Latencies are
nanoseconds and error rates are percentages, as both tool descriptions say,
and SigNoz applies the top-operations limit after ranking by p99.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, note_blocks
from fixtures.telemetry import seed_traces, wait_for

# seed_traces makes each root span 250 ms long and each child span 100 ms.
ROOT_SPAN_NS = 250_000_000
CHILD_SPAN_NS = 100_000_000


def list_services(mcp_client: MCPClient, limit: int, offset: int = 0) -> dict:
    return first_block_json(
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_list_services",
                {"searchContext": "list services", "timeRange": "1h", "limit": str(limit), "offset": str(offset)},
            )
        )
    )


def test_services_sorted_by_name_and_paged_without_gaps(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    at_sign = f"mcp-e2e-{test_id}-b@stg"
    plain = f"mcp-e2e-{test_id}-a"
    seed_traces(at_sign, count=2)
    seed_traces(plain, count=2)

    def both_listed() -> bool:
        names = {item["serviceName"] for item in list_services(mcp_client, 1000)["data"]}
        return {at_sign, plain} <= names

    wait_for(both_listed, f"services {plain} and {at_sign} listed")

    listing = list_services(mcp_client, 1000)
    names = [item["serviceName"] for item in listing["data"]]
    assert names == sorted(names)
    assert names.index(plain) < names.index(at_sign)

    seeded = next(item for item in listing["data"] if item["serviceName"] == at_sign)
    assert seeded["numCalls"] == 2
    assert seeded["numErrors"] == 1
    assert seeded["errorRate"] == 50, "errorRate is a percentage: one of two calls failed"
    assert seeded["p99"] == ROOT_SPAN_NS, "latencies are nanoseconds"
    assert seeded["avgDuration"] == ROOT_SPAN_NS

    paged: list[str] = []
    offset = 0
    while True:
        page = list_services(mcp_client, 3, offset)
        paged += [item["serviceName"] for item in page["data"]]
        if not page["pagination"]["hasMore"]:
            break
        offset = page["pagination"]["nextOffset"]
    assert set(names) <= set(paged)
    assert len(paged) == len(set(paged)), "a service appeared on two pages"


def top_operations(mcp_client: MCPClient, service: str, limit: str | None = None) -> dict:
    args = {"searchContext": "slowest operations", "service": service, "timeRange": "1h"}
    if limit is not None:
        args["limit"] = limit
    return assert_tool_ok(mcp_client.call_tool("signoz_get_service_top_operations", args))


def test_top_operations_keep_the_slowest_within_limit(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2, with_child=True)

    # Top operations come from a table SigNoz fills after the spans are queryable.
    def both_operations() -> list[dict]:
        operations = first_block_json(top_operations(mcp_client, service))
        return operations if len(operations) == 2 else []

    operations = wait_for(both_operations, f"two operations of {service}")
    assert [op["name"] for op in operations] == ["e2e-operation", "e2e-operation-child"], "highest p99 first"
    assert [op["p99"] for op in operations] == [ROOT_SPAN_NS, CHILD_SPAN_NS], "latencies are nanoseconds"

    slowest = top_operations(mcp_client, service, limit="1")
    assert [op["name"] for op in first_block_json(slowest)] == ["e2e-operation"]
    assert "operations with the highest p99" in note_blocks(slowest)
