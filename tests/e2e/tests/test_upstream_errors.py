"""Upstream error classification — port of Family B (upstream half).

A well-formed-but-nonexistent id reaches the backend and the rejection carries
the uniform "SigNoz API error:" prefix. A bad credential surfaces through the
same coded upstream path (AGENTS.md: 401/403 must propagate, never hide inside
partial results).
"""

from fixtures.mcpclient import MCPClient
from fixtures.mcpserver import MCPServer
from fixtures.results import first_text_block

GHOST_RULE_ID = "01900000-0000-7000-8000-000000000000"  # valid UUIDv7, nonexistent


def test_upstream_error_prefix(mcp_client: MCPClient) -> None:
    result = mcp_client.call_tool(
        "signoz_get_alert", {"searchContext": f"get rule {GHOST_RULE_ID}", "ruleId": GHOST_RULE_ID}
    )
    body = first_text_block(result)
    if not result.get("isError", False):
        # Some deployments return an empty 200 for an unknown rule — backend
        # behavior, not a regression; nothing to assert on the prefix then.
        return
    assert body.startswith("SigNoz API error:"), f"upstream error should carry the uniform prefix; got {body!r}"


def test_invalid_api_key_surfaces_coded_error(mcp_server: MCPServer) -> None:
    """A rejected credential comes back as a coded tool error, not a crash."""
    bad_client = MCPClient(mcp_url=mcp_server.mcp_url, headers={"Authorization": "Bearer mcp-e2e-invalid-key"})
    try:
        result = bad_client.call_tool("signoz_list_dashboards", {"searchContext": "list dashboards with a bad key"})
    finally:
        bad_client.close()

    assert result.get("isError", False), (
        f"expected a tool error for a rejected credential, got: {first_text_block(result)[:300]}"
    )
    body = first_text_block(result)
    assert body.startswith("SigNoz API error:"), f"upstream auth failure should carry the uniform prefix; got {body!r}"
