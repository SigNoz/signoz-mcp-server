"""Get-by-id canonical inputs and retained alert/view aliases.

Alert ruleId remains supported. Dashboard uuid was removed by the v6 hard cut
and must fail clearly instead of reaching the backend.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import dig_id, first_block_json, first_text_block, result_code
from fixtures.seeded import (
    alert_rule_gone,
    channel_gone,
    create_alert_rule,
    create_channel,
    delete_alert_rule,
    delete_channel,
)


def test_get_alert_by_id_and_legacy(mcp_client: MCPClient, test_id: str) -> None:
    channel_name = f"mcp-e2e-ch-{test_id}"
    channel_id = create_channel(
        mcp_client,
        channel_name,
        {"kind": "webhook", "spec": {"url": "http://example.invalid/no-send"}},
    )
    rule_id = None
    try:
        rule_id = create_alert_rule(mcp_client, f"mcp-e2e-rule-{test_id}", channel_name=channel_name)
        for key in ("id", "ruleId"):
            assert_tool_ok(
                mcp_client.call_tool("signoz_get_alert", {"searchContext": f"get rule {rule_id}", key: rule_id})
            )
    finally:
        if rule_id is not None:
            delete_alert_rule(mcp_client, rule_id)
            assert alert_rule_gone(mcp_client, rule_id), f"alert rule {rule_id} remained after cleanup"
        delete_channel(mcp_client, channel_id)
        assert channel_gone(mcp_client, channel_id), f"channel {channel_id} remained after cleanup"


def test_get_dashboard_by_id_and_removed_uuid(mcp_client: MCPClient, test_id: str) -> None:
    created = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_create_dashboard",
            {
                "searchContext": f"create dashboard mcp-e2e-{test_id}",
                "schemaVersion": "v6",
                "tags": [],
                "spec": {"display": {"name": f"mcp-e2e-{test_id}"}, "variables": [], "panels": {}, "layouts": []},
            },
        )
    )
    dashboard_id = dig_id(first_block_json(created))
    assert dashboard_id, "could not extract dashboard id from create response"
    try:
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_dashboard", {"searchContext": f"get dashboard {dashboard_id}", "id": dashboard_id}
            )
        )
        removed = mcp_client.call_tool(
            "signoz_get_dashboard", {"searchContext": "reject the removed dashboard uuid alias", "uuid": dashboard_id}
        )
        assert removed.get("isError", False)
        assert '"uuid" is no longer accepted' in first_text_block(removed)
        assert result_code(removed) == "VALIDATION_FAILED"
    finally:
        assert_tool_ok(
            mcp_client.call_tool(
                "signoz_delete_dashboard",
                {"searchContext": f"cleanup dashboard {dashboard_id}", "id": dashboard_id},
            )
        )
        gone = mcp_client.call_tool(
            "signoz_get_dashboard",
            {"searchContext": f"confirm dashboard {dashboard_id} gone", "id": dashboard_id},
        )
        assert gone.get("isError", False), f"dashboard {dashboard_id} remained after cleanup"
