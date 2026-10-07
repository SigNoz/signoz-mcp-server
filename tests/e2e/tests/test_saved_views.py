"""Saved view CRUD round-trip — port of Family E K5.

Clone a real view's shape (never hand-craft the round-trip payload), read it
back via the canonical id AND the legacy viewId, delete via the legacy key, and
confirm it is gone. The Go test cloned a staging view; here the source view is
seeded first so the flow always runs.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json
from fixtures.seeded import create_view, delete_view, extract_view_data, extract_view_name, view_gone


def listed_view_names(result: dict, source: str | None = None) -> set:
    """Names in a signoz_list_views result (fails on envelope drift)."""
    payload = first_block_json(result)
    data = payload.get("data")
    assert isinstance(data, list), f"list_views envelope drifted ({source=}): {payload}"
    return {v.get("name") for v in data if isinstance(v, dict)}


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
            delete_view(mcp_client, clone_id)
        if clone_id:
            assert view_gone(mcp_client, clone_id), f"clone view {clone_id} remained after cleanup"
        delete_view(mcp_client, source_id)
        assert view_gone(mcp_client, source_id), f"source view {source_id} remained after cleanup"


def test_list_views_without_source_spans_explorers(mcp_client: MCPClient, test_id: str) -> None:
    """Omitting source lists every Explorer's views, including sources beyond the old enum.

    The source enum left the schema for an open set SigNoz validates: the
    descriptions name "ai_observability" as a valid source (the hand-copied
    enum used to reject it) and teach that omitting source lists views from
    every Explorer. If SigNoz stops accepting the source or scoping the list,
    that teaching is stale and must change with this pin.
    """
    logs_name = f"mcp-e2e-logs-{test_id}"
    ai_name = f"mcp-e2e-ai-{test_id}"
    logs_id = create_view(mcp_client, logs_name)
    ai_id = ""
    try:
        ai_id = create_view(
            mcp_client,
            ai_name,
            source="ai_observability",
            spec={
                "displayName": ai_name,
                "panelType": "list",
                "requestType": "raw",
                "queries": [{"type": "builder_query", "spec": {"name": "A", "signal": "traces", "limit": 100}}],
            },
        )
        listed = assert_tool_ok(
            mcp_client.call_tool("signoz_list_views", {"searchContext": "list saved views across all explorers"})
        )
        names = listed_view_names(listed)
        assert logs_name in names and ai_name in names, (
            f"omitted source must span explorers; missing {logs_name!r} or {ai_name!r} in {sorted(names)[:20]}"
        )
    finally:
        for view_id in (ai_id, logs_id):
            if view_id:
                delete_view(mcp_client, view_id)
                assert view_gone(mcp_client, view_id), f"view {view_id} remained after cleanup"


def test_update_view_source_change_moves_view(mcp_client: MCPClient, test_id: str) -> None:
    """Changing source on update is forwarded, and SigNoz moves the view to that Explorer.

    The old update guard rejected a source change client-side;
    signoz://view/instructions now teaches that SigNoz accepts it and the view
    moves. Pinned: after updating a logs view to traces, the traces listing
    carries it and the logs listing does not.
    """
    name = f"mcp-e2e-move-{test_id}"
    view_id = create_view(mcp_client, name)
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool("signoz_get_view", {"searchContext": f"get view {view_id}", "id": view_id})
        )
        view = {k: v for k, v in extract_view_data(fetched).items() if k not in ("id", "createdAt", "updatedAt")}
        view["source"] = "traces"
        for query in view.get("spec", {}).get("queries", []):
            if isinstance(query, dict) and isinstance(query.get("spec"), dict):
                query["spec"]["signal"] = "traces"

        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_update_view",
                {"searchContext": f"move view {view_id} to traces", "id": view_id, "view": view},
            )
        )

        traces_names = listed_view_names(
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_list_views", {"searchContext": "traces explorer views", "source": "traces"}
                )
            ),
            source="traces",
        )
        logs_names = listed_view_names(
            assert_tool_ok(
                mcp_client.call_tool("signoz_list_views", {"searchContext": "logs explorer views", "source": "logs"})
            ),
            source="logs",
        )
        assert name in traces_names, f"view did not move to traces explorer: {sorted(traces_names)[:20]}"
        assert name not in logs_names, "view still listed under logs after the source change"
    finally:
        delete_view(mcp_client, view_id)
        assert view_gone(mcp_client, view_id), f"view {view_id} remained after cleanup"
