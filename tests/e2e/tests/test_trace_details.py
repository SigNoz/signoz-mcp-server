import json

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block, note_blocks, result_code
from fixtures.telemetry import seed_trace_tree, wait_for_trace_details

# A page holds about 100 KB of spans; the summary and envelope ride on top.
MAX_PAGE_BYTES = 130_000


def test_trace_details_summary_describes_the_trace(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    service = f"mcp-e2e-{test_id}"
    seeded = seed_trace_tree(service, chain_depth=8, sibling_count=3)

    summary = wait_for_trace_details(mcp_client, seeded["trace_id"], seeded["count"])["summary"]

    assert summary["complete"] is True
    assert summary["services"] == [{"name": service, "spans": seeded["count"], "errorSpans": 1}]
    assert [span["span_id"] for span in summary["errorSpans"]] == [seeded["deep"]]
    assert (summary["rootService"], summary["rootOperation"]) == (service, "e2e-root")
    assert len(summary["slowestSpans"]) == 5


def test_trace_details_include_spans_false_returns_only_the_summary(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=2)

    body = wait_for_trace_details(mcp_client, seeded["trace_id"], seeded["count"])

    assert set(body) == {"traceId", "webUrl", "summary"}, f"summary-only response carried {sorted(body)}"
    assert "/trace/" in body["webUrl"]


def test_trace_details_pages_cover_every_span_once_in_tree_order(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=8, sibling_count=110, padding_chars=1500)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])

    pages, cursor = [], None
    while True:
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_trace_details",
                {"searchContext": f"trace {trace_id}", "traceId": trace_id, **({"cursor": cursor} if cursor else {})},
            )
        )
        pages.append((len(first_text_block(result)), json.loads(first_text_block(result)), note_blocks(result)))
        cursor = pages[-1][1]["pagination"].get("nextCursor")
        if not pages[-1][1]["pagination"]["hasMore"]:
            break

    ids = [span["span_id"] for _, page, _ in pages for span in page["spans"]]
    position = {span_id: index for index, span_id in enumerate(ids)}
    assert len(pages) >= 2, "the padded trace should need more than one page"
    assert all(size <= MAX_PAGE_BYTES for size, _, _ in pages), [size for size, _, _ in pages]
    assert all(f'cursor="{page["pagination"]["nextCursor"]}"' in notes for _, page, notes in pages[:-1])
    assert len(ids) == len(set(ids)) == seeded["count"], "pages must cover every span exactly once"
    assert all(
        position[span["parent_span_id"]] < position[span["span_id"]]
        for _, page, _ in pages
        for span in page["spans"]
        if span.get("parent_span_id")
    ), "parents must come before children"


def test_trace_details_lists_each_resource_once(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    service = f"mcp-e2e-{test_id}"
    seeded = seed_trace_tree(service, chain_depth=2, sibling_count=3)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])

    body = json.loads(
        first_text_block(
            assert_tool_ok(
                mcp_client.call_tool("signoz_get_trace_details", {"searchContext": "trace", "traceId": trace_id})
            )
        )
    )

    assert [resource["attributes"]["service.name"] for resource in body["resources"]] == [service]
    assert {span["resourceId"] for span in body["spans"]} == {body["resources"][0]["resourceId"]}
    assert all("resource" not in span and "trace_id" not in span for span in body["spans"])


def test_trace_details_keeps_three_events_per_span_exceptions_first(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=2, deep_span_events=5)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])

    body = json.loads(
        first_text_block(
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_trace_details",
                    {"searchContext": "trace", "traceId": trace_id, "spanId": seeded["deep"]},
                )
            )
        )
    )
    deep = body["spans"][0]

    assert [event["name"] for event in deep["events"]] == ["exception", "e2e-event-0", "e2e-event-1"]
    assert deep["eventsOmitted"] == 2


def test_trace_details_shortens_long_values(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=2, deep_span_long_value_chars=5000)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_get_trace_details", {"searchContext": "trace", "traceId": trace_id, "spanId": seeded["deep"]}
        )
    )

    assert json.loads(first_text_block(result))["spans"][0]["attributes"]["e2e.long"] == "L" * 2000 + "…[+3000 chars]"
    assert "signoz_search_traces" in note_blocks(result), "a trimmed page must say where to read the full value"


def test_trace_details_span_focus_pages_the_subtree(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=8, sibling_count=3)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])
    # chain[3] sits at depth 3; its subtree is the rest of the chain.
    target = seeded["chain"][3]

    body = json.loads(
        first_text_block(
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_trace_details", {"searchContext": "trace", "traceId": trace_id, "spanId": target}
                )
            )
        )
    )

    assert [span["span_id"] for span in body["focus"]["ancestors"]] == seeded["chain"][:3]
    assert [span["span_id"] for span in body["spans"]] == seeded["chain"][3:]


def test_trace_details_span_focus_lists_root_and_nearest_five_ancestors(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=8)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])

    focus = json.loads(
        first_text_block(
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_trace_details",
                    {"searchContext": "trace", "traceId": trace_id, "spanId": seeded["deep"], "includeSpans": False},
                )
            )
        )
    )["focus"]

    # The deep span has 8 ancestors: the root plus the nearest 5 are listed.
    assert [span["span_id"] for span in focus["ancestors"]] == [seeded["root"], *seeded["chain"][3:8]]
    assert focus["ancestorsOmitted"] == 2


@pytest.mark.parametrize("argument", ["spanId", "cursor"], ids=["unknown_span", "unknown_cursor"])
def test_trace_details_rejects_span_ids_outside_the_trace(
    mcp_client: MCPClient, test_id: str, telemetry: None, argument: str
) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=1)
    trace_id = seeded["trace_id"]
    wait_for_trace_details(mcp_client, trace_id, seeded["count"])

    result = mcp_client.call_tool(
        "signoz_get_trace_details", {"searchContext": "trace", "traceId": trace_id, argument: "0000000000000000"}
    )

    assert result_code(result) == "VALIDATION_FAILED", first_text_block(result)
    assert f'"{argument}"' in first_text_block(result)


def test_trace_details_finds_a_trace_older_than_any_time_window(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    seeded = seed_trace_tree(f"mcp-e2e-{test_id}", chain_depth=2, age_seconds=8 * 3600)

    body = wait_for_trace_details(mcp_client, seeded["trace_id"], seeded["count"])

    assert body["summary"]["totalSpans"] == seeded["count"]


def test_trace_details_unknown_trace_is_not_found(mcp_client: MCPClient) -> None:
    result = mcp_client.call_tool(
        "signoz_get_trace_details", {"searchContext": "trace", "traceId": "0123456789abcdef0123456789abcdef"}
    )

    assert result_code(result) == "NOT_FOUND", first_text_block(result)
