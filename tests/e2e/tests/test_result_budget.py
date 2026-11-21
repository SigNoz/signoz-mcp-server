"""Result size budget on the raw searches against the pinned release.

A small maxResultBytes ends each page on a whole row, and following
pagination.nextOffset returns every matching row exactly once, in order.
"""

from collections.abc import Callable

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json
from fixtures.telemetry import seed_logs, seed_traces, wait_for

SEEDED = 12
SMALL_BUDGET = 2500
LARGE_BUDGET = 200000


def _text_bytes(result: dict) -> int:
    """UTF-8 bytes of every text block, the number the budget bounds."""
    return sum(
        len(block.get("text", "").encode("utf-8")) for block in result.get("content", []) if block.get("type") == "text"
    )


def _rows(body: dict) -> list[dict]:
    rows = []
    for result in body["data"]["data"]["results"]:
        rows.extend(result.get("rows") or [])
    return rows


def _walk_pages(search: Callable[..., dict], full: list[dict]) -> int:
    """Follow nextOffset under SMALL_BUDGET; every page must be a whole-row slice of `full`."""
    offset, pages, collected = 0, 0, []
    while True:
        result = assert_tool_ok(search(offset=str(offset), maxResultBytes=str(SMALL_BUDGET)))
        assert _text_bytes(result) <= SMALL_BUDGET, (
            f"page at offset {offset} is over its budget: {_text_bytes(result)} bytes"
        )
        body = first_block_json(result)
        rows, pagination = _rows(body), body["pagination"]
        assert rows, f"a page with rows left must return at least one: {pagination}"
        assert pagination["returned"] == len(rows) and pagination["offset"] == offset, pagination
        assert rows == full[offset : offset + len(rows)], f"page at offset {offset} must be whole rows in order"
        assert "shortenedRows" not in pagination, f"no seeded row is larger than the budget: {pagination}"
        collected.extend(rows)
        pages += 1
        if not pagination["hasMore"]:
            assert pagination["nextOffset"] == -1 and not pagination["truncated"], pagination
            break
        assert pagination["truncated"], (
            f"with limit {pagination['limit']} only the budget can end this page: {pagination}"
        )
        assert pagination["nextOffset"] == offset + len(rows), pagination
        offset = pagination["nextOffset"]
        assert pages <= SEEDED, "paging must make progress"
    assert collected == full, "following nextOffset must return every row exactly once"
    return pages


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

    assert _walk_pages(search, full) > 1, f"a {SMALL_BUDGET}-byte budget must split {SEEDED} rows into several pages"


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

    assert _walk_pages(search, full) > 1, f"a {SMALL_BUDGET}-byte budget must split {SEEDED} rows into several pages"
