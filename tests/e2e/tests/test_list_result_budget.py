"""Result size budget on the list tools against the pinned release.

The list tools take no per-call budget, so this test runs a second server with
a small MCP_MAX_RESULT_BYTES. It splits SigNoz's real dashboard list into
several pages, and following pagination.nextOffset returns every dashboard
exactly once, in order. The handler tests pin how a page is cut and what its
pagination says.
"""

from collections.abc import Iterator

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.mcpserver import SMALL_RESULT_BUDGET, MCPServer
from fixtures.results import first_block_json

CREATED = 8
DESCRIPTION = "A dashboard created to page the dashboard list by result size. " * 5


@pytest.fixture(scope="module")
def small_budget_client(mcp_server_with_small_result_budget: MCPServer) -> Iterator[MCPClient]:
    client = MCPClient(mcp_server_with_small_result_budget.mcp_url)
    client.initialize()
    yield client
    client.close()


def _without_web_url(items: list[dict]) -> list[dict]:
    """Items without webUrl, which each server builds from its own SigNoz URL when SigNoz has no external URL."""
    return [{key: value for key, value in item.items() if key != "webUrl"} for item in items]


def test_list_dashboards_pages_by_result_size(
    mcp_client: MCPClient, small_budget_client: MCPClient, test_id: str
) -> None:
    """A small budget splits SigNoz's dashboard list into pages that add up to the whole list."""
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

        full = first_block_json(assert_tool_ok(list_page(mcp_client, 0)))["data"]
        assert sorted(item["id"] for item in full["dashboards"]) == sorted(created)

        offset, pages = 0, []
        while True:
            page = first_block_json(assert_tool_ok(list_page(small_budget_client, offset)))
            pages.append(page)
            pagination = page.get("pagination")
            if pagination is None or not pagination["hasMore"]:
                break
            assert pagination["nextOffset"] > offset and len(pages) <= CREATED, (
                f"paging must make progress: {pagination}"
            )
            offset = pagination["nextOffset"]

        assert len(pages) > 1, (
            f"a {SMALL_RESULT_BUDGET}-byte budget must split SigNoz's list of {CREATED} dashboards into several pages"
        )
        collected = [item for page in pages for item in page["data"]["dashboards"]]
        assert _without_web_url(collected) == _without_web_url(full["dashboards"]), (
            "following nextOffset must return every dashboard SigNoz holds once, whole, in order"
        )
        # A cut page reports SigNoz's total, so it must count the whole list at every offset.
        assert [page["data"]["total"] for page in pages] == [CREATED] * len(pages)
    finally:
        remaining = []
        for dashboard_id in created:
            args = {"searchContext": f"delete budget test dashboard {dashboard_id}", "id": dashboard_id}
            mcp_client.call_tool("signoz_delete_dashboard", args)
            if not mcp_client.call_tool("signoz_get_dashboard", args).get("isError", False):
                remaining.append(dashboard_id)
        assert not remaining, f"dashboards remained after cleanup: {remaining}"
