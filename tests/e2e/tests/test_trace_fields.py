"""Trace field snake_case migration — port of e2e_trace_fields_test.go.

Canonical row fields are present, deprecated camelCase fields are gone, webUrl
deep links are emitted, canonical shortcut filters and the legacy free-form
filter both pass through, and duration_nano aggregations/group-bys work. All
read-only against seeded traces (the Go test read staging's).
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import aggregate_columns_and_row_count, first_text_block, query_range_rows
from fixtures.telemetry import seed_traces, wait_for

CANONICAL_FIELDS = ["trace_id", "span_id", "duration_nano", "has_error", "service.name", "webUrl"]
DEPRECATED_FIELDS = ["traceID", "spanID", "durationNano", "hasError"]


def test_snake_case_migration(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=3)

    def rows_visible() -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5"},
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    rows = wait_for(rows_visible, f"seeded traces for {service} visible")

    row = next((r for r in rows if r.get("trace_id")), None)
    assert row, f"search_traces returned rows but none carried canonical trace_id: {rows}"
    trace_id = row["trace_id"]

    for key in CANONICAL_FIELDS:
        assert key in row, f"search_traces row missing canonical field {key!r}; row keys: {sorted(row)}"
    for deprecated in DEPRECATED_FIELDS:
        assert deprecated not in row, f"search_traces row still contains deprecated field {deprecated!r}"
    assert "/trace/" in str(row["webUrl"]), f"search_traces row webUrl = {row['webUrl']!r}, want trace deep link"

    # Canonical shortcut filters: has_error=false and duration_nano bounds.
    assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_traces",
            {
                "searchContext": "non-error traces within duration bounds",
                "timeRange": "1h",
                "limit": "1",
                "error": False,
                "minDuration": "0",
                "maxDuration": "86400000000000",
            },
        )
    )

    # Legacy free-form durationNano filter still passes through.
    assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": "traces by duration", "timeRange": "1h", "limit": "1", "filter": "durationNano >= 0"},
        )
    )

    # Canonical duration_nano aggregation.
    assert_tool_ok(
        mcp_client.call_tool(
            "signoz_aggregate_traces",
            {
                "searchContext": "p99 trace duration",
                "timeRange": "1h",
                "aggregation": "p99",
                "aggregateOn": "duration_nano",
                "requestType": "scalar",
            },
        )
    )

    grouped = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_aggregate_traces",
            {
                "searchContext": "trace count by service",
                "timeRange": "1h",
                "aggregation": "count",
                "groupBy": "service.name",
                "limit": "5",
                "requestType": "scalar",
            },
        )
    )
    columns, row_count = aggregate_columns_and_row_count(first_text_block(grouped))
    assert row_count > 0, "aggregate_traces groupBy=service.name returned no aggregate rows despite recent trace rows"
    assert "service.name" in columns, f"aggregate_traces groupBy columns missing service.name; columns: {columns}"

    details = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_get_trace_details",
            {"searchContext": f"trace {trace_id}", "traceId": trace_id, "timeRange": "1h", "includeSpans": True},
        )
    )
    body = first_text_block(details)
    assert '"webUrl"' in body and "/trace/" in body, "get_trace_details response missing trace webUrl"
