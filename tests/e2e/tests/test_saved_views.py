"""Saved view CRUD round-trip — port of Family E K5.

Clone a real view's shape (never hand-craft the round-trip payload), read it
back via the canonical id AND the legacy viewId, delete via the legacy key, and
confirm it is gone. The Go test cloned a staging view; here the source view is
seeded first so the flow always runs.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.seeded import create_view, delete_view, extract_view_data, extract_view_name, view_gone


def test_view_crud_round_trip(mcp_client: MCPClient, test_id: str) -> None:
    source_id = create_view(mcp_client, f"mcp-e2e-src-{test_id}")
    clone_id = ""
    deleted = False
    try:
        source = assert_tool_ok(
            mcp_client.call_tool("signoz_get_view", {"searchContext": f"get view {source_id}", "id": source_id})
        )
        source_data = extract_view_data(source)

        name = f"mcp-e2e-e-{test_id}"
        create_args = dict(source_data)
        create_args["name"] = name

        created = assert_tool_ok(mcp_client.call_tool("signoz_create_view", create_args))
        clone_id = extract_view_data(created).get("id", "")
        assert clone_id, f"could not extract created view id from create_view response: {created}"

        # Read back via BOTH the canonical id and the legacy viewId.
        for key in ("id", "viewId"):
            fetched = assert_tool_ok(
                mcp_client.call_tool("signoz_get_view", {"searchContext": f"get view {clone_id}", key: clone_id})
            )
            assert extract_view_name(fetched) == name, f"get_view via {key!r}: name round-trip mismatch"

        # Delete via the legacy key to prove the alias works on the mutation path.
        delete_view(mcp_client, clone_id, key="viewId")
        deleted = True

        assert view_gone(mcp_client, clone_id), f"view {clone_id} should be gone after delete"
    finally:
        if clone_id and not deleted:
            mcp_client.call_tool("signoz_delete_view", {"searchContext": f"cleanup view {clone_id}", "id": clone_id})
        delete_view(mcp_client, source_id)
