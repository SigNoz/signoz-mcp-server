"""Result size budget on the raw searches against the pinned release.

A small maxResultBytes splits SigNoz's real rows into several pages, and
following pagination.nextOffset returns every matching row exactly once, in
order. The handler tests pin how a page is cut and what its pagination says.
"""

from collections.abc import Callable

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json
from fixtures.telemetry import seed_logs, seed_traces, wait_for

SEEDED = 12
SMALL_BUDGET = 2500
LARGE_BUDGET = 200000


def _rows(body: dict) -> list[dict]:
    rows = []
    for result in body["data"]["data"]["results"]:
        rows.extend(result.get("rows") or [])
    return rows


def _walk_pages(search: Callable[..., dict]) -> tuple[list[dict], int]:
    """Follow pagination.nextOffset under SMALL_BUDGET; return the rows and the number of pages."""
    offset, pages, collected = 0, 0, []
    while True:
        body = first_block_json(assert_tool_ok(search(offset=str(offset), maxResultBytes=str(SMALL_BUDGET))))
        collected.extend(_rows(body))
        pages += 1
        pagination = body["pagination"]
        if not pagination["hasMore"]:
            return collected, pages
        assert pagination["nextOffset"] > offset and pages <= SEEDED, f"paging must make progress: {pagination}"
        offset = pagination["nextOffset"]


def test_search_logs_pages_by_result_size(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """A small budget splits twelve logs into several whole-row pages that add up to all twelve."""
    service = f"mcp-e2e-budget-{test_id}"
    seed_logs(service, f"budget-{test_id} " + "x" * 200, count=SEEDED)

    def search(**arguments: str) -> dict:
        return mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": "page the seeded logs by result size",
                "service": service,
                "selectFields": ["body"],
                "timeRange": "1h",
                "limit": "50",
                **arguments,
            },
        )

    def all_visible() -> bool:
        result = search(maxResultBytes=str(LARGE_BUDGET))
        return not result.get("isError", False) and len(_rows(first_block_json(result))) == SEEDED

    wait_for(all_visible, f"all {SEEDED} seeded logs for {service} visible")
    full = _rows(first_block_json(assert_tool_ok(search(maxResultBytes=str(LARGE_BUDGET)))))
    assert len({row["data"]["id"] for row in full}) == SEEDED

    collected, pages = _walk_pages(search)
    assert pages > 1, f"a {SMALL_BUDGET}-byte budget must split SigNoz's {SEEDED} rows into several pages"
    assert collected == full, "following nextOffset must return every row SigNoz holds exactly once, in order"


def test_search_traces_pages_by_result_size(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """The same holds for span rows, measured with their webUrl links."""
    service = f"mcp-e2e-budget-{test_id}"
    seed_traces(service, count=SEEDED)

    def search(**arguments: str) -> dict:
        return mcp_client.call_tool(
            "signoz_search_traces",
            {
                "searchContext": "page the seeded spans by result size",
                "service": service,
                "selectFields": ["name"],
                "timeRange": "1h",
                "limit": "50",
                **arguments,
            },
        )

    def all_visible() -> bool:
        result = search(maxResultBytes=str(LARGE_BUDGET))
        return not result.get("isError", False) and len(_rows(first_block_json(result))) == SEEDED

    wait_for(all_visible, f"all {SEEDED} seeded spans for {service} visible")
    full = _rows(first_block_json(assert_tool_ok(search(maxResultBytes=str(LARGE_BUDGET)))))
    assert len({row["data"]["span_id"] for row in full}) == SEEDED

    collected, pages = _walk_pages(search)
    assert pages > 1, f"a {SMALL_BUDGET}-byte budget must split SigNoz's {SEEDED} rows into several pages"
    assert collected == full, "following nextOffset must return every row SigNoz holds exactly once, in order"
