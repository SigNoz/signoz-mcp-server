from fixtures.mcpclient import MCPClient, assert_tool_ok, first_json, text_blocks


def _dashboard_uuid(payload: dict) -> str:
    data = payload.get("data", payload)
    uuid = data.get("uuid") or data.get("id")
    assert uuid, f"no dashboard uuid in payload: {payload}"
    return uuid


def test_dashboard_create_get_delete_roundtrip(mcp_client: MCPClient, test_id: str) -> None:
    """A dashboard created through MCP tools is readable, then deletable, then gone."""
    title = f"mcp-e2e-{test_id}"
    create = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_create_dashboard",
            {
                "searchContext": f"create a dashboard named {title}",
                "schemaVersion": "v6",
                "tags": [],
                "spec": {
                    "display": {"name": title},
                    "variables": [],
                    "panels": {},
                    "layouts": [],
                },
            },
        )
    )
    uuid = _dashboard_uuid(first_json(create))

    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool("signoz_get_dashboard", {"searchContext": f"get dashboard {uuid}", "id": uuid})
        )
        assert title in text_blocks(fetched), (
            f"get_dashboard did not return the created dashboard: {text_blocks(fetched)[:400]}"
        )

        deleted = assert_tool_ok(
            mcp_client.call_tool("signoz_delete_dashboard", {"searchContext": f"delete dashboard {uuid}", "id": uuid})
        )
        assert "deleted" in text_blocks(deleted).lower()

        gone = mcp_client.call_tool(
            "signoz_get_dashboard", {"searchContext": f"confirm dashboard {uuid} is gone", "id": uuid}
        )
        assert gone.get("isError", False), f"dashboard {uuid} still readable after delete: {text_blocks(gone)[:400]}"
    finally:
        # Confirm the resource is gone even when an assertion above failed;
        # deletion is idempotent enough for a best-effort retry here.
        mcp_client.call_tool("signoz_delete_dashboard", {"searchContext": f"cleanup dashboard {uuid}", "id": uuid})
