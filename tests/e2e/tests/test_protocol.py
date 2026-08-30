from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.mcpserver import MCPServer
from fixtures.results import first_text_block


def test_initialize_handshake(mcp_server: MCPServer) -> None:
    """The initialize handshake negotiates the legacy era against the live backend."""
    client = MCPClient(mcp_url=mcp_server.mcp_url)
    try:
        result = client.initialize()
    finally:
        client.close()

    assert result["protocolVersion"] == "2025-11-25"
    assert result["serverInfo"]["name"] == "SigNozMCP"
    assert result["serverInfo"]["version"]
    assert result["instructions"]
    capabilities = result["capabilities"]
    assert "tools" in capabilities
    assert "resources" in capabilities
    assert "prompts" in capabilities


def test_tools_list_and_call(mcp_client: MCPClient) -> None:
    """tools/list serves the catalog, and a list tool succeeds against the live backend."""
    tools = mcp_client.list_tools()
    names = {tool["name"] for tool in tools}
    assert "signoz_search_logs" in names
    assert "signoz_search_docs" in names
    assert "signoz_list_dashboards" in names

    result = assert_tool_ok(mcp_client.call_tool("signoz_list_dashboards", {"searchContext": "e2e protocol smoke"}))
    assert text_blocks(result), "signoz_list_dashboards returned an empty body"


def test_get_alert_without_arguments_returns_validation_error(mcp_client: MCPClient) -> None:
    """A tools/call with no arguments object yields a validation error, not a panic (nil_arguments port)."""
    result = mcp_client.call_tool_without_arguments("signoz_get_alert")

    text = first_text_block(result)
    assert result.get("isError", False), f"expected a validation error tool result, got: {text[:300]}"
    assert '"id"' in text, f"expected an id validation message, got: {text[:300]}"
    assert "panic" not in text.lower(), (
        f"response still surfaces a panic instead of a clean validation error: {text[:300]}"
    )
