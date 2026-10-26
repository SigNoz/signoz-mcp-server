"""The upstream request examples the signoz://*/examples resources publish work on this release.

`make gen` renders those resources from the request examples in the pinned SigNoz
OpenAPI document, and the tool descriptions tell an agent to copy them. These
tests read each resource over MCP, send every example through the tool it is
meant for, and delete what they create, so a release whose validator rejects a
published example fails here instead of in front of a user. Alert examples route
to illustrative channel displayNames, so those channels are created first.
"""

import json
import re
import time
from collections.abc import Iterator

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import first_block_json
from fixtures.seeded import channel_gone, create_channel, delete_channel, delete_view, view_gone

HEADING = re.compile(r"^(#{2,3}) (.+)$")


def examples_in(resource_text: str) -> dict[str, list[tuple[str, object]]]:
    """Map each H2 section to its (H3 title, JSON body) examples."""
    sections: dict[str, list[tuple[str, object]]] = {}
    section, example, fence = "", "", None
    for line in resource_text.splitlines():
        if fence is not None:
            if line.startswith("```"):
                sections.setdefault(section, []).append((example, json.loads("\n".join(fence))))
                fence = None
            else:
                fence.append(line)
            continue
        match = HEADING.match(line)
        if match and match.group(1) == "##":
            section, example = match.group(2), ""
        elif match:
            example = match.group(2)
        elif line.startswith("```json"):
            fence = []
    return sections


def section(resource_text: str, prefix: str) -> list[tuple[str, object]]:
    found = [body for title, body in examples_in(resource_text).items() if title.startswith(prefix)]
    assert found, f"no section starting with {prefix!r} in resource"
    return found[0]


def slug(test_id: str, suffix: str) -> str:
    return f"e2e-{test_id}-{suffix}".lower()[:63].strip("-")


def channel_names(rule: dict) -> set[str]:
    names = set(rule.get("preferredChannels") or [])
    for tier in ((rule.get("condition") or {}).get("thresholds") or {}).get("spec") or []:
        names.update(tier.get("channels") or [])
    return names


@pytest.fixture
def routing_channels(mcp_client: MCPClient, test_id: str) -> Iterator[dict[str, str]]:
    """Webhook channels named exactly as the alert examples route to."""
    rules = section(mcp_client.read_resource("signoz://alert/examples"), "Create a rule")
    addendum = section(mcp_client.read_resource("signoz://alert/examples"), "Cost Meter budget alert")
    names: set[str] = set()
    for _, rule in rules + addendum:
        names |= channel_names(rule)
    created: dict[str, str] = {}
    try:
        for index, display_name in enumerate(sorted(names)):
            result = assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_create_notification_channel",
                    {
                        "searchContext": f"create routing channel {display_name}",
                        "name": slug(test_id, f"ch{index}"),
                        "displayName": display_name,
                        "config": {"kind": "webhook", "spec": {"url": "http://example.invalid/e2e-routing"}},
                        "test": False,
                    },
                )
            )
            created[display_name] = (first_block_json(result).get("data") or {}).get("id")
        yield created
    finally:
        for channel_id in created.values():
            if channel_id:
                delete_channel(mcp_client, channel_id)
                assert channel_gone(mcp_client, channel_id)


def test_alert_examples_create_and_delete(mcp_client: MCPClient, routing_channels: dict[str, str]) -> None:
    text = mcp_client.read_resource("signoz://alert/examples")
    examples = section(text, "Create a rule") + section(text, "Cost Meter budget alert")
    for title, rule in examples:
        result = mcp_client.call_tool("signoz_create_alert", {"searchContext": f"create {title}", **rule})
        assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
        rule_id = (first_block_json(result).get("data") or {}).get("id")
        assert rule_id, f"{title}: create returned no id"
        assert_tool_ok(mcp_client.call_tool("signoz_delete_alert", {"searchContext": f"delete {title}", "id": rule_id}))


def test_view_examples_create_replace_and_delete(mcp_client: MCPClient, test_id: str) -> None:
    text = mcp_client.read_resource("signoz://view/examples")
    created: list[str] = []
    try:
        for index, (title, view) in enumerate(section(text, "Create a view")):
            if not view.get("generateName"):
                view = {**view, "name": slug(test_id, f"v{index}")}
            result = mcp_client.call_tool("signoz_create_view", {"searchContext": f"create {title}", **view})
            assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
            view_id = (first_block_json(result).get("data") or {}).get("id")
            assert view_id, f"{title}: create returned no id"
            created.append(view_id)
        for title, body in section(text, "Replace a view"):
            result = mcp_client.call_tool(
                "signoz_update_view", {"searchContext": f"apply {title}", "id": created[0], "view": body}
            )
            assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
    finally:
        for view_id in created:
            delete_view(mcp_client, view_id)
            assert view_gone(mcp_client, view_id)


def _dashboard_id(result: dict) -> str:
    payload = first_block_json(result)
    data = payload.get("data") if isinstance(payload, dict) else None
    dashboard_id = (data or payload).get("id") if isinstance(data or payload, dict) else None
    assert isinstance(dashboard_id, str) and dashboard_id, f"dashboard response carried no id: {payload}"
    return dashboard_id


def test_dashboard_examples_create_replace_patch_and_delete(mcp_client: MCPClient) -> None:
    text = mcp_client.read_resource("signoz://dashboard/examples")
    created: list[str] = []
    try:
        for title, dashboard in section(text, "Create a dashboard"):
            body = {k: v for k, v in dashboard.items() if k != "name"} | {"generateName": True}
            result = mcp_client.call_tool("signoz_create_dashboard", {"searchContext": f"create {title}", **body})
            assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
            created.append(_dashboard_id(result))
        target = created[0]
        current = first_block_json(
            assert_tool_ok(mcp_client.call_tool("signoz_get_dashboard", {"searchContext": "read", "id": target}))
        )
        current_name = (current.get("data") or current).get("name")
        for title, body in section(text, "Replace a dashboard"):
            result = mcp_client.call_tool(
                "signoz_update_dashboard",
                {"searchContext": f"apply {title}", "id": target, **body, "name": current_name},
            )
            assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
        for title, patch in section(text, "Patch a dashboard"):
            result = mcp_client.call_tool(
                "signoz_patch_dashboard", {"searchContext": f"apply {title}", "id": target, "patch": patch}
            )
            assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
    finally:
        for dashboard_id in created:
            assert_tool_ok(
                mcp_client.call_tool("signoz_delete_dashboard", {"searchContext": "cleanup", "id": dashboard_id})
            )


def test_channel_examples_create_replace_and_delete(mcp_client: MCPClient, test_id: str) -> None:
    text = mcp_client.read_resource("signoz://notification-channel/examples")
    created: list[str] = []
    try:
        for index, (title, channel) in enumerate(section(text, "Create a channel")):
            body = {**channel, "name": slug(test_id, f"c{index}"), "displayName": slug(test_id, f"c{index}")}
            body.pop("generateName", None)
            created.append(create_channel(mcp_client, body["name"], body["config"]))
        slack_id = next(
            cid
            for cid, (_, channel) in zip(created, section(text, "Create a channel"))
            if channel["config"]["kind"] == "slack"
        )
        for title, body in section(text, "Replace a channel"):
            # The upstream PUT body is {config: {...}}; the tool takes its config object.
            result = mcp_client.call_tool(
                "signoz_update_notification_channel",
                {"searchContext": f"apply {title}", "id": slack_id, "config": body["config"], "test": False},
            )
            assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
    finally:
        for channel_id in created:
            delete_channel(mcp_client, channel_id)
            assert channel_gone(mcp_client, channel_id)


def test_query_builder_examples_execute(mcp_client: MCPClient) -> None:
    text = mcp_client.read_resource("signoz://query-builder/examples")
    end = int(time.time() * 1000)
    start = end - 30 * 60 * 1000
    for title, query in section(text, "Run a query"):
        body = {**query, "start": start, "end": end}
        result = mcp_client.call_tool("signoz_execute_builder_query", {"searchContext": f"run {title}", "query": body})
        assert not result.get("isError", False), f"{title}: {text_blocks(result)[:600]}"
