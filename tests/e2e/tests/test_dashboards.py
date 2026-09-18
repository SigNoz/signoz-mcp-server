"""Dashboard v6 TextPanel lifecycle and released system-dashboard contracts."""

from fixtures.mcpclient import MCPClient, assert_tool_ok, first_json, text_blocks
from fixtures.signoz import SigNoz


def _data(payload: dict) -> dict:
    value = payload.get("data", payload)
    assert isinstance(value, dict), f"dashboard response carried no data object: {payload}"
    return value


def _dashboard_id(payload: dict) -> str:
    data = _data(payload)
    dashboard_id = data.get("id") or data.get("uuid")
    assert isinstance(dashboard_id, str) and dashboard_id, f"no dashboard id in payload: {payload}"
    return dashboard_id


def _panel(data: dict, name: str) -> dict:
    panels = data["spec"]["panels"]
    assert isinstance(panels, dict), f"v6 panels must be a map, got {panels!r}"
    panel = panels[name]
    assert panel["kind"] == "Panel"
    assert panel["spec"]["plugin"]["kind"] == "signoz/TextPanel"
    assert panel["spec"]["queries"] == []
    return panel


def _reserved_keyword_name(item: object) -> str:
    if isinstance(item, str):
        return item
    if isinstance(item, dict):
        value = item.get("name") or item.get("field")
        return value if isinstance(value, str) else ""
    return ""


def test_text_panel_create_get_update_patch_defaults_layout_and_cleanup(mcp_client: MCPClient, test_id: str) -> None:
    title = f"mcp-e2e-textpanel-{test_id}"
    explicit_spec = {
        "mode": "markdown",
        "text": "# Runbook",
        "presentation": {"textAlign": "center", "verticalAlign": "bottom", "background": "#1A2b3C"},
        "headerOptions": {"hide": True},
    }
    dashboard = {
        "searchContext": f"create a dashboard named {title} with explicit and default text panels",
        "schemaVersion": "v6",
        "tags": [],
        "spec": {
            "display": {"name": title},
            "variables": [],
            "panels": {
                "explicit": {
                    "kind": "Panel",
                    "spec": {
                        "display": {"name": "Explicit text"},
                        "links": [],
                        "plugin": {"kind": "signoz/TextPanel", "spec": explicit_spec},
                        "queries": [],
                    },
                },
                "defaults": {
                    "kind": "Panel",
                    "spec": {
                        "display": {"name": "Defaults text"},
                        "links": [],
                        "plugin": {"kind": "signoz/TextPanel", "spec": {}},
                        "queries": [],
                    },
                },
            },
            "layouts": [
                {
                    "kind": "Grid",
                    "spec": {
                        "items": [
                            {"x": 0, "y": 0, "width": 6, "height": 6, "content": {"$ref": "#/spec/panels/explicit"}},
                            {"x": 6, "y": 0, "width": 6, "height": 6, "content": {"$ref": "#/spec/panels/defaults"}},
                        ]
                    },
                }
            ],
        },
    }

    created = assert_tool_ok(mcp_client.call_tool("signoz_create_dashboard", dashboard))
    dashboard_id = _dashboard_id(first_json(created))
    deleted = False
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_dashboard", {"searchContext": f"get dashboard {dashboard_id}", "id": dashboard_id}
            )
        )
        data = _data(first_json(fetched))
        assert data["spec"]["display"]["name"] == title
        got_explicit = _panel(data, "explicit")["spec"]["plugin"]["spec"]
        assert got_explicit == explicit_spec

        got_defaults = _panel(data, "defaults")["spec"]["plugin"]["spec"]
        assert got_defaults == {
            "mode": "markdown",
            "text": "",
            "presentation": {"textAlign": "left", "verticalAlign": "top"},
            "headerOptions": {"hide": False},
        }
        assert data["spec"]["layouts"] == dashboard["spec"]["layouts"]

        replacement = dict(dashboard)
        replacement.pop("searchContext")
        replacement["id"] = dashboard_id
        replacement["name"] = data["name"]
        replacement["spec"] = data["spec"]
        replacement["spec"]["panels"]["explicit"]["spec"]["plugin"]["spec"] = {
            **explicit_spec,
            "text": "# Updated runbook",
            "presentation": {"textAlign": "right", "verticalAlign": "center", "background": "#244B57"},
        }
        updated = assert_tool_ok(mcp_client.call_tool("signoz_update_dashboard", replacement))
        updated_data = _data(first_json(updated))
        assert (
            updated_data["spec"]["panels"]["explicit"]["spec"]["plugin"]["spec"]
            == replacement["spec"]["panels"]["explicit"]["spec"]["plugin"]["spec"]
        )
        assert updated_data["spec"]["layouts"] == dashboard["spec"]["layouts"]

        patched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_patch_dashboard",
                {
                    "searchContext": "patch only the explicit TextPanel plugin spec",
                    "id": dashboard_id,
                    "patch": [
                        {
                            "op": "replace",
                            "path": "/spec/panels/explicit/spec/plugin/spec",
                            "value": {
                                "mode": "markdown",
                                "text": "# Patched runbook",
                                "presentation": {
                                    "textAlign": "center",
                                    "verticalAlign": "bottom",
                                    "background": "#102A33",
                                },
                                "headerOptions": {"hide": False},
                            },
                        }
                    ],
                },
            )
        )
        patched_data = _data(first_json(patched))
        patched_spec = _panel(patched_data, "explicit")["spec"]["plugin"]["spec"]
        assert patched_spec["text"] == "# Patched runbook"
        assert patched_spec["presentation"]["background"] == "#102A33"
        assert patched_spec["headerOptions"] == {"hide": False}
        assert patched_data["spec"]["layouts"] == dashboard["spec"]["layouts"]

        deleted_result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_delete_dashboard",
                {"searchContext": f"delete dashboard {dashboard_id}", "id": dashboard_id},
            )
        )
        assert "deleted" in text_blocks(deleted_result).lower()
        deleted = True
        gone = mcp_client.call_tool(
            "signoz_get_dashboard", {"searchContext": f"confirm dashboard {dashboard_id} is gone", "id": dashboard_id}
        )
        assert gone.get("isError", False), f"dashboard {dashboard_id} remained after delete"
    finally:
        if not deleted:
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_delete_dashboard",
                    {"searchContext": f"cleanup dashboard {dashboard_id}", "id": dashboard_id},
                )
            )
        gone = mcp_client.call_tool(
            "signoz_get_dashboard",
            {"searchContext": f"confirm dashboard {dashboard_id} is gone after cleanup", "id": dashboard_id},
        )
        assert gone.get("isError", False), f"dashboard {dashboard_id} remained after cleanup"


def test_system_dashboard_is_hidden_but_gettable_and_source_is_not_a_list_filter(signoz: SigNoz) -> None:
    """Read-only system-dashboard checks; platform rows are never mutated here."""
    list_response = signoz.api("GET", "/api/v2/dashboards", params={"limit": 200})
    assert list_response.status_code == 200, list_response.text[:500]
    list_payload = list_response.json()
    rows = list_payload.get("data", {}).get("dashboards", [])
    assert all(row.get("name") != "signoz---ai-o11y-overview" for row in rows)

    reserved = list_payload.get("data", {}).get("reservedKeywords", [])
    keyword_names = {_reserved_keyword_name(item) for item in reserved}
    assert "source" not in keyword_names

    system_response = signoz.api("GET", "/api/v2/dashboards/system/ai-o11y-overview")
    assert system_response.status_code == 200, system_response.text[:500]
    system_data = system_response.json().get("data", {})
    assert system_data.get("source") == "system"
    assert system_data.get("schemaVersion") == "v6"
    assert "id" not in system_data
