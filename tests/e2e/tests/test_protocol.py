from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.mcpserver import MCPServer


def test_initialize_handshake(mcp_server: MCPServer) -> None:
    """The initialize handshake negotiates the legacy era against the live backend."""
    result = MCPClient(mcp_url=mcp_server.mcp_url).initialize()

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
