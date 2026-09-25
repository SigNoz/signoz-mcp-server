"""Trace field snake_case migration — port of e2e_trace_fields_test.go.

Canonical row fields are present, deprecated camelCase fields are gone, webUrl
deep links are emitted, canonical shortcut filters and the legacy free-form
filter both pass through, and duration_nano aggregations/group-bys work. All
read-only against seeded traces (the Go test read staging's).
"""

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import aggregate_columns_and_row_count, first_text_block, query_range_rows, result_code
from fixtures.telemetry import seed_traces, wait_for

CANONICAL_FIELDS = ["trace_id", "span_id", "duration_nano", "has_error", "service.name", "webUrl"]
DEPRECATED_FIELDS = ["traceID", "spanID", "durationNano", "hasError"]
# Drift pin: the columns every search returns without selectFields.
CORE_SEARCH_FIELDS = {
    "timestamp",
    "trace_id",
    "span_id",
    "parent_span_id",
    "name",
    "service.name",
    "kind_string",
    "duration_nano",
    "has_error",
    "status_code_string",
    "status_message",
    "response_status_code",
    "http_method",
}


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


def test_search_traces_default_rows_carry_only_core_fields(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def rows_visible() -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5"},
        )
        return [] if result.get("isError", False) else query_range_rows(first_text_block(result))

    rows = wait_for(rows_visible, f"seeded traces for {service} visible")

    assert all(set(row) - {"webUrl"} == CORE_SEARCH_FIELDS for row in rows), f"row keys drifted: {rows}"
    assert all(row["service.name"] == service for row in rows)


@pytest.mark.parametrize(
    "select_fields",
    [["e2e.marker"], ["attribute.e2e.marker", "service.name"], "e2e.marker, name"],
    ids=["array", "prefixed_array", "comma_string"],
)
def test_search_traces_select_fields_add_flat_keys(
    mcp_client: MCPClient, test_id: str, telemetry: None, select_fields: list[str] | str
) -> None:
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=1)

    def rows_visible() -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {
                "searchContext": f"traces from {service}",
                "service": service,
                "timeRange": "1h",
                "selectFields": select_fields,
            },
        )
        return [] if result.get("isError", False) else query_range_rows(first_text_block(result))

    rows = wait_for(rows_visible, f"seeded traces for {service} visible")

    assert all(row.get("e2e.marker") == service for row in rows), f"e2e.marker missing as a flat key: {rows}"
    assert all(CORE_SEARCH_FIELDS <= set(row) for row in rows), f"core fields dropped: {rows}"
    assert all("resource" not in row and "attributes" not in row for row in rows), f"rows became nested: {rows}"


@pytest.mark.parametrize(
    ("select_fields", "expected"),
    [([1, 2], '"selectFields" item 1'), (["attribute.service.name"], '"attribute.service.name" conflicts')],
    ids=["non_string_item", "context_conflict"],
)
def test_search_traces_rejects_unrepresentable_select_fields(
    mcp_client: MCPClient, select_fields: list, expected: str
) -> None:
    result = mcp_client.call_tool(
        "signoz_search_traces",
        {"searchContext": "select fields", "timeRange": "1h", "selectFields": select_fields},
    )

    assert result_code(result) == "VALIDATION_FAILED", first_text_block(result)
    assert expected in first_text_block(result)


def test_search_traces_reports_unknown_select_field(mcp_client: MCPClient, test_id: str) -> None:
    unknown_key = f"mcp.e2e.absent.{test_id.replace('-', '_')}"

    result = mcp_client.call_tool(
        "signoz_search_traces",
        {"searchContext": "unknown field", "timeRange": "1h", "selectFields": [unknown_key]},
    )

    # SigNoz either rejects the key or returns a key-not-found warning, shown as a note.
    assert unknown_key in text_blocks(result), f"unknown key dropped silently: {text_blocks(result)[:600]}"
