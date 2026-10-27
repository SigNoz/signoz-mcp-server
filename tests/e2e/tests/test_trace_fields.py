"""Trace fields against seeded traces — port of e2e_trace_fields_test.go.

Rows carry the core field set and webUrl deep links, the shortcut filters
narrow on the canonical keys, group-bys work, and selectFields adds flat keys.
All read-only against seeded traces (the Go test read staging's).
"""

import json

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import aggregate_columns_and_row_count, first_text_block, note_blocks, query_range_rows
from fixtures.telemetry import seed_traces, wait_for

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


def test_trace_deep_links_shortcut_filters_and_group_by(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=3)

    def search(extra: dict) -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5", **extra},
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    # The seeding marks one of the three spans as an error and makes every span
    # 250ms long, so each shortcut filter below must narrow the rows, not just pass.
    def error_and_ok_rows() -> list[dict]:
        found = search({})
        return found if {r.get("has_error") for r in found} >= {True, False} else []

    rows = wait_for(error_and_ok_rows, f"seeded spans for {service} with has_error true and false visible")

    row = next((r for r in rows if r.get("trace_id")), None)
    assert row, f"search_traces returned rows but none carried canonical trace_id: {rows}"
    trace_id = row["trace_id"]
    assert "/trace/" in str(row.get("webUrl")), (
        f"search_traces row webUrl = {row.get('webUrl')!r}, want trace deep link"
    )

    for error in (False, True):
        filtered = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_search_traces",
                {
                    "searchContext": f"traces with error={error} within duration bounds",
                    "service": service,
                    "timeRange": "1h",
                    "limit": "5",
                    "error": error,
                    "minDuration": "0",
                    "maxDuration": "86400000000000",
                },
            )
        )
        filtered_rows = query_range_rows(first_text_block(filtered))
        assert filtered_rows, f"error={error} within duration bounds returned no seeded spans"
        assert all(r.get("has_error") is error for r in filtered_rows), (
            f"error={error} returned rows with another has_error: {[r.get('has_error') for r in filtered_rows]}"
        )
    # The seeded spans are 250ms long. Each bound below is written in a form the
    # duration parameters document: a unit string, nanosecond digits, or a JSON number.
    for bounds, want_rows in (
        ({"maxDuration": "1000000"}, False),
        ({"maxDuration": "1ms"}, False),
        ({"maxDuration": 1000000}, False),
        ({"minDuration": "200ms", "maxDuration": "1s"}, True),
        ({"minDuration": "300ms"}, False),
    ):
        bounded = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_search_traces",
                {"searchContext": f"traces within {bounds}", "service": service, "timeRange": "1h", **bounds},
            )
        )
        assert bool(query_range_rows(first_text_block(bounded))) is want_rows, (
            f"duration bounds {bounds} should {'match' if want_rows else 'exclude'} the 250ms seeded spans"
        )

    grouped = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_aggregate_traces",
            {
                "searchContext": "trace count by service",
                "timeRange": "1h",
                "aggregations": "count()",
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
    page = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"traces from {service}", "service": service, "timeRange": "1h", "limit": "5"},
        )
    )
    assert "hasMore" in note_blocks(page), f"expected a completeness note with hasMore: {note_blocks(page)!r}"

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


def test_shortcut_filters_narrow_an_or_filter(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """The service shortcut ANDs with the whole filter, so a top-level OR cannot escape it."""
    service = f"mcp-e2e-{test_id}"
    other = f"mcp-e2e-other-{test_id}"
    seed_traces(service, count=2)
    seed_traces(other, count=2)
    either = f"service.name = '{other}' OR service.name = '{service}'"

    def search(extra: dict) -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {
                "searchContext": f"spans from {service} or {other}",
                "filter": either,
                "timeRange": "1h",
                "limit": "20",
                **extra,
            },
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    # Wait until both services are visible, so the narrowed search below can fail.
    def both_visible() -> list[dict]:
        rows = search({})
        return rows if {r.get("service.name") for r in rows} == {service, other} else []

    wait_for(both_visible, f"seeded spans for {service} and {other} visible")
    rows = search({"service": service})
    assert rows, f"the service shortcut with an OR filter returned no spans for {service}"
    services = {r.get("service.name") for r in rows}
    assert services == {service}, f"service shortcut did not narrow the OR filter; got services {services}"


def test_trace_details_finds_a_trace_by_id_alone(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Pins the contract the get_trace_details description teaches: no time window, complete span records, and no spans for an unknown ID."""
    trace_id = seed_traces(f"mcp-e2e-{test_id}", count=1)[0]

    def spans() -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_get_trace_details", {"searchContext": f"trace {trace_id}", "traceId": trace_id}
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    rows = wait_for(spans, f"trace {trace_id} found by ID")
    assert {r.get("trace_id") for r in rows} == {trace_id}, f"the trace lookup returned other traces: {rows}"
    # Each span is its complete record: the seeding sets e2e.marker as a span
    # attribute and service.name as a resource attribute.
    span = rows[0]
    assert span.get("attributes", {}).get("e2e.marker") == f"mcp-e2e-{test_id}", f"span attributes missing: {span}"
    assert span.get("resource", {}).get("service.name") == f"mcp-e2e-{test_id}", f"span resource missing: {span}"

    unknown = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_get_trace_details", {"searchContext": "trace that never existed", "traceId": "0" * 32}
        )
    )
    assert not query_range_rows(first_text_block(unknown)), "an unknown trace ID returned spans"


def test_is_root_selects_root_spans(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Pins the isRoot span-scope filter the traces guide teaches: child spans are excluded."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2, with_child=True)

    def search(extra: dict) -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": f"spans of {service}", "service": service, "timeRange": "1h", "limit": "20", **extra},
        )
        if result.get("isError", False):
            return []
        return query_range_rows(first_text_block(result))

    # Wait until the child spans are visible, so the isRoot search below can fail.
    wait_for(lambda: len(search({})) == 4, f"root and child spans for {service} visible")
    rows = search({"filter": "isRoot = true"})
    assert sorted(r.get("name") for r in rows) == ["e2e-operation", "e2e-operation"], (
        f"isRoot = true must return only the two roots: {rows}"
    )


def test_group_by_resolves_custom_and_intrinsic_trace_fields(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """SigNoz resolves bare groupBy names; no MCP field list names e2e.marker."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=3)

    def grouped() -> list:
        result = mcp_client.call_tool(
            "signoz_aggregate_traces",
            {
                "searchContext": f"span count by marker and error for {service}",
                "service": service,
                "aggregations": "count()",
                "groupBy": ["e2e.marker", "has_error"],
                "timeRange": "1h",
                "requestType": "scalar",
            },
        )
        if result.get("isError", False):
            return []
        payload = json.loads(first_text_block(result))
        rows = [row for r in payload["data"]["data"]["results"] for row in r.get("data") or []]
        return rows if sum(row[-1] for row in rows) == 3 else []

    rows = wait_for(grouped, f"grouped spans for {service}")
    # The seeding sets e2e.marker to the service name and marks one span as an error.
    assert {(row[0], str(row[1]).lower()) for row in rows} == {(service, "true"), (service, "false")}, rows


def test_all_fields_returns_each_span_complete(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Pins the allFields contract on traces: the same span record signoz_get_trace_details returns."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=1)

    def spans() -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_traces",
            {"searchContext": "complete spans", "service": service, "allFields": True, "timeRange": "1h"},
        )
        return [] if result.get("isError", False) else query_range_rows(first_text_block(result))

    span = wait_for(spans, f"complete spans for {service}")[0]
    assert span.get("attributes", {}).get("e2e.marker") == service, f"span attributes missing: {span}"
    assert span.get("resource", {}).get("service.name") == service, f"span resource missing: {span}"
