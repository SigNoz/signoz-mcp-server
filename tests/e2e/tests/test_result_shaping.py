"""Result shaping on the query tools against the pinned release.

Each check teaches a claim the tool descriptions make about fields SigNoz
accepts: several aggregations and a having clause on the aggregate tools,
limit/orderBy/having and typed formulaQueries on query_metrics, and
selectFields and orderBy on the raw searches.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import (
    aggregate_columns_and_row_count,
    first_block_json,
    first_text_block,
    note_blocks,
    query_range_rows,
)
from fixtures.telemetry import seed_logs, seed_metrics, seed_traces, wait_for

# The default search_logs columns. A drift here changes what every caller gets
# without naming selectFields.
CORE_LOG_FIELDS = {"timestamp", "id", "body", "severity_text", "trace_id", "span_id"}


def _aggregate_logs(mcp_client: MCPClient, **arguments) -> dict:
    return mcp_client.call_tool(
        "signoz_aggregate_logs",
        {"searchContext": "result shaping on aggregate_logs", "timeRange": "1h", **arguments},
    )


def test_aggregate_logs_several_aggregations_and_having(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Two expressions come back as __result_0 and __result_1; having drops the small group."""
    big, small = f"mcp-e2e-big-{test_id}", f"mcp-e2e-small-{test_id}"
    seed_logs(big, f"marker-{test_id}", count=3)
    seed_logs(small, f"marker-{test_id}", count=1)
    scope = f"service.name IN ('{big}', '{small}')"

    def both_visible() -> bool:
        result = _aggregate_logs(mcp_client, aggregations="count()", groupBy="service.name", filter=scope)
        if result.get("isError", False):
            return False
        _, rows = aggregate_columns_and_row_count(first_text_block(result))
        return rows == 2

    wait_for(both_visible, "both seeded services visible through aggregate_logs")

    result = assert_tool_ok(
        _aggregate_logs(
            mcp_client,
            aggregations=["count()", "count_distinct(severity_text)"],
            groupBy=["service.name"],
            filter=scope,
            having="count() > 1",
        )
    )
    columns, rows = aggregate_columns_and_row_count(first_text_block(result))
    assert "__result_0" in columns and "__result_1" in columns, f"both aggregations must be returned: {columns}"
    assert rows == 1, f"having must keep only the service with more than one log: {first_text_block(result)[:600]}"
    assert big in first_text_block(result) and small not in first_text_block(result)


def test_aggregate_traces_functions_change_time_series_and_not_scalars(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """clampMax caps every time_series value; on a scalar request the tool says functions did nothing."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=3)

    def aggregate(**arguments) -> dict:
        return mcp_client.call_tool(
            "signoz_aggregate_traces",
            {
                "searchContext": "span count over time",
                "service": service,
                "aggregations": "count()",
                "timeRange": "1h",
                **arguments,
            },
        )

    def series_values(result: dict) -> list[float]:
        values = []
        for query_result in first_block_json(result)["data"]["data"]["results"]:
            for bucket in query_result.get("aggregations") or []:
                for series in bucket.get("series") or []:
                    values.extend(point["value"] for point in series.get("values") or [])
        return values

    def all_seeded() -> bool:
        result = aggregate(requestType="time_series")
        return not result.get("isError", False) and sum(series_values(result)) == 3

    wait_for(all_seeded, f"all three seeded spans for {service} counted through aggregate_traces")

    raw_values = series_values(assert_tool_ok(aggregate(requestType="time_series")))
    clamped = assert_tool_ok(
        aggregate(requestType="time_series", functions=[{"name": "clampMax", "args": [{"value": 1}]}])
    )
    clamped_values = series_values(clamped)
    assert clamped_values and max(clamped_values) == 1 and max(raw_values) > 1, (
        f"clampMax 1 must cap every value: raw {raw_values}, clamped {clamped_values}"
    )

    scalar = assert_tool_ok(aggregate(functions=[{"name": "clampMax", "args": [{"value": 1}]}]))
    scalar_rows = [row for r in first_block_json(scalar)["data"]["data"]["results"] for row in r.get("data") or []]
    assert [row[-1] for row in scalar_rows] == [3], f"the scalar count must come back unchanged: {scalar_rows}"
    assert "functions were not applied" in note_blocks(scalar), (
        f"scalar results must say functions did nothing: {note_blocks(scalar)!r}"
    )


def test_search_logs_select_fields_and_order(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Rows default to the core log columns, selectFields adds to them, and orderBy flips the order."""
    service = f"mcp-e2e-{test_id}"
    seed_logs(service, f"marker-{test_id}", count=3)

    def search(**arguments) -> dict:
        return mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "seeded logs", "service": service, "timeRange": "1h", **arguments},
        )

    def all_visible() -> bool:
        result = search(limit="10")
        return not result.get("isError", False) and len(query_range_rows(first_text_block(result))) == 3

    wait_for(all_visible, f"all three seeded logs for {service} visible")

    core_rows = query_range_rows(first_text_block(assert_tool_ok(search(limit="10"))))
    for row in core_rows:
        assert set(row) == CORE_LOG_FIELDS, f"default row keys drifted from the core set: {sorted(row)}"

    added = query_range_rows(
        first_text_block(assert_tool_ok(search(limit="10", selectFields=["service.name", "e2e.marker"])))
    )
    for row in added:
        assert set(row) == CORE_LOG_FIELDS | {"service.name", "e2e.marker"}, (
            f"selectFields must add to the core set: {sorted(row)}"
        )
        assert row["service.name"] == service

    newest_first = [row["timestamp"] for row in core_rows]
    oldest_first = [
        row["timestamp"]
        for row in query_range_rows(first_text_block(assert_tool_ok(search(limit="10", orderBy="timestamp asc"))))
    ]
    assert oldest_first == sorted(newest_first), f"orderBy timestamp asc must return oldest first: {oldest_first}"


def test_search_traces_order_by_duration(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """orderBy on search_traces reaches SigNoz: duration_nano desc returns the slowest span first."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=3)

    def search(**arguments) -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": "seeded spans", "service": service, "timeRange": "1h", "limit": "10", **arguments},
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    wait_for(lambda: len(search()) == 3, f"all three seeded spans for {service} visible")
    durations = [int(row["duration_nano"]) for row in search(orderBy="duration_nano desc")]
    assert durations == sorted(durations, reverse=True), f"spans must come back slowest first: {durations}"


def test_query_metrics_limit_order_having_and_typed_formula_queries(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """limit and orderBy bound the series, having filters them, and typed formulaQueries run."""
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    low, high = f"mcp-e2e-low-{test_id}", f"mcp-e2e-high-{test_id}"
    seed_metrics(low, metric, value=10.0, count=2)
    seed_metrics(high, metric, value=1000.0, count=2)

    def query(**arguments) -> dict:
        return mcp_client.call_tool(
            "signoz_query_metrics",
            {
                "searchContext": "seeded gauge per service",
                "metricName": metric,
                "groupBy": "service.name",
                "requestType": "scalar",
                "reduceTo": "avg",
                "timeRange": "1h",
                **arguments,
            },
        )

    def both_visible() -> bool:
        result = query()
        if result.get("isError", False):
            return False
        _, rows = aggregate_columns_and_row_count(first_text_block(result))
        return rows == 2

    wait_for(both_visible, "both seeded services visible through query_metrics")

    smallest = assert_tool_ok(query(limit="1", orderBy="__result asc"))
    _, rows = aggregate_columns_and_row_count(first_text_block(smallest))
    assert rows == 1, "limit=1 must return one series"
    assert low in first_text_block(smallest) and high not in first_text_block(smallest), (
        "orderBy __result asc must keep the smallest series"
    )
    assert "limit=1 groups, order=__result asc" in note_blocks(smallest), note_blocks(smallest)

    filtered = assert_tool_ok(query(having="__result > 500"))
    _, rows = aggregate_columns_and_row_count(first_text_block(filtered))
    assert rows == 1 and high in first_text_block(filtered), "having must keep only the large series"

    def formula_rows(result: dict) -> list:
        results = first_block_json(result)["data"]["data"]["results"]
        formula_result = [r for r in results if r.get("queryName") == "C"]
        assert formula_result, (
            f"the formula result C must be returned next to its inputs: {[r.get('queryName') for r in results]}"
        )
        return formula_result[0].get("data") or []

    formula_queries = [{"name": "B", "metricName": metric, "groupBy": "service.name", "reduceTo": "avg"}]
    ratio = assert_tool_ok(query(formula="A / B", formulaQueries=formula_queries))
    assert len(formula_rows(ratio)) == 2, (
        f"the typed formulaQueries item must run and the formula must return both groups: {formula_rows(ratio)}"
    )
    assert "Input validation notice" not in "".join(text_blocks(ratio)), text_blocks(ratio)

    # having filters query A before the formula; SigNoz ignores a having on the formula itself.
    filtered_ratio = assert_tool_ok(query(formula="A / B", formulaQueries=formula_queries, having="__result > 500"))
    assert len(formula_rows(filtered_ratio)) == 1, (
        f"having on query A must leave one formula group: {formula_rows(filtered_ratio)}"
    )
    assert "having (on query A" in note_blocks(filtered_ratio), note_blocks(filtered_ratio)
