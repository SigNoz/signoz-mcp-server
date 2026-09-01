"""Docs agent flow — port of e2e_docs_test.go::TestE2EDocsAgentFlow.

Both docs tools are discoverable, search and fetch work against the embedded
corpus, an out-of-scope URL is a structured tool error (never a transport
error), and the sitemap resource resolves. Assertions avoid pinning specific
page titles — the embedded corpus tracks signoz.io.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, first_text_block, result_code


def test_docs_agent_flow(mcp_client: MCPClient) -> None:
    tools = mcp_client.list_tools()
    names = {tool["name"] for tool in tools}
    assert "signoz_search_docs" in names
    assert "signoz_fetch_doc" in names

    search = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_docs",
            {"searchContext": "How do I send Docker logs to SigNoz?", "searchText": "docker", "limit": 5},
        )
    )
    assert "docker" in first_text_block(search).lower(), "search_docs happy path returned no docker content"

    fetch = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_fetch_doc",
            {"searchContext": "fetch the docker install doc", "url": "https://signoz.io/docs/install/docker/"},
        )
    )
    payload = first_block_json(fetch)
    assert payload["url"] == "https://signoz.io/docs/install/docker/"
    assert "Docker" in payload["content"]
    assert payload["truncation_reason"] == "none"
    assert payload["available_headings"], "expected a populated available_headings list"

    out_of_scope = mcp_client.call_tool(
        "signoz_fetch_doc",
        {"searchContext": "fetch an out-of-scope doc", "url": "https://evil.example.com/docs/x/"},
    )
    assert out_of_scope.get("isError", False), (
        "out-of-scope URL must surface as a tool-result error, not a transport error"
    )
    assert result_code(out_of_scope) == "OUT_OF_SCOPE_URL"

    sitemap = mcp_client.read_resource("signoz://docs/sitemap")
    contents = sitemap.get("contents", [])
    assert contents, "sitemap resource returned no contents"
    text = contents[0].get("text", "")
    assert "signoz.io/docs/" in text, "sitemap resource body does not look like the docs sitemap"
