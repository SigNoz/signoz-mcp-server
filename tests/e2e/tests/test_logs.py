import pytest

from fixtures.logger import setup_logger
from fixtures.mcpclient import MCPClient, assert_tool_ok, first_json, text_blocks
from fixtures.results import first_text_block, note_blocks, query_range_rows
from fixtures.telemetry import seed_field_tokens, seed_logs, wait_for

logger = setup_logger(__name__)


def _decoded_rows_contain(result: dict, expected: str) -> bool:
    def strings(value: object):
        if isinstance(value, str):
            yield value
        elif isinstance(value, dict):
            for nested in value.values():
                yield from strings(nested)
        elif isinstance(value, list):
            for nested in value:
                yield from strings(nested)

    return any(expected == value for row in query_range_rows(first_text_block(result)) for value in strings(row))


def test_seeded_logs_are_searchable(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Logs seeded over OTLP become visible through signoz_search_logs."""
    service = f"mcp-e2e-{test_id}"
    marker = f"mcp-e2e-marker-{test_id}"
    seed_logs(service, marker, count=3)

    def search() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": f"find the e2e marker log line {marker}",
                "searchText": marker,
                "timeRange": "1h",
                "limit": "10",
            },
        )
        if result.get("isError", False):
            logger.info("signoz_search_logs errored, retrying: %s", text_blocks(result)[:200])
            return False
        return marker in text_blocks(result)

    wait_for(search, f"seeded log marker {marker} visible via signoz_search_logs")

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": f"find logs from service {service}",
                "service": service,
                "timeRange": "1h",
                "limit": "10",
            },
        )
    )
    assert marker in text_blocks(result), f"service-filtered search missed the marker: {first_json(result)}"
    assert "hasMore" in note_blocks(result), f"expected a completeness note with hasMore: {note_blocks(result)!r}"


def test_body_regexp_with_doubled_backslashes_matches_the_message(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """The body REGEXP example in signoz://dashboard/query-builder-example matches as taught."""
    marker = f"mcp-e2e-regexp-{test_id}"
    seed_logs(f"mcp-e2e-{test_id}", f"[SnapshotGenerator id=42] {marker}")
    # A filter literal doubles each backslash, so SigNoz receives the regex ^\[SnapshotGenerator id=\d+\].
    expression = f"body REGEXP '^\\\\[SnapshotGenerator id=\\\\d+\\\\] {marker}$'"

    def found() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "find snapshot generator logs", "filter": expression, "timeRange": "1h", "limit": "10"},
        )
        assert not result.get("isError", False), f"body REGEXP was rejected: {text_blocks(result)[:400]}"
        return marker in text_blocks(result)

    wait_for(found, f"body REGEXP matches {marker}")


def test_explicit_search_scopes_quoting_and_warning_round_trip(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """Pins v0.142.0 search() scopes, literal escaping, and cost warning."""
    service = f"mcp-e2e-search-{test_id}"
    unique = test_id.replace("-", "")
    plain_sentinel = f"plain-{unique}"
    body_token = f"{plain_sentinel} apo'strophe back\\slash"
    readiness_body = f"{plain_sentinel} readiness"
    attribute_token = f"attribute-{unique}"
    resource_token = f"resource-{unique}"
    seed_logs(service, readiness_body)
    seed_field_tokens(
        service,
        body_token=body_token,
        attribute_token=attribute_token,
        resource_token=resource_token,
    )

    def search(filter_expression: str, **arguments: str) -> dict:
        return mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": f"search the seeded log with {filter_expression}",
                "filter": filter_expression,
                "service": service,
                "timeRange": "1h",
                "limit": "10",
                **arguments,
            },
        )

    def quoted(value: str) -> str:
        return "'" + value.replace("\\", "\\\\").replace("'", "\\'") + "'"

    def body_visible(expected: str) -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": "wait for the seeded row through its service only",
                "service": service,
                "timeRange": "1h",
                "limit": "10",
            },
        )
        return not result.get("isError", False) and _decoded_rows_contain(result, expected)

    wait_for(
        lambda: body_visible(readiness_body),
        "the plain readiness row to be visible through its service only",
        timeout=60.0,
    )
    wait_for(
        lambda: body_visible(body_token),
        "the special-character row to be visible through its service only",
        timeout=60.0,
    )

    search_text_result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": "find the exact escaped body through body-only searchText",
                "searchText": body_token,
                "service": service,
                "timeRange": "1h",
                "limit": "10",
            },
        )
    )
    plain = assert_tool_ok(search(f"search({quoted(plain_sentinel)}, body)"))
    unscoped = assert_tool_ok(search(f"search({quoted(body_token)})"))
    assert query_range_rows(first_text_block(search_text_result)), (
        "searchText missed a service-visible body containing an apostrophe and backslash"
    )
    assert query_range_rows(first_text_block(plain)), "plain scoped search() missed the service-visible body"
    assert _decoded_rows_contain(search_text_result, body_token)
    assert _decoded_rows_contain(unscoped, body_token), "unscoped search() missed the escaped body token"
    assert "search() runs across all fields" in note_blocks(unscoped), (
        f"upstream search() cost warning was not preserved: {note_blocks(unscoped)!r}"
    )

    for scope in ("body", "attribute", "resource"):
        token = {"body": body_token, "attribute": attribute_token, "resource": resource_token}[scope]
        result = assert_tool_ok(search(f"search({quoted(token)}, {scope})"))
        assert query_range_rows(first_text_block(result)), f"scoped {scope} search missed its distinct token"
        assert _decoded_rows_contain(result, body_token), f"scoped {scope} search returned the wrong row"

    log_scope_result = assert_tool_ok(search(f"search({quoted(body_token)}, log)"))
    assert not query_range_rows(first_text_block(log_scope_result)), (
        "body-only token incorrectly matched the log-field scope"
    )

    # searchText is body-only and must not silently become cross-field search().
    missed = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": "body-only searchText must miss an attribute-only token",
                "searchText": attribute_token,
                "service": service,
                "timeRange": "1h",
                "limit": "10",
            },
        )
    )
    assert not query_range_rows(first_text_block(missed)), "body-only searchText matched an attribute-only token"

    composed = assert_tool_ok(
        search(
            f"search({quoted(body_token)}, body) AND NOT search({quoted(attribute_token)}, resource)",
        )
    )
    assert query_range_rows(first_text_block(composed))
    assert _decoded_rows_contain(composed, body_token)


@pytest.mark.skip(
    reason="foundry casts set allow_simdjson: 0, so the body JSON fallback for a missing key returns a "
    "ClickHouse 500 (SigNoz/events-pod#227); unskip once foundry drops the setting"
)
def test_missing_filter_key_returns_no_rows_with_the_taught_warning(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """A filter on a key the workspace never carried runs, returns no rows, and warns, as the log filter teaches."""
    service = f"mcp-e2e-{test_id}"
    seed_logs(service, f"marker-{test_id}", count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "seeded logs", "service": service, "timeRange": "1h", "limit": "5"},
        )
        return not result.get("isError", False) and bool(query_range_rows(first_text_block(result)))

    wait_for(visible, f"seeded logs for {service} visible")

    missing_key = f"mcp_e2e_absent_{test_id.replace('-', '_')}"
    filtered = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": "filter on a key the workspace never carried",
                "service": service,
                "filter": f"{missing_key} = 'x'",
                "timeRange": "1h",
                "limit": "5",
            },
        )
    )
    assert not query_range_rows(first_text_block(filtered)), "a missing key matched rows"
    assert f"key `{missing_key}` not found in metadata" in note_blocks(filtered), (
        f"the taught warning is missing: {note_blocks(filtered)!r}"
    )


def test_unlisted_body_field_cannot_be_grouped(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """With JSON bodies ingested, field keys list no body paths, and grouping by one fails as the groupBy description says."""
    service = f"mcp-e2e-{test_id}"
    seed_logs(service, f'{{"mcp_e2e_field": "{test_id}"}}', count=2)

    def seeded_rows() -> list:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "find the seeded JSON-body logs", "service": service, "timeRange": "1h"},
        )
        assert_tool_ok(result)
        return query_range_rows(first_text_block(result))

    wait_for(lambda: seeded_rows() or None, "seeded JSON-body logs searchable")

    listed = mcp_client.call_tool(
        "signoz_get_field_keys",
        {"searchContext": "which body fields can I group by", "signal": "logs", "fieldContext": "body"},
    )
    assert_tool_ok(listed)
    body_keys = first_json(listed)["data"]["keys"]
    assert not body_keys, (
        f"field keys list body paths, so the groupBy description says they can be grouped: {body_keys}"
    )

    grouped = mcp_client.call_tool(
        "signoz_aggregate_logs",
        {
            "searchContext": "group by a JSON body field",
            "service": service,
            "aggregations": "count()",
            "groupBy": "body.mcp_e2e_field",
            "timeRange": "1h",
        },
    )
    assert grouped.get("isError", False), (
        f"an unlisted body field grouped, but the descriptions teach that it is rejected: {first_text_block(grouped)[:400]}"
    )
    message = first_text_block(grouped)
    assert "can be filtered but not selected or grouped" in message or "field not found" in message, (
        f"an unlisted body field failed with a message other than the taught ones: {message[:400]}"
    )


def test_search_scope_without_search_text_is_ignored_with_a_note(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """searchScope with no searchText runs the search unscoped and says so, instead of failing."""
    service = f"mcp-e2e-{test_id}"
    seed_logs(service, f"marker-{test_id}", count=2)

    def search(**arguments) -> dict:
        return mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "seeded logs", "service": service, "timeRange": "1h", "limit": "5", **arguments},
        )

    def visible() -> bool:
        result = search()
        return not result.get("isError", False) and bool(query_range_rows(first_text_block(result)))

    wait_for(visible, f"seeded logs for {service} visible")

    scoped = assert_tool_ok(search(searchScope="attribute"))
    assert query_range_rows(first_text_block(scoped)), "the search must still return the seeded rows"
    assert "searchScope was ignored because searchText is empty" in note_blocks(scoped), note_blocks(scoped)

    unknown = search(searchScope="everywhere")
    assert unknown.get("isError", False), "an unknown searchScope is still rejected"


def test_severity_shortcut_matches_any_case_as_a_prefix(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """The severity shortcut finds WARNING logs from warn, while = in a filter stays exact."""
    service = f"mcp-e2e-{test_id}"
    seed_logs(service, f"marker-{test_id}", severity="WARNING", count=2)

    def search(**arguments: str) -> list[dict]:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "warning logs", "service": service, "timeRange": "1h", **arguments},
        )
        return query_range_rows(first_text_block(assert_tool_ok(result)))

    rows = wait_for(lambda: search(severity="warn"), f"WARNING logs for {service} found by severity=warn")
    assert len(rows) == 2, rows
    assert not search(filter="severity_text = 'warning'"), "= in a filter matched logs recorded as WARNING"
