"""Trace field snake_case migration — port of e2e_trace_fields_test.go.

Canonical row fields are present, deprecated camelCase fields are gone, webUrl
deep links are emitted, canonical shortcut filters and the legacy free-form
filter both pass through, and duration_nano aggregations/group-bys work. All
read-only against seeded traces (the Go test read staging's).
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import aggregate_columns_and_row_count, first_text_block, query_range_rows
from fixtures.telemetry import seed_traces, wait_for

CANONICAL_FIELDS = ["trace_id", "span_id", "duration_nano", "has_error", "service.name", "webUrl"]
DEPRECATED_FIELDS = ["traceID", "spanID", "durationNano", "hasError"]
# The default search_traces columns. A drift here changes what every caller
# gets without naming selectFields.
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


def test_search_traces_core_fields_and_select_fields(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Rows default to the flat core set; selectFields adds flat keys on top of it."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def search(extra: dict) -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5", **extra},
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    rows = wait_for(lambda: search({}), f"seeded traces for {service} visible")
    for row in rows:
        assert set(row) - {"webUrl"} == CORE_SEARCH_FIELDS, f"default row keys drifted from the core set: {sorted(row)}"
        assert row["service.name"] == service

    # The seeded span attribute e2e.marker is outside the core set; each accepted
    # form must return it as its own flat key next to the core fields.
    for select_fields in (["e2e.marker"], ["attribute.e2e.marker", "service.name"], "e2e.marker, name"):
        rows = search({"selectFields": select_fields})
        assert rows, f"selectFields={select_fields!r} returned no rows"
        for row in rows:
            assert row.get("e2e.marker") == service, (
                f"selectFields={select_fields!r} row lacks flat e2e.marker={service!r}; row keys: {sorted(row)}"
            )
            assert CORE_SEARCH_FIELDS <= set(row), f"selectFields={select_fields!r} dropped core fields: {sorted(row)}"
            assert "resource" not in row and "attributes" not in row, f"row switched to nested maps: {sorted(row)}"

    invalid = mcp_client.call_tool(
        "signoz_search_traces",
        {"searchContext": "bad selectFields", "service": service, "timeRange": "1h", "selectFields": [1, 2]},
    )
    assert invalid.get("isError", False), "non-string selectFields items must be rejected"
    assert "selectFields" in first_text_block(invalid), f"error must name selectFields: {first_text_block(invalid)!r}"

    # An unknown field is never silent: SigNoz either rejects the key or runs the
    # query and returns a key-not-found warning, which the tool shows as a note.
    unknown_key = f"mcp.e2e.absent.{test_id.replace('-', '_')}"
    unknown = mcp_client.call_tool(
        "signoz_search_traces",
        {"searchContext": "unknown field", "service": service, "timeRange": "1h", "selectFields": [unknown_key]},
    )
    assert unknown_key in text_blocks(unknown), (
        f"unknown selectFields key {unknown_key!r} was dropped silently: {text_blocks(unknown)[:600]}"
    )
