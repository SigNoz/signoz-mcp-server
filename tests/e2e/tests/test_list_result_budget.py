"""Result size budget on the list tools against the pinned release.

The list tools take no per-call budget, so this test runs a second server with
a small MCP_MAX_RESULT_BYTES. A page over it ends on a whole item, and
following pagination.nextOffset returns every item exactly once.
"""

from collections.abc import Callable, Iterator

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.mcpserver import SMALL_RESULT_BUDGET, MCPServer
from fixtures.results import first_block_json

CREATED = 8
DESCRIPTION = "A dashboard created to page the dashboard list by result size. " * 5


def _text_bytes(result: dict) -> int:
    """UTF-8 bytes of every text block, the number the budget bounds."""
    return sum(
        len(block.get("text", "").encode("utf-8")) for block in result.get("content", []) if block.get("type") == "text"
    )


@pytest.fixture(scope="module")
def small_budget_client(mcp_server_with_small_result_budget: MCPServer) -> Iterator[MCPClient]:
    client = MCPClient(mcp_server_with_small_result_budget.mcp_url)
    client.initialize()
    yield client
    client.close()


def _without_web_url(items: list[dict]) -> list[dict]:
    """Items without webUrl, which each server builds from its own SigNoz URL when SigNoz has no external URL."""
    return [{key: value for key, value in item.items() if key != "webUrl"} for item in items]


def _walk_pages(list_page: Callable[[int], dict], items_of: Callable[[dict], list]) -> tuple[list, int]:
    """Follow nextOffset from offset 0; every page must be whole items within the budget."""
    offset, pages, collected = 0, 0, []
    while True:
        result = assert_tool_ok(list_page(offset))
        body = first_block_json(result)
        items, pagination = items_of(body), body.get("pagination")
        assert items, f"a page with items left must return at least one: {pagination}"
        if len(items) > 1:
            assert _text_bytes(result) <= SMALL_RESULT_BUDGET, (
                f"page at offset {offset} holds {len(items)} items and is over its budget: {_text_bytes(result)} bytes"
            )
        collected.extend(items)
        pages += 1
        if pagination is None or not pagination["hasMore"]:
            assert pagination is None or not pagination.get("truncated"), pagination
            break
        assert pagination["truncated"] and pagination["maxResultBytes"] == SMALL_RESULT_BUDGET, (
            f"with limit {pagination['limit']} only the budget can end this page: {pagination}"
        )
        assert pagination["offset"] == offset and pagination["nextOffset"] == offset + len(items), pagination
        offset = pagination["nextOffset"]
        assert pages <= CREATED, "paging must make progress"
    return collected, pages


def test_list_dashboards_pages_by_result_size(
    mcp_client: MCPClient, small_budget_client: MCPClient, test_id: str
) -> None:
    """SigNoz's dashboard page gains pagination only when the budget cuts it, and the walk covers total."""
    prefix = f"mcp-e2e-list-budget-{test_id}"
    created: list[str] = []

    def list_page(client: MCPClient, offset: int) -> dict:
        return client.call_tool(
            "signoz_list_dashboards",
            {
                "searchContext": "page the budget test dashboards by result size",
                "filter": f"name CONTAINS '{prefix}'",
                "sort": "name",
                "order": "asc",
                "limit": "50",
                "offset": str(offset),
            },
        )

    try:
        for index in range(CREATED):
            title = f"{prefix}-{index:02d}"
            result = assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_create_dashboard",
                    {
                        "searchContext": f"create the dashboard {title} to page by result size",
                        "schemaVersion": "v6",
                        "tags": [],
                        "spec": {
                            "display": {"name": title, "description": DESCRIPTION},
                            "variables": [],
                            "panels": {
                                "note": {
                                    "kind": "Panel",
                                    "spec": {
                                        "display": {"name": "Note"},
                                        "links": [],
                                        "plugin": {"kind": "signoz/TextPanel", "spec": {}},
                                        "queries": [],
                                    },
                                }
                            },
                            "layouts": [
                                {
                                    "kind": "Grid",
                                    "spec": {
                                        "items": [
                                            {
                                                "x": 0,
                                                "y": 0,
                                                "width": 6,
                                                "height": 6,
                                                "content": {"$ref": "#/spec/panels/note"},
                                            }
                                        ]
                                    },
                                }
                            ],
                        },
                    },
                )
            )
            body = first_block_json(result)
            created.append(body.get("data", body)["id"])

        full = first_block_json(assert_tool_ok(list_page(mcp_client, 0)))
        assert "pagination" not in full, "a dashboard page that fits keeps SigNoz's body without pagination"
        assert full["data"]["total"] == CREATED
        assert sorted(item["id"] for item in full["data"]["dashboards"]) == sorted(created)

        pages = []

        def small_page(offset: int) -> dict:
            result = list_page(small_budget_client, offset)
            pages.append(first_block_json(assert_tool_ok(result)))
            return result

        collected, count = _walk_pages(small_page, lambda body: body["data"]["dashboards"])
        assert count > 1, f"a {SMALL_RESULT_BUDGET}-byte budget must split {CREATED} dashboards into several pages"
        assert _without_web_url(collected) == _without_web_url(full["data"]["dashboards"]), (
            "following nextOffset must return every dashboard once, whole, in order"
        )
        for page in pages:
            assert page["data"]["total"] == CREATED, "a cut keeps SigNoz's total"
            if "pagination" in page:
                assert page["pagination"]["total"] == CREATED, page["pagination"]
    finally:
        remaining = []
        for dashboard_id in created:
            args = {"searchContext": f"delete budget test dashboard {dashboard_id}", "id": dashboard_id}
            mcp_client.call_tool("signoz_delete_dashboard", args)
            if not mcp_client.call_tool("signoz_get_dashboard", args).get("isError", False):
                remaining.append(dashboard_id)
        assert not remaining, f"dashboards remained after cleanup: {remaining}"
