"""Notification channel lifecycle — port of Family A N6.

A bad webhook must still create (fail-open) with the test_notification field in
the body and a WARNING note when the test-send fails; the normal lifecycle
round-trips the name server-side. Every created channel is deleted and
confirmed gone.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import dig_id, first_block_json, first_text_block, note_blocks
from fixtures.seeded import channel_gone, channel_id_by_name, create_channel, delete_channel


def test_channel_test_send_failure(mcp_client: MCPClient, test_id: str) -> None:
    """Bad-webhook create -> fail-open success + test_notification field (+ WARNING note if the send fails)."""
    name = f"mcp-e2e-a-{test_id}-badhook"
    channel_id = ""
    try:
        result = mcp_client.call_tool(
            "signoz_create_notification_channel",
            {
                "searchContext": f"create a channel named {name}",
                "type": "webhook",
                "name": name,
                "webhook_url": "http://127.0.0.1:1/mcp-e2e-bad-hook",
                "send_resolved": False,
            },
        )
        assert not result.get("isError", False), (
            f"N6: expected fail-open success, got IsError: {first_text_block(result)[:300]}"
        )
        channel_id = dig_id(first_block_json(result))
        assert channel_id, f"could not extract channel id from create response (name={name})"

        body = first_text_block(result)
        # The test-send may pass or fail depending on backend egress, but the
        # embedded test_notification field must be present either way.
        assert "test_notification" in body, "expected test_notification in body"
        if '"success":false' in body:
            assert "WARNING" in note_blocks(result), (
                f"N6: test failed but no prominent WARNING note: {note_blocks(result)!r}"
            )
    finally:
        if channel_id:
            delete_channel(mcp_client, channel_id)
            assert channel_gone(mcp_client, channel_id), f"channel {channel_id} still fetchable after cleanup delete"
        else:
            recovered = channel_id_by_name(mcp_client, name)
            if recovered:
                mcp_client.call_tool(
                    "signoz_delete_notification_channel", {"searchContext": "cleanup channel", "id": recovered}
                )


def test_normal_channel_lifecycle(mcp_client: MCPClient, test_id: str) -> None:
    """Normal create -> verify the name round-tripped server-side -> delete -> gone."""
    name = f"mcp-e2e-a-{test_id}-ok"
    channel_id = create_channel(mcp_client, name, send_resolved="true")
    try:
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_notification_channel", {"searchContext": f"get channel {channel_id}", "id": channel_id}
            )
        )
        assert name in first_text_block(result), (
            f"channel name {name!r} did not round-trip server-side: {first_text_block(result)[:300]}"
        )
    finally:
        delete_channel(mcp_client, channel_id)
        assert channel_gone(mcp_client, channel_id), f"channel {channel_id} still fetchable after delete"
