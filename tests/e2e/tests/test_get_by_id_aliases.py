"""Get-by-id aliases — port of Family E K5 reads.

The canonical id param and the legacy alias (ruleId / uuid) both reach the
backend. The Go test read whatever staging had; here the resources are seeded
so both alias paths always run.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import dig_id, first_block_json
from fixtures.seeded import create_alert_rule, create_channel, delete_alert_rule, delete_channel


def test_get_alert_by_id_and_legacy(mcp_client: MCPClient, test_id: str) -> None:
    channel_name = f"mcp-e2e-ch-{test_id}"
    channel_id = create_channel(mcp_client, channel_name)
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
        delete_channel(mcp_client, channel_id)


def test_get_dashboard_by_id_and_legacy(mcp_client: MCPClient, test_id: str) -> None:
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
        for key in ("id", "uuid"):
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_get_dashboard", {"searchContext": f"get dashboard {dashboard_id}", key: dashboard_id}
                )
            )
    finally:
        mcp_client.call_tool(
            "signoz_delete_dashboard", {"searchContext": f"cleanup dashboard {dashboard_id}", "id": dashboard_id}
        )
