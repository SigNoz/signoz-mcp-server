"""Alert-rule contract against real SigNoz — the taught v2alpha1 authoring flow.

The MCP server forwards alert payloads verbatim; SigNoz is the only validator.
These tests pin the claims the tool descriptions and signoz://alert resources
publish against the pinned release: the explicit v2alpha1 payload the
instructions teach round-trips through create/get/update/delete, SigNoz rejects
direct routing to a channel that does not exist, and the history endpoint keeps
the items/total envelope the completeness note parses.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, first_text_block

SERVER_POPULATED_FIELDS = ("id", "createdAt", "updatedAt", "createdBy", "updatedBy", "webUrl")


def threshold_rule(name: str, *, alert_type: str = "METRIC_BASED_ALERT", channels: list | None = None) -> dict:
    """A minimal explicit v2alpha1 threshold rule, as the instructions teach it.

    Policy routing (usePolicy=true) keeps the rule channel-free; passing
    channels switches the tier to direct routing instead.
    """
    tier: dict = {"name": "warning", "op": "above", "matchType": "at_least_once", "target": 99}
    notification_settings: dict = {"usePolicy": True}
    if channels is not None:
        tier["channels"] = channels
        notification_settings = {"usePolicy": False}
    return {
        "alert": name,
        "alertType": alert_type,
        "ruleType": "threshold_rule",
        "schemaVersion": "v2alpha1",
        "labels": {"severity": "warning"},
        "condition": {
            "compositeQuery": {
                "queryType": "builder",
                "panelType": "graph",
                "queries": [
                    {
                        "type": "builder_query",
                        "spec": {
                            "name": "A",
                            "signal": "metrics",
                            "stepInterval": 60,
                            "aggregations": [
                                {"metricName": "system.cpu.time", "timeAggregation": "rate", "spaceAggregation": "sum"}
                            ],
                        },
                    }
                ],
            },
            "selectedQueryName": "A",
            "thresholds": {"kind": "basic", "spec": [tier]},
        },
        "evaluation": {"kind": "rolling", "spec": {"evalWindow": "5m", "frequency": "1m"}},
        "notificationSettings": notification_settings,
    }


def create_rule(client: MCPClient, rule: dict) -> str:
    result = client.call_tool("signoz_create_alert", {"searchContext": f"create alert {rule['alert']}", **rule})
    assert_tool_ok(result)
    payload = first_block_json(result)
    rule_id = (payload.get("data") or {}).get("id")
    assert isinstance(rule_id, str) and rule_id, f"create_alert returned no rule id: {payload}"
    return rule_id


def delete_rule(client: MCPClient, rule_id: str) -> None:
    assert_tool_ok(client.call_tool("signoz_delete_alert", {"searchContext": f"delete rule {rule_id}", "id": rule_id}))


def listed_rule_names(client: MCPClient) -> set:
    result = client.call_tool("signoz_list_alert_rules", {"searchContext": "list alert rules", "limit": "1000"})
    assert_tool_ok(result)
    payload = first_block_json(result)
    data = payload.get("data")
    assert isinstance(data, list), f"list_alert_rules envelope drifted: {payload}"
    return {r.get("alert") for r in data if isinstance(r, dict)}


def test_alert_crud_round_trip(mcp_client: MCPClient, test_id: str) -> None:
    """The explicit v2alpha1 payload the instructions teach survives create/get/update/delete."""
    name = f"mcp-e2e-{test_id}"
    rule_id = create_rule(mcp_client, threshold_rule(name))
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool("signoz_get_alert", {"searchContext": f"get rule {name}", "id": rule_id})
        )
        rule = first_block_json(fetched).get("data")
        assert isinstance(rule, dict), f"get_alert envelope drifted: {fetched}"
        assert rule.get("alert") == name
        assert rule.get("alertType") == "METRIC_BASED_ALERT"
        assert rule.get("schemaVersion") == "v2alpha1", f"schemaVersion did not round-trip: {rule.get('schemaVersion')}"

        # The taught update flow: full replacement from the get result, minus
        # the server-populated fields the instructions say to omit.
        updated = {k: v for k, v in rule.items() if k not in SERVER_POPULATED_FIELDS}
        updated["description"] = "updated by e2e"
        result = mcp_client.call_tool(
            "signoz_update_alert", {"searchContext": f"update rule {name}", "id": rule_id, **updated}
        )
        assert_tool_ok(result)

        refetched = assert_tool_ok(
            mcp_client.call_tool("signoz_get_alert", {"searchContext": f"re-get rule {name}", "id": rule_id})
        )
        assert first_block_json(refetched)["data"].get("description") == "updated by e2e"
    finally:
        delete_rule(mcp_client, rule_id)
    assert name not in listed_rule_names(mcp_client), f"deleted rule {name} still listed"


def test_create_alert_unknown_channel_rejected(mcp_client: MCPClient, test_id: str) -> None:
    """SigNoz rejects direct routing to a channel that does not exist, as the descriptions teach."""
    name = f"mcp-e2e-{test_id}-ghost-channel"
    result = mcp_client.call_tool(
        "signoz_create_alert",
        {
            "searchContext": "create an alert routed to a channel that does not exist",
            **threshold_rule(name, channels=["mcp-e2e-no-such-channel"]),
        },
    )
    if not result.get("isError", False):
        # Unexpected acceptance must not leak a rule into reused environments.
        leaked = (first_block_json(result).get("data") or {}).get("id")
        if isinstance(leaked, str) and leaked:
            delete_rule(mcp_client, leaked)
        raise AssertionError(f"SigNoz accepted an unknown channel: {first_text_block(result)[:300]}")
    body = first_text_block(result)
    assert body.startswith("SigNoz API error:"), f"channel rejection lost the uniform prefix: {body!r}"
    assert "mcp-e2e-no-such-channel" in body, f"rejection does not name the unknown channel: {body!r}"
    assert name not in listed_rule_names(mcp_client), "rejected rule was persisted anyway"


def test_alert_history_envelope(mcp_client: MCPClient, test_id: str) -> None:
    """History keeps the data.items/total envelope the completeness note parses (empty timeline is fine)."""
    name = f"mcp-e2e-{test_id}-history"
    rule_id = create_rule(mcp_client, threshold_rule(name))
    try:
        result = mcp_client.call_tool(
            "signoz_get_alert_history", {"searchContext": f"history for {name}", "id": rule_id}
        )
        assert_tool_ok(result)
        data = first_block_json(result).get("data")
        assert isinstance(data, dict), f"alert history envelope drifted: {data}"
        assert "items" in data, f"history items field missing: {data}"
        assert isinstance(data["items"], list | None.__class__), f"history items shape drifted: {data}"
        assert isinstance(data.get("total"), int), f"history total shape drifted: {data}"
    finally:
        delete_rule(mcp_client, rule_id)
