"""Field discovery contract against real SigNoz — keys/values context filtering.

The fieldContext descriptions teach a closed context set and warn that SigNoz
does not honor the legacy Query Builder context "tag" (keys silently drops the
filter, values returns empty). These tests pin both halves of that teaching
against the pinned release: taught contexts are honored (a context filter
restricts keys to that context, span intrinsics surface under "span", a seeded
resource attribute's observed values come back in values.stringValues), and the
"tag" trap behaves as the descriptions say it does. queryType=builder_ai_query
pins what the AI field keys endpoint returns: the computed per-trace fields
under the trace context, and the same keys whatever the signal.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import first_block_json
from fixtures.telemetry import seed_traces, wait_for


def field_key_entries(result: dict) -> dict:
    """The data.keys map from a get_field_keys result (fails on envelope drift)."""
    payload = first_block_json(result)
    data = payload.get("data")
    assert isinstance(data, dict) and isinstance(data.get("keys"), dict), f"field keys envelope drifted: {payload}"
    return data["keys"]


def string_values(result: dict) -> list:
    """data.values.stringValues from a get_field_values result (fails on envelope drift)."""
    payload = first_block_json(result)
    data = payload.get("data")
    assert isinstance(data, dict) and isinstance(data.get("values"), dict), f"field values envelope drifted: {payload}"
    return data["values"].get("stringValues") or []


def test_seeded_resource_attribute_values_discoverable(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """get_field_values returns a seeded service.name under the resource context."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def service_visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_get_field_values",
            {
                "searchContext": f"values for service.name including {service}",
                "signal": "traces",
                "name": "service.name",
                "fieldContext": "resource",
                "searchText": service,
            },
        )
        if result.get("isError", False):
            return False
        return service in string_values(result)

    wait_for(service_visible, f"service.name value {service} discoverable")

    # SigNoz does not honor the legacy context "tag": the same lookup returns no
    # values, with the input-schema notice as the only signal. The fieldContext
    # descriptions teach exactly this trap, so if SigNoz starts rejecting or
    # honoring "tag", this pin fails and the teaching must change with it.
    legacy = mcp_client.call_tool(
        "signoz_get_field_values",
        {
            "searchContext": "values for service.name via the legacy tag context",
            "signal": "traces",
            "name": "service.name",
            "fieldContext": "tag",
            "searchText": service,
        },
    )
    assert_tool_ok(legacy)
    assert string_values(legacy) == [], f"legacy 'tag' context unexpectedly honored: {string_values(legacy)}"
    assert "Input validation notice" in text_blocks(legacy), "schema notice missing for out-of-enum fieldContext"


def test_field_keys_context_filter_is_honored(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """A context filter restricts keys to that context: the seeded span attribute appears under "attribute" only."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=1)

    def seeded_marker_keys(context: str) -> dict:
        result = mcp_client.call_tool(
            "signoz_get_field_keys",
            {
                "searchContext": f"discover e2e.marker in context {context}",
                "signal": "traces",
                "fieldContext": context,
                "searchText": "e2e.marker",
            },
        )
        assert_tool_ok(result)
        return field_key_entries(result)

    keys = wait_for(lambda: seeded_marker_keys("attribute") or None, "seeded span attribute e2e.marker discoverable")
    contexts = {entry.get("fieldContext") for entries in keys.values() for entry in entries}
    assert contexts == {"attribute"}, f"fieldContext=attribute returned other contexts: {keys}"

    resource_keys = seeded_marker_keys("resource")
    assert "e2e.marker" not in resource_keys, f"resource filter leaked an attribute key: {resource_keys}"


def test_span_intrinsics_discoverable_under_span_context(mcp_client: MCPClient) -> None:
    """duration_nano is discoverable with fieldContext=span, as the traces guide teaches."""
    result = mcp_client.call_tool(
        "signoz_get_field_keys",
        {
            "searchContext": "discover the span intrinsic duration_nano",
            "signal": "traces",
            "fieldContext": "span",
            "searchText": "duration_nano",
        },
    )
    assert_tool_ok(result)
    keys = field_key_entries(result)
    entries = keys.get("duration_nano", [])
    assert any(e.get("fieldContext") == "span" for e in entries), f"duration_nano missing from span context: {keys}"


AI_TRACE_COLUMNS = {
    "llm_call_count",
    "tool_call_count",
    "distinct_tool_count",
    "input_tokens",
    "output_tokens",
    "total_tokens",
    "estimated_total_cost",
    "max_llm_duration_nano",
}


def ai_key_names(signal: str, mcp_client: MCPClient) -> set:
    """Every key name queryType=builder_ai_query lists for the given signal."""
    result = mcp_client.call_tool(
        "signoz_get_field_keys",
        {"searchContext": "which AI fields exist", "signal": signal, "queryType": "builder_ai_query"},
    )
    assert_tool_ok(result)
    return set(field_key_entries(result))


def ai_trace_column_names(signal: str, mcp_client: MCPClient) -> set:
    """Names that queryType=builder_ai_query lists under the per-trace "trace" context."""
    result = mcp_client.call_tool(
        "signoz_get_field_keys",
        {
            "searchContext": "which AI fields can I filter AI traces on, like cost and token counts",
            "signal": signal,
            "queryType": "builder_ai_query",
        },
    )
    assert_tool_ok(result)
    keys = field_key_entries(result)
    return {name for name, entries in keys.items() if any(e.get("fieldContext") == "trace" for e in entries)}


def test_ai_field_keys_list_per_trace_columns(mcp_client: MCPClient) -> None:
    """queryType=builder_ai_query lists the per-trace cost, token and call-count columns under fieldContext trace."""
    names = ai_trace_column_names("traces", mcp_client)
    assert AI_TRACE_COLUMNS <= names, f"per-trace AI columns missing: {AI_TRACE_COLUMNS - names}"


def test_ai_field_keys_ignore_signal(mcp_client: MCPClient) -> None:
    """signal=logs with queryType=builder_ai_query still lists the AI trace fields, as the queryType description says.

    The AI fields endpoint takes no signal and always reads traces, and the MCP
    does not validate the combination; if SigNoz starts scoping AI keys by
    signal, this pin fails and the description must change with it.
    """
    logs, traces = ai_key_names("logs", mcp_client), ai_key_names("traces", mcp_client)
    assert logs == traces, f"signal=logs changed the AI keys: {sorted(logs ^ traces)}"


def test_ai_field_keys_filter_to_trace_context(mcp_client: MCPClient) -> None:
    """fieldContext=trace with queryType=builder_ai_query returns only the computed per-trace fields."""
    result = mcp_client.call_tool(
        "signoz_get_field_keys",
        {
            "searchContext": "which per-trace AI columns can I filter on",
            "signal": "traces",
            "queryType": "builder_ai_query",
            "fieldContext": "trace",
        },
    )
    assert_tool_ok(result)
    keys = field_key_entries(result)
    contexts = {entry.get("fieldContext") for entries in keys.values() for entry in entries}
    assert contexts == {"trace"}, f"fieldContext=trace returned other contexts: {keys}"
    assert AI_TRACE_COLUMNS <= set(keys), f"per-trace AI columns missing: {AI_TRACE_COLUMNS - set(keys)}"
