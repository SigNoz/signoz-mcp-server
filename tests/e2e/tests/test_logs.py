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


def test_unknown_keys_and_body_fields_match_the_taught_contract(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    """Pins the outcomes the log tool descriptions quote for unknown keys and body fields.

    The descriptions teach that an unknown filter key may not fail and, when it
    runs, returns no rows with a `key ... not found in metadata` warning; and
    that a body field is usually rejected for grouping with one of two quoted
    messages. Each call must land on an outcome the descriptions name.
    """
    service = f"mcp-e2e-{test_id}"
    seed_logs(service, f"marker-{test_id}", count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {"searchContext": "seeded logs", "service": service, "timeRange": "1h", "limit": "5"},
        )
        return not result.get("isError", False) and bool(query_range_rows(first_text_block(result)))

    wait_for(visible, f"seeded logs for {service} visible")

    unknown_key = f"mcp_e2e_absent_{test_id.replace('-', '_')}"
    filtered = mcp_client.call_tool(
        "signoz_search_logs",
        {
            "searchContext": "filter on a key the workspace never carried",
            "service": service,
            "filter": f"{unknown_key} = 'x'",
            "timeRange": "1h",
            "limit": "5",
        },
    )
    if filtered.get("isError", False):
        assert f"key {unknown_key} not found" in first_text_block(filtered), (
            f"an unknown key failed with a message other than the taught one: {first_text_block(filtered)[:400]}"
        )
    else:
        assert not query_range_rows(first_text_block(filtered)), "an unknown key matched rows"
        assert f"key `{unknown_key}` not found in metadata" in note_blocks(filtered), (
            f"the taught warning is missing: {note_blocks(filtered)!r}"
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
        f"a body-field group-by ran, but the descriptions teach that it is rejected: {first_text_block(grouped)[:400]}"
    )
    message = first_text_block(grouped)
    assert "can be filtered but not selected or grouped" in message or "field not found" in message, (
        f"a body-field group-by failed with a message other than the taught ones: {message[:400]}"
    )
