"""AI trace search against real SigNoz: queryType, requestType, and orderBy on signoz_search_traces.

These tests pin what the search descriptions teach against the pinned release.
One service holds an expensive looping AI trace, a cheap AI trace, and a plain
trace, so ordering and exclusion assertions can fail: builder_ai_query keeps
only AI traces, per-trace filters and order keys pick between the two AI
traces, per-trace order keys are rejected on span rows, and plain trace rows
come back slowest first.
"""

import time
from datetime import datetime
from typing import NamedTuple

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block, query_range_rows, result_code
from fixtures.telemetry import seed_ai_trace, seed_traces, wait_for

# seed_ai_trace spans: an agent root span plus the LLM and tool calls.
LOOPING_SPAN_COUNT = 1 + 2 + 12
CHEAP_SPAN_COUNT = 1 + 1 + 2

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


class Seeded(NamedTuple):
    service: str
    looping: str  # two LLM calls and twelve tool calls: the expensive, slowest trace
    cheap: str  # one LLM call and two tool calls
    plain: str  # one span, no gen_ai attributes


@pytest.fixture(scope="module")
def seeded(mcp_client: MCPClient, telemetry: None) -> Seeded:
    service = f"mcp-e2e-ai-{int(time.time())}"
    looping = seed_ai_trace(service)
    cheap = seed_ai_trace(service, llm_calls=1, tool_calls=2)
    (plain,) = seed_traces(service)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "requestType": "trace", "service": service, "timeRange": "1h"},
        )
        if result.get("isError", False):
            return False
        return {r.get("trace_id") for r in query_range_rows(first_text_block(result))} >= {looping, cheap, plain}

    wait_for(visible, f"seeded traces of {service} visible")
    return Seeded(service, looping, cheap, plain)


def search_rows(client: MCPClient, arguments: dict) -> list[dict]:
    result = assert_tool_ok(client.call_tool("signoz_search_traces", {"timeRange": "1h", "limit": "50", **arguments}))
    return query_range_rows(first_text_block(result))


def ai_rows(client: MCPClient, seeded: Seeded, request_type: str, **arguments: str) -> list[dict]:
    return search_rows(
        client,
        {
            "searchContext": "AI traces",
            "queryType": "builder_ai_query",
            "requestType": request_type,
            "service": seeded.service,
            **arguments,
        },
    )


def test_ai_span_rows_cover_only_ai_traces_newest_first(mcp_client: MCPClient, seeded: Seeded) -> None:
    rows = ai_rows(mcp_client, seeded, "raw")
    assert len(rows) == LOOPING_SPAN_COUNT + CHEAP_SPAN_COUNT, f"expected the spans of both AI traces: {rows}"
    assert {r["trace_id"] for r in rows} == {seeded.looping, seeded.cheap}, "builder_ai_query kept the plain trace"
    timestamps = [datetime.fromisoformat(r["timestamp"]) for r in rows]
    assert timestamps == sorted(timestamps, reverse=True), "span rows are not newest first"


def test_ai_span_rows_filter_on_per_trace_counts(mcp_client: MCPClient, seeded: Seeded) -> None:
    looping = ai_rows(mcp_client, seeded, "raw", filter="trace.tool_call_count > 10 AND trace.distinct_tool_count < 3")
    assert {r["trace_id"] for r in looping} == {seeded.looping}, looping
    assert not ai_rows(mcp_client, seeded, "raw", filter="trace.distinct_tool_count > 5")


def test_span_rows_reject_per_trace_order_keys(mcp_client: MCPClient, seeded: Seeded) -> None:
    """The orderBy description says SigNoz rejects trace. keys on span rows; if that changes, so must the description."""
    result = mcp_client.call_tool(
        "signoz_search_traces",
        {
            "searchContext": "AI spans ordered by trace tokens",
            "queryType": "builder_ai_query",
            "orderBy": "trace.total_tokens desc",
            "service": seeded.service,
            "timeRange": "1h",
        },
    )
    assert result.get("isError", False), f"trace. order key unexpectedly accepted: {first_text_block(result)[:400]}"
    assert result_code(result) == "VALIDATION_FAILED"


def test_builder_query_returns_no_rows_for_per_trace_keys(mcp_client: MCPClient, seeded: Seeded) -> None:
    """The queryType description says builder_query returns no rows, not an error, for trace. keys."""
    rows = search_rows(
        mcp_client,
        {
            "searchContext": "spans with many tool calls",
            "filter": "trace.tool_call_count > 10",
            "service": seeded.service,
        },
    )
    assert rows == [], f"builder_query matched rows on a per-trace key: {rows}"


def test_span_rows_order_by_duration(mcp_client: MCPClient, seeded: Seeded) -> None:
    rows = search_rows(
        mcp_client,
        {"searchContext": "slowest spans first", "orderBy": "duration_nano desc", "service": seeded.service},
    )
    durations = [int(r["duration_nano"]) for r in rows]
    assert len(set(durations)) > 2, f"seeded spans should have distinct durations: {durations}"
    assert durations == sorted(durations, reverse=True), f"span rows not ordered by duration_nano desc: {durations}"


def test_ai_trace_rows_carry_per_trace_columns(mcp_client: MCPClient, seeded: Seeded) -> None:
    rows = {r["trace_id"]: r for r in ai_rows(mcp_client, seeded, "trace")}
    assert set(rows) == {seeded.looping, seeded.cheap}, f"expected one row per AI trace: {sorted(rows)}"
    row = rows[seeded.looping]
    assert PER_TRACE_COLUMNS <= set(row), f"AI trace row lacks per-trace columns: {sorted(row)}"
    assert float(row["llm_call_count"]) == 2
    assert float(row["tool_call_count"]) == 12
    assert float(row["distinct_tool_count"]) == 2
    assert float(row["total_tokens"]) == float(row["input_tokens"]) + float(row["output_tokens"])
    assert "/trace/" in str(row.get("webUrl")), f"AI trace row lacks a trace deep link: {row.get('webUrl')!r}"


def test_ai_trace_rows_order_by_per_trace_tokens(mcp_client: MCPClient, seeded: Seeded) -> None:
    """Orders on token totals the fixture controls (2,400 vs 1,200); seeded cost reads back null."""
    most_tokens_first = ai_rows(mcp_client, seeded, "trace", orderBy="trace.total_tokens desc")
    assert [r["trace_id"] for r in most_tokens_first] == [seeded.looping, seeded.cheap]
    fewest_tokens_first = ai_rows(mcp_client, seeded, "trace", orderBy="trace.total_tokens asc")
    assert [r["trace_id"] for r in fewest_tokens_first] == [seeded.cheap, seeded.looping]


def test_plain_trace_rows_come_back_slowest_first(mcp_client: MCPClient, seeded: Seeded) -> None:
    rows = search_rows(
        mcp_client, {"searchContext": "slowest traces", "requestType": "trace", "service": seeded.service}
    )
    assert [r["trace_id"] for r in rows] == [seeded.looping, seeded.cheap, seeded.plain]
    assert {"trace_id", "name", "service.name", "duration_nano", "span_count"} <= set(rows[0]), sorted(rows[0])
