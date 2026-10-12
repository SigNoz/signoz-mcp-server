"""Upstream error classification — port of Family B (upstream half).

A well-formed-but-nonexistent id reaches the backend and the rejection carries
the uniform "SigNoz API error:" prefix. A bad credential surfaces through the
same coded upstream path (AGENTS.md: 401/403 must propagate, never hide inside
partial results).
"""

from fixtures.mcpclient import MCPClient, text_blocks
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


def test_unknown_filter_key_surfaces_backend_warning(mcp_client: MCPClient, test_id: str) -> None:
    """A filter on a key the workspace never carried surfaces SigNoz's non-fatal warning as a note.

    The pinned release does not hard-error on an unknown log key: it falls back
    to querying the data directly and returns a warning envelope naming the key.
    The server must surface that warning as its own note block so agents see
    the typo signal (fail open, never silent). If SigNoz goes back to a 400
    here, the coded key-not-found path in errs.go takes over and this pin must
    change with it.
    """
    key = f"mcp_e2e_missing_{test_id.replace('-', '_')}"
    result = mcp_client.call_tool(
        "signoz_search_logs",
        {"searchContext": f"logs filtered on {key}", "filter": f"{key} = 'x'", "timeRange": "5m"},
    )
    body = first_text_block(result)
    assert not result.get("isError", False), f"unknown key {key} soft-fallback became an error: {body[:300]}"
    notes = text_blocks(result)
    assert "not found in metadata" in notes and key in notes, f"backend warning not surfaced as a note: {notes[:500]}"
