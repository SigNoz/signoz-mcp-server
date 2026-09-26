"""Canonical notification-channel v2 lifecycle checks against a live SigNoz."""

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok, text_blocks
from fixtures.results import first_block_json
from fixtures.seeded import channel_gone, channel_id_by_name, create_channel, delete_channel
from fixtures.webhooksink import WebhookSink

CHANNEL_SPECS: dict[str, dict] = {
    "slack": {"apiUrl": "https://example.invalid/e2e-slack"},
    "email": {"to": "e2e@example.invalid"},
    "webhook": {"url": "http://example.invalid/e2e-webhook"},
    "pagerduty": {"routingKey": "e2e-pagerduty-routing-key"},
    "opsgenie": {"apiKey": "e2e-opsgenie-api-key"},
    "msteams": {"webhookUrl": "https://example.invalid/e2e-msteams"},
    "googlechat": {"webhookUrl": "https://chat.googleapis.com/v1/spaces/AAA/messages?key=e2e&token=e2e"},
    "jira": {
        "site": "https://e2e.atlassian.net",
        "project": "E2E",
        "issueType": "Task",
        "email": "e2e@example.invalid",
        "apiToken": "e2e-jira-token",
    },
    "jsmops": {"apiKey": "e2e-jsm-ops-api-key"},
    "incidentio": {
        "url": "https://api.incident.io/v2/alert_events/http/01M0D1JNVBGBGVTWX053EM12XV",
        "token": "e2e-incident-token",
    },
}

UPDATE_FIELDS = {
    "slack": ("title", "Updated Slack title"),
    "email": ("to", "updated-e2e@example.invalid"),
    "webhook": ("url", "http://example.invalid/e2e-webhook-updated"),
    "pagerduty": ("description", "Updated PagerDuty description"),
    "opsgenie": ("message", "Updated Opsgenie message"),
    "msteams": ("title", "Updated Teams title"),
    "googlechat": ("title", "Updated Google Chat title"),
    "jira": ("summary", "Updated Jira summary"),
    "jsmops": ("message", "Updated JSM Ops message"),
    "incidentio": ("title", "Updated incident.io title"),
}

CREDENTIAL_FIELDS = {
    "pagerduty": ("routingKey",),
    "opsgenie": ("apiKey",),
    "jira": ("email", "apiToken"),
    "jsmops": ("apiKey",),
    "incidentio": ("token",),
}

EMPTY_OPTIONAL_FIELDS = {
    "slack": "channel",
    "email": "html",
    "webhook": "username",
    "pagerduty": "source",
    "opsgenie": "description",
    "msteams": "text",
    "googlechat": "text",
    "jira": "description",
    "jsmops": "description",
    "incidentio": "description",
}


def _channel_data(result: dict) -> dict:
    payload = first_block_json(result)
    data = payload.get("data", payload)
    assert isinstance(data, dict), f"notification response has no data object: {text_blocks(result)[:500]}"
    return data


@pytest.mark.parametrize("kind", CHANNEL_SPECS)
def test_every_provider_config_round_trips_and_update_preserves_it(
    mcp_client: MCPClient, test_id: str, kind: str
) -> None:
    """Create/get/update/list for each v2 provider with test:false (no send)."""
    name = f"mcp-e2e-{kind}-{test_id}".lower().replace("_", "-")
    spec = dict(CHANNEL_SPECS[kind])
    spec["sendResolved"] = False
    channel_id = ""
    try:
        channel_id = create_channel(mcp_client, name, {"kind": kind, "spec": spec})

        fetched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_notification_channel",
                {"searchContext": f"get the {kind} channel {channel_id}", "id": channel_id},
            )
        )
        got = _channel_data(fetched)
        assert got["name"] == name
        assert got["displayName"] == name
        returned_config = got["config"]
        assert returned_config["kind"] == kind
        for field, value in spec.items():
            assert returned_config["spec"][field] == value, f"{kind}.{field} did not round-trip"
        returned_spec = returned_config["spec"]
        credentials = {field: returned_spec[field] for field in CREDENTIAL_FIELDS.get(kind, ())}

        # Use the complete real GET response as the update base. It includes
        # unset optional strings serialized as ""; update must accept those
        # response values and preserve credentials.
        replacement = {"kind": returned_config["kind"], "spec": dict(returned_spec)}
        update_field, updated_value = UPDATE_FIELDS[kind]
        replacement["spec"][update_field] = updated_value
        empty_field = EMPTY_OPTIONAL_FIELDS[kind]
        replacement["spec"][empty_field] = ""

        updated = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_update_notification_channel",
                {
                    "searchContext": f"update one field on the {kind} channel",
                    "id": channel_id,
                    "config": replacement,
                    "test": False,
                },
            )
        )
        updated_payload = _channel_data(updated)
        assert updated_payload["mutationCommitted"] is True
        assert updated_payload["testNotification"] == {"requested": False, "status": "skipped"}
        updated_config = updated_payload["channel"]["config"]
        assert updated_config["kind"] == kind
        assert updated_config["spec"][update_field] == updated_value
        assert updated_config["spec"]["sendResolved"] is False
        for field, value in returned_spec.items():
            if field not in (update_field, empty_field) and value != "":
                assert updated_config["spec"][field] == value, f"{kind}.{field} changed during full-config update"
        for field, value in credentials.items():
            assert updated_config["spec"][field] == value, f"{kind}.{field} was not preserved by full-config update"

        listing = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_list_notification_channels",
                {"searchContext": f"verify {name} appears in a config-free list", "query": name},
            )
        )
        list_data = _channel_data(listing)
        channels = list_data["channels"]
        assert isinstance(list_data["total"], int)
        pagination = list_data["pagination"]
        assert pagination["limit"] == 20
        assert pagination["offset"] == 0
        assert pagination["count"] == len(channels)
        assert isinstance(pagination["hasMore"], bool)
        assert pagination["nextOffset"] is None or isinstance(pagination["nextOffset"], int)
        rows = [row for row in channels if row.get("id") == channel_id]
        assert len(rows) == 1
        assert "config" not in rows[0]
    finally:
        if channel_id:
            delete_channel(mcp_client, channel_id)
            assert channel_gone(mcp_client, channel_id), f"{kind} channel remained after cleanup"
        else:
            recovered = channel_id_by_name(mcp_client, name)
            if recovered:
                delete_channel(mcp_client, recovered)
                assert channel_gone(mcp_client, recovered), f"recovered {kind} channel remained after cleanup"


def test_local_webhook_test_is_opt_in_and_reaches_only_local_sink(
    mcp_client: MCPClient, test_id: str, local_webhook_sink: WebhookSink
) -> None:
    name = f"mcp-e2e-local-webhook-{test_id}".lower()
    channel_id = ""
    try:
        channel_id = create_channel(
            mcp_client,
            name,
            {"kind": "webhook", "spec": {"url": "http://example.invalid/no-send", "sendResolved": False}},
        )
        assert local_webhook_sink.captured_requests() == []

        config = {"kind": "webhook", "spec": {"url": local_webhook_sink.url, "sendResolved": False}}
        updated = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_update_notification_channel",
                {
                    "searchContext": "update and send one test to the explicitly opt-in local sink",
                    "id": channel_id,
                    "config": config,
                    "test": True,
                },
            )
        )
        test_notification = _channel_data(updated).get("testNotification")
        assert test_notification == {"requested": True, "success": True, "status": "succeeded"}, (
            f"unexpected test notification outcome: {test_notification!r}"
        )
        captured = local_webhook_sink.captured_requests()
        assert len(captured) == 1, f"expected exactly one local sink request, got {captured!r}"
        assert captured[0]["path"] == "/e2e"
    finally:
        if channel_id:
            delete_channel(mcp_client, channel_id)
            assert channel_gone(mcp_client, channel_id), "local webhook channel remained after cleanup"
        else:
            recovered = channel_id_by_name(mcp_client, name)
            if recovered:
                delete_channel(mcp_client, recovered)
                assert channel_gone(mcp_client, recovered), "recovered local webhook channel remained after cleanup"


def test_slack_message_settings_round_trip_through_get_and_full_update(mcp_client: MCPClient, test_id: str) -> None:
    """Slack attachment settings survive create, get, and an unchanged full-config update."""
    name = f"mcp-e2e-slack-message-{test_id}".lower().replace("_", "-")
    spec = {
        "apiUrl": "https://example.invalid/e2e-slack-message",
        "sendResolved": False,
        "color": "danger",
        "footer": "SigNoz e2e",
        "fields": [{"title": "Service", "value": "checkout", "short": True}],
        "actions": [{"type": "button", "text": "Runbook", "url": "https://example.invalid/runbook"}],
    }
    channel_id = ""
    try:
        channel_id = create_channel(mcp_client, name, {"kind": "slack", "spec": spec})
        fetched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_notification_channel",
                {"searchContext": f"get the slack channel {channel_id}", "id": channel_id},
            )
        )
        returned_spec = _channel_data(fetched)["config"]["spec"]
        assert returned_spec["color"] == "danger"
        assert returned_spec["footer"] == "SigNoz e2e"
        assert returned_spec["fields"] == spec["fields"]
        action = returned_spec["actions"][0]
        assert (action["type"], action["text"], action["url"]) == (
            "button",
            "Runbook",
            "https://example.invalid/runbook",
        )

        updated = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_update_notification_channel",
                {
                    "searchContext": "update the slack channel footer and keep its message settings",
                    "id": channel_id,
                    "config": {"kind": "slack", "spec": {**returned_spec, "footer": "SigNoz e2e updated"}},
                    "test": False,
                },
            )
        )
        updated_spec = _channel_data(updated)["channel"]["config"]["spec"]
        assert updated_spec["footer"] == "SigNoz e2e updated"
        assert updated_spec["color"] == "danger"
        assert updated_spec["fields"] == spec["fields"]
        assert updated_spec["actions"][0]["url"] == "https://example.invalid/runbook"
    finally:
        if channel_id:
            delete_channel(mcp_client, channel_id)
            assert channel_gone(mcp_client, channel_id), f"slack channel {channel_id} remained after cleanup"
