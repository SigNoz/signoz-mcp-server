"""AI trace search against real SigNoz: queryType, requestType, and orderBy on signoz_search_traces.

The MCP copies queryType into the query envelope type, picks the row shape from
requestType, and forwards everything else for SigNoz to judge. These tests pin
what the search descriptions teach against the pinned release: span rows for
both query types, per-trace filters, per-trace order keys rejected on span rows
but accepted on AI trace rows, AI trace rows with their per-trace columns, and
plain trace rows slowest first.
"""

import time

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block, query_range_rows, result_code
from fixtures.telemetry import seed_ai_trace, wait_for

# seed_ai_trace: an agent root span, two LLM calls, and twelve tool calls.
SEEDED_SPAN_COUNT = 15

PER_TRACE_COLUMNS = {
    "trace_id",
    "llm_call_count",
    "tool_call_count",
    "distinct_tool_count",
    "input_tokens",
    "output_tokens",
    "total_tokens",
    "estimated_total_cost",
    "max_llm_duration_nano",
}


@pytest.fixture(scope="module")
def ai_trace(mcp_client: MCPClient, telemetry: None) -> tuple[str, str]:
    """One seeded agent trace, visible to builder_ai_query; returns (service, trace_id)."""
    service = f"mcp-e2e-ai-{int(time.time())}"
    trace_id = seed_ai_trace(service)

    def visible() -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {
                "searchContext": f"spans of AI traces from {service}",
                "queryType": "builder_ai_query",
                "service": service,
                "timeRange": "1h",
            },
        )
        if result.get("isError", False):
            return []
        return [r for r in query_range_rows(first_text_block(result)) if r.get("trace_id") == trace_id]

    wait_for(visible, f"seeded AI trace {trace_id} visible to builder_ai_query")
    return service, trace_id


def search_rows(client: MCPClient, arguments: dict) -> list[dict]:
    result = assert_tool_ok(client.call_tool("signoz_search_traces", {"timeRange": "1h", **arguments}))
    return query_range_rows(first_text_block(result))


def test_ai_search_returns_span_rows_newest_first(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, trace_id = ai_trace
    rows = search_rows(
        mcp_client,
        {
            "searchContext": "spans of recent AI traces",
            "queryType": "builder_ai_query",
            "service": service,
            "limit": "50",
        },
    )
    assert len(rows) == SEEDED_SPAN_COUNT, f"builder_ai_query search must return span rows, got {len(rows)}: {rows}"
    assert all(r.get("span_id") and r["trace_id"] == trace_id for r in rows), rows
    timestamps = [r["timestamp"] for r in rows]
    assert timestamps == sorted(timestamps, reverse=True), f"span rows not newest first: {timestamps}"


def test_ai_search_filters_on_per_trace_counts(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, trace_id = ai_trace
    looping = search_rows(
        mcp_client,
        {
            "searchContext": "spans of agents stuck in a loop",
            "queryType": "builder_ai_query",
            "filter": "trace.tool_call_count > 10 AND trace.distinct_tool_count < 3",
            "service": service,
            "limit": "50",
        },
    )
    assert {r["trace_id"] for r in looping} == {trace_id}, looping
    many_tools = search_rows(
        mcp_client,
        {
            "searchContext": "spans of agents with many distinct tools",
            "queryType": "builder_ai_query",
            "filter": "trace.distinct_tool_count > 5",
            "service": service,
        },
    )
    assert not many_tools, f"trace.distinct_tool_count > 5 matched the two-tool trace: {many_tools}"


def test_ai_search_rejects_per_trace_order_keys(mcp_client: MCPClient, ai_trace: tuple) -> None:
    """The orderBy description says SigNoz rejects trace. keys on span rows; if that changes, so must the description."""
    service, _ = ai_trace
    result = mcp_client.call_tool(
        "signoz_search_traces",
        {
            "searchContext": "AI spans ordered by trace tokens",
            "queryType": "builder_ai_query",
            "orderBy": "trace.total_tokens desc",
            "service": service,
            "timeRange": "1h",
        },
    )
    assert result.get("isError", False), f"trace. order key unexpectedly accepted: {first_text_block(result)[:400]}"
    assert result_code(result) == "VALIDATION_FAILED"


def test_builder_query_returns_no_rows_for_per_trace_keys(mcp_client: MCPClient, ai_trace: tuple) -> None:
    """The queryType description says builder_query returns no rows, not an error, for trace. keys."""
    service, _ = ai_trace
    rows = search_rows(
        mcp_client,
        {"searchContext": "spans with many tool calls", "filter": "trace.tool_call_count > 10", "service": service},
    )
    assert rows == [], f"builder_query matched rows on a per-trace key: {rows}"


def test_builder_query_order_by_reaches_span_rows(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, _ = ai_trace
    rows = search_rows(
        mcp_client,
        {"searchContext": "slowest spans first", "orderBy": "duration_nano desc", "limit": "20", "service": service},
    )
    assert len(rows) > 1, f"expected the seeded agent spans as span rows, got {rows}"
    durations = [int(r["duration_nano"]) for r in rows]
    assert durations == sorted(durations, reverse=True), f"span rows not ordered by duration_nano desc: {durations}"


def test_failing_tool_calls_are_span_rows(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, trace_id = ai_trace
    rows = search_rows(
        mcp_client,
        {
            "searchContext": "failing tool calls",
            "filter": "gen_ai.tool.name EXISTS AND has_error = true",
            "selectFields": ["gen_ai.tool.name"],
            "service": service,
        },
    )
    assert len(rows) == 1, f"expected the one failing tool call, got {rows}"
    assert rows[0]["trace_id"] == trace_id
    assert rows[0]["gen_ai.tool.name"] == "read_file"


def test_ai_trace_rows_carry_per_trace_columns(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, trace_id = ai_trace
    rows = search_rows(
        mcp_client,
        {
            "searchContext": "list recent AI traces",
            "queryType": "builder_ai_query",
            "requestType": "trace",
            "service": service,
        },
    )
    assert len(rows) == 1, f"requestType=trace must return one row per AI trace, got {len(rows)}: {rows}"
    row = rows[0]
    assert row["trace_id"] == trace_id
    assert PER_TRACE_COLUMNS <= set(row), f"AI trace row lacks per-trace columns: {sorted(row)}"
    assert float(row["llm_call_count"]) == 2
    assert float(row["tool_call_count"]) == 12
    assert float(row["distinct_tool_count"]) == 2
    assert float(row["total_tokens"]) == float(row["input_tokens"]) + float(row["output_tokens"])
    assert "/trace/" in str(row.get("webUrl")), f"AI trace row lacks a trace deep link: {row.get('webUrl')!r}"


def test_most_expensive_ai_traces_order_by_per_trace_cost(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, trace_id = ai_trace
    rows = search_rows(
        mcp_client,
        {
            "searchContext": "most expensive AI traces",
            "queryType": "builder_ai_query",
            "requestType": "trace",
            "orderBy": "trace.estimated_total_cost desc",
            "limit": "10",
            "service": service,
        },
    )
    assert [r["trace_id"] for r in rows] == [trace_id]
    costs = [float(r["estimated_total_cost"] or 0) for r in rows]
    assert costs == sorted(costs, reverse=True)


def test_plain_trace_rows_list_one_row_per_trace(mcp_client: MCPClient, ai_trace: tuple) -> None:
    service, trace_id = ai_trace
    rows = search_rows(
        mcp_client,
        {"searchContext": "slowest traces", "requestType": "trace", "service": service},
    )
    assert [r["trace_id"] for r in rows] == [trace_id], f"expected one row for the seeded trace: {rows}"
    assert {"trace_id", "name", "service.name", "duration_nano", "span_count"} <= set(rows[0]), sorted(rows[0])
    assert int(rows[0]["span_count"]) == SEEDED_SPAN_COUNT
