"""Helpers for seeding SigNoz resources through the MCP tools themselves.

Every create returns the new resource's id; callers own deletion (usually via
a try/finally in the test). Used by suites whose assertions need an existing
rule, view, or channel — the fresh cast has none, where the Go staging tests
silently skipped.
"""

import json

from fixtures.logger import setup_logger
from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import dig_id, first_block_json, first_text_block

logger = setup_logger(__name__)


def create_channel(client: MCPClient, name: str, config: dict) -> str:
    """Create a canonical v2 notification channel; return its id."""
    result = assert_tool_ok(
        client.call_tool(
            "signoz_create_notification_channel",
            {
                "searchContext": f"create a webhook channel named {name}",
                "name": name,
                "displayName": name,
                "config": config,
                "test": False,
            },
        )
    )
    payload = first_block_json(result)
    test_notification = payload.get("testNotification", {})
    assert test_notification.get("requested") is False
    assert test_notification.get("status") == "skipped"
    channel_id = dig_id(payload)
    assert channel_id, f"could not extract channel id from create response: {first_text_block(result)[:400]}"
    logger.info("created channel id=%s name=%s", channel_id, name)
    return channel_id


def delete_channel(client: MCPClient, channel_id: str) -> None:
    assert_tool_ok(
        client.call_tool(
            "signoz_delete_notification_channel",
            {"searchContext": f"delete channel {channel_id}", "id": channel_id},
        )
    )


def channel_gone(client: MCPClient, channel_id: str) -> bool:
    result = client.call_tool(
        "signoz_get_notification_channel", {"searchContext": f"confirm channel {channel_id} gone", "id": channel_id}
    )
    return bool(result.get("isError", False))


def channel_id_by_name(client: MCPClient, name: str) -> str:
    """Best-effort id recovery by name (cleanup backstop; never raises)."""
    try:
        result = client.call_tool(
            "signoz_list_notification_channels",
            {"searchContext": f"find channel {name}", "query": name, "limit": 200},
        )
        if result.get("isError", False):
            return ""
        payload = first_block_json(result)
        data = payload.get("data", payload) if isinstance(payload, dict) else {}
        channels = data.get("channels", []) if isinstance(data, dict) else []
        if not isinstance(channels, list):
            channels = []
        for item in channels:
            if isinstance(item, dict) and item.get("name") == name:
                value = item.get("id")
                if isinstance(value, str):
                    return value
    except Exception as err:  # noqa: BLE001 — a backstop must never raise
        logger.warning("channel id recovery for %s failed: %s", name, err)
    return ""


def create_alert_rule(client: MCPClient, name: str, *, channel_name: str) -> str:
    """Create a logs threshold alert rule routed to `channel_name`; return its id."""
    result = assert_tool_ok(
        client.call_tool(
            "signoz_create_alert",
            {
                "searchContext": f"create an alert rule named {name}",
                "alert": name,
                "alertType": "LOGS_BASED_ALERT",
                "ruleType": "threshold_rule",
                "condition": {
                    "thresholds": {
                        "kind": "basic",
                        "spec": [
                            {
                                "name": "critical",
                                "target": 1000000,
                                "matchType": "at_least_once",
                                "op": "above",
                                "channels": [channel_name],
                            }
                        ],
                    },
                    "compositeQuery": {
                        "queryType": "builder",
                        "panelType": "graph",
                        "queries": [
                            {
                                "type": "builder_query",
                                "spec": {"name": "A", "signal": "logs", "aggregations": [{"expression": "count()"}]},
                            }
                        ],
                    },
                    "selectedQueryName": "A",
                },
                "labels": {"severity": "critical"},
                "annotations": {"summary": "e2e seeded rule"},
            },
        )
    )
    rule_id = dig_id(first_block_json(result))
    assert rule_id, f"could not extract rule id from create response: {first_text_block(result)[:400]}"
    logger.info("created alert rule id=%s name=%s", rule_id, name)
    return rule_id


def delete_alert_rule(client: MCPClient, rule_id: str) -> None:
    assert_tool_ok(
        client.call_tool(
            "signoz_delete_alert",
            {"searchContext": f"delete alert rule {rule_id}", "id": rule_id},
        )
    )


def alert_rule_gone(client: MCPClient, rule_id: str) -> bool:
    result = client.call_tool(
        "signoz_get_alert", {"searchContext": f"confirm alert rule {rule_id} gone", "id": rule_id}
    )
    return bool(result.get("isError", False))


def create_view(client: MCPClient, name: str, *, source: str = "logs", spec: dict | None = None) -> str:
    """Create a saved view; return its id."""
    result = assert_tool_ok(
        client.call_tool(
            "signoz_create_view",
            {
                "searchContext": f"create a saved view named {name}",
                "name": name,
                "source": source,
                "schemaVersion": "v2",
                "spec": spec
                or {
                    "displayName": name,
                    "panelType": "list",
                    "requestType": "raw",
                    "queries": [{"type": "builder_query", "spec": {"name": "A", "signal": source, "limit": 100}}],
                },
            },
        )
    )
    view_id = dig_id(first_block_json(result))
    assert view_id, f"could not extract view id from create response: {first_text_block(result)[:400]}"
    logger.info("created view id=%s name=%s", view_id, name)
    return view_id


def delete_view(client: MCPClient, view_id: str, *, key: str = "id") -> None:
    assert_tool_ok(
        client.call_tool(
            "signoz_delete_view",
            {"searchContext": f"delete view {view_id}", key: view_id},
        )
    )


def view_gone(client: MCPClient, view_id: str) -> bool:
    result = client.call_tool("signoz_get_view", {"searchContext": f"confirm view {view_id} gone", "id": view_id})
    return bool(result.get("isError", False))


def extract_view_data(result: dict) -> dict:
    """The saved-view data object from a get_view response (port of extractViewData)."""
    payload = json.loads(first_text_block(result))
    data = payload.get("data")
    if isinstance(data, dict) and data:
        return data
    return payload


def extract_view_name(result: dict) -> str:
    """The saved-view name from a get_view response (port of extractViewName)."""
    payload = json.loads(first_text_block(result))
    data = payload.get("data")
    if isinstance(data, dict) and isinstance(data.get("name"), str):
        return data["name"]
    name = payload.get("name")
    return name if isinstance(name, str) else ""
