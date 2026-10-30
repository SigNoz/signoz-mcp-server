"""signoz_list_services against the services endpoint of the pinned SigNoz.

Services come back sorted by name, so offset pages neither skip nor repeat
one, and a name containing `@` keeps its statistics.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json
from fixtures.telemetry import seed_traces, wait_for


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
