"""AI trace queries against real SigNoz: queryType, requestType, and orderBy on the trace tools.

These tests pin what the trace tool descriptions and signoz://ai/query-builder-guide
teach against the pinned release. One service holds an expensive looping AI
trace, a cheap AI trace, and a plain trace, so ordering, counting, and exclusion
assertions can fail: builder_ai_query keeps only AI traces, per-trace filters
and order keys pick between the two AI traces, per-trace order keys are rejected
on span rows, plain trace rows come back slowest first, per-trace aggregates
agree with the trace rows, and a builder_ai_query runs through
signoz_execute_builder_query and round-trips in an alert and a dashboard panel.
"""

import time
from datetime import datetime
from typing import NamedTuple

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok, first_json
from fixtures.results import first_block_json, first_text_block, note_blocks, query_range_rows, result_code
from fixtures.telemetry import PLAIN_AI_CHILD_SPAN, seed_ai_trace, seed_traces, wait_for

# gen_ai spans of seed_ai_trace: an agent root span plus the LLM and tool calls.
# Each trace also has one plain child span, PLAIN_AI_CHILD_SPAN.
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


def test_ai_span_rows_are_the_gen_ai_spans_of_ai_traces_newest_first(mcp_client: MCPClient, seeded: Seeded) -> None:
    """The queryType description says builder_ai_query span rows are only the LLM, tool, and agent spans."""
    rows = ai_rows(mcp_client, seeded, "raw")
    assert len(rows) == LOOPING_SPAN_COUNT + CHEAP_SPAN_COUNT, f"expected the gen_ai spans of both AI traces: {rows}"
    assert PLAIN_AI_CHILD_SPAN not in {r["name"] for r in rows}, "builder_ai_query kept a span without gen_ai keys"
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


# SigNoz names a scalar aggregation column by its index, not its expression.
VALUE = "__result_0"


def scalar_rows(result: dict) -> list[dict]:
    """Rows of a scalar query_range result, keyed by column name."""
    payload = first_block_json(result)
    rows = []
    for table in payload["data"]["data"]["results"]:
        names = [column["name"] for column in table.get("columns") or []]
        rows.extend(dict(zip(names, values, strict=True)) for values in table.get("data") or [])
    return rows


def aggregate(client: MCPClient, seeded: Seeded, **arguments: str) -> list[dict]:
    result = client.call_tool(
        "signoz_aggregate_traces",
        {"searchContext": "AI trace statistics", "service": seeded.service, "timeRange": "1h", **arguments},
    )
    return scalar_rows(assert_tool_ok(result))


def only_value(rows: list[dict]) -> object:
    assert len(rows) == 1, f"expected one aggregate row: {rows}"
    return rows[0][VALUE]


def trace_row_total(client: MCPClient, seeded: Seeded, column: str) -> float:
    return sum(float(r[column]) for r in ai_rows(client, seeded, "trace"))


def test_ai_aggregate_counts_only_ai_traces(mcp_client: MCPClient, seeded: Seeded) -> None:
    """count_distinct(trace_id) counts AI traces with builder_ai_query, as the queryType description teaches."""
    ai = aggregate(mcp_client, seeded, queryType="builder_ai_query", aggregations="count_distinct(trace_id)")
    assert float(only_value(ai)) == 2, ai
    every = aggregate(mcp_client, seeded, aggregations="count_distinct(trace_id)")
    assert float(only_value(every)) == 3, every


def test_ai_aggregate_per_trace_tokens_match_trace_rows(mcp_client: MCPClient, seeded: Seeded) -> None:
    total = aggregate(mcp_client, seeded, queryType="builder_ai_query", aggregations="sum(trace.total_tokens)")
    assert float(only_value(total)) == trace_row_total(mcp_client, seeded, "total_tokens")


def test_ai_tokens_per_call_by_model_match_per_trace_tokens(mcp_client: MCPClient, seeded: Seeded) -> None:
    """Per-call totals by model (builder_query on a gen_ai.* span attribute) add up to the per-trace columns.

    Spans without the model attribute fall in a null group, as the guide says.
    """
    by_model = aggregate(
        mcp_client, seeded, aggregations="sum(gen_ai.usage.input_tokens)", groupBy="gen_ai.request.model"
    )
    totals = {row["gen_ai.request.model"]: row[VALUE] for row in by_model}
    assert set(totals) == {"gpt-4o-mini", None}, f"expected the model group and a null group: {by_model}"
    assert float(totals["gpt-4o-mini"]) == trace_row_total(mcp_client, seeded, "input_tokens")


def test_ai_filter_rejects_or_across_trace_and_span_levels(mcp_client: MCPClient, seeded: Seeded) -> None:
    """The guide says SigNoz rejects OR across per-trace and span-level conditions; AND works."""
    filters = {"queryType": "builder_ai_query", "aggregations": "count_distinct(trace_id)"}
    rejected = mcp_client.call_tool(
        "signoz_aggregate_traces",
        {
            "searchContext": "looping or failing AI traces",
            "service": seeded.service,
            "timeRange": "1h",
            "filter": "trace.tool_call_count > 10 OR has_error = true",
            **filters,
        },
    )
    assert rejected.get("isError", False), f"OR across levels accepted: {first_text_block(rejected)[:300]}"
    assert result_code(rejected) == "VALIDATION_FAILED"
    combined = aggregate(mcp_client, seeded, filter="trace.tool_call_count > 10 AND has_error = true", **filters)
    assert float(only_value(combined)) == 1, combined


def ai_spec(seeded: Seeded, expression: str) -> dict:
    return {
        "name": "A",
        "signal": "traces",
        "aggregations": [{"expression": expression}],
        "filter": {"expression": f"service.name = '{seeded.service}'"},
    }


def scalar_ai_request(spec: dict) -> dict:
    now = int(time.time() * 1000)
    return {
        "schemaVersion": "v1",
        "start": now - 60 * 60 * 1000,
        "end": now,
        "requestType": "scalar",
        "compositeQuery": {"queries": [{"type": "builder_ai_query", "spec": spec}]},
    }


def test_execute_builder_query_bounds_builder_ai_query_without_an_order(mcp_client: MCPClient, seeded: Seeded) -> None:
    query = scalar_ai_request(ai_spec(seeded, "count_distinct(trace_id)"))
    result = assert_tool_ok(
        mcp_client.call_tool("signoz_execute_builder_query", {"searchContext": "count AI traces", "query": query})
    )
    assert float(only_value(scalar_rows(result))) == 2
    notes = note_blocks(result)
    assert 'query "A": limit=100 (request-type default)' in notes, f"default limit not reported: {notes!r}"
    assert "order=" not in notes, f"an order was inserted into a builder_ai_query: {notes!r}"


def test_ai_alert_round_trip(mcp_client: MCPClient, seeded: Seeded, test_id: str) -> None:
    name = f"mcp-e2e-{test_id}"
    spec = {**ai_spec(seeded, "sum(trace.estimated_total_cost)"), "stepInterval": 60}
    rule = {
        "alert": name,
        "alertType": "AI_TRACES_BASED_ALERT",
        "ruleType": "threshold_rule",
        "schemaVersion": "v2alpha1",
        "labels": {"severity": "warning"},
        "condition": {
            "compositeQuery": {
                "queryType": "builder",
                "panelType": "graph",
                "queries": [{"type": "builder_ai_query", "spec": spec}],
            },
            "selectedQueryName": "A",
            "thresholds": {
                "kind": "basic",
                "spec": [{"name": "warning", "op": "above", "matchType": "at_least_once", "target": 99}],
            },
        },
        "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
        "notificationSettings": {"usePolicy": True},
    }
    created = assert_tool_ok(mcp_client.call_tool("signoz_create_alert", {"searchContext": "alert on AI cost", **rule}))
    rule_id = (first_block_json(created).get("data") or {}).get("id")
    assert isinstance(rule_id, str) and rule_id, f"create_alert returned no rule id: {first_text_block(created)[:300]}"
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool("signoz_get_alert", {"searchContext": f"get rule {name}", "id": rule_id})
        )
        data = first_block_json(fetched)["data"]
        assert data["alertType"] == "AI_TRACES_BASED_ALERT", f"alert type did not round-trip: {data['alertType']}"
        queries = data["condition"]["compositeQuery"]["queries"]
        assert queries[0]["type"] == "builder_ai_query", f"envelope type did not round-trip: {queries}"
        assert queries[0]["spec"]["aggregations"][0]["expression"] == "sum(trace.estimated_total_cost)"
    finally:
        assert_tool_ok(mcp_client.call_tool("signoz_delete_alert", {"searchContext": "cleanup", "id": rule_id}))
    gone = mcp_client.call_tool("signoz_get_alert", {"searchContext": f"confirm {name} is gone", "id": rule_id})
    assert gone.get("isError", False), f"alert {rule_id} remained after cleanup"


def test_ai_dashboard_panel_round_trips_and_runs(mcp_client: MCPClient, seeded: Seeded, test_id: str) -> None:
    title = f"mcp-e2e-ai-{test_id}"
    plugin = {"kind": "signoz/AIBuilderQuery", "spec": ai_spec(seeded, "count_distinct(trace_id)")}
    dashboard = {
        "searchContext": f"create a dashboard named {title} with an AI trace count panel",
        "schemaVersion": "v6",
        "tags": [],
        "spec": {
            "display": {"name": title},
            "variables": [],
            "panels": {
                "traces": {
                    "kind": "Panel",
                    "spec": {
                        "display": {"name": "AI traces"},
                        "links": [],
                        "plugin": {"kind": "signoz/TablePanel", "spec": {}},
                        "queries": [{"kind": "scalar", "spec": {"name": "A", "plugin": plugin}}],
                    },
                }
            },
            "layouts": [
                {
                    "kind": "Grid",
                    "spec": {
                        "items": [
                            {"x": 0, "y": 0, "width": 12, "height": 6, "content": {"$ref": "#/spec/panels/traces"}}
                        ]
                    },
                }
            ],
        },
    }
    created = assert_tool_ok(mcp_client.call_tool("signoz_create_dashboard", dashboard))
    payload = first_json(created)
    data = payload.get("data", payload)
    dashboard_id = data.get("id") or data.get("uuid")
    assert isinstance(dashboard_id, str) and dashboard_id, f"no dashboard id in {payload}"
    try:
        fetched = first_json(
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_dashboard", {"searchContext": f"get dashboard {dashboard_id}", "id": dashboard_id}
                )
            )
        )
        saved = fetched.get("data", fetched)["spec"]["panels"]["traces"]["spec"]["queries"][0]["spec"]["plugin"]
        assert saved["kind"] == "signoz/AIBuilderQuery", f"plugin kind did not round-trip: {saved}"

        # The saved panel spec runs as a builder_ai_query, so the panel shows the AI trace count.
        query = scalar_ai_request(saved["spec"])
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_execute_builder_query", {"searchContext": "run the saved panel", "query": query}
            )
        )
        assert float(only_value(scalar_rows(result))) == 2
    finally:
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_delete_dashboard", {"searchContext": f"cleanup dashboard {dashboard_id}", "id": dashboard_id}
            )
        )
    gone = mcp_client.call_tool(
        "signoz_get_dashboard", {"searchContext": f"confirm dashboard {dashboard_id} is gone", "id": dashboard_id}
    )
    assert gone.get("isError", False), f"dashboard {dashboard_id} remained after cleanup"
