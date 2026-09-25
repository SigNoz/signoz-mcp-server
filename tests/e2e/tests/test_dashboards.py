"""Dashboard v6 panel lifecycles, panel query dry-runs, and released system-dashboard contracts."""

import time

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


def _area_panel(spec: dict) -> dict:
    return {
        "kind": "Panel",
        "spec": {
            "display": {"name": "Request Volume"},
            "links": [],
            "plugin": {"kind": "signoz/AreaChartPanel", "spec": spec},
            "queries": [
                {
                    "kind": "time_series",
                    "spec": {
                        "name": "A",
                        "plugin": {
                            "kind": "signoz/BuilderQuery",
                            "spec": {
                                "signal": "traces",
                                "name": "A",
                                "aggregations": [{"expression": "count()"}],
                                "groupBy": [
                                    {
                                        "name": "service.name",
                                        "fieldContext": "resource",
                                        "fieldDataType": "string",
                                        "signal": "traces",
                                    }
                                ],
                                "legend": "{{service.name}}",
                                "order": [{"key": {"name": "count()"}, "direction": "desc"}],
                                "limit": 100,
                            },
                        },
                    },
                }
            ],
        },
    }


def _area_dashboard(title: str, spec: dict) -> dict:
    return {
        "searchContext": f"create a dashboard named {title} with a stacked area chart",
        "schemaVersion": "v6",
        "tags": [],
        "spec": {
            "display": {"name": title},
            "variables": [],
            "panels": {"volume": _area_panel(spec)},
            "layouts": [
                {
                    "kind": "Grid",
                    "spec": {
                        "items": [
                            {"x": 0, "y": 0, "width": 12, "height": 6, "content": {"$ref": "#/spec/panels/volume"}}
                        ]
                    },
                }
            ],
        },
    }


def test_area_chart_panel_round_trips_stack_and_fill_and_rejects_fill_none(mcp_client: MCPClient, test_id: str) -> None:
    title = f"mcp-e2e-areachart-{test_id}"
    rejected = mcp_client.call_tool(
        "signoz_create_dashboard",
        _area_dashboard(f"{title}-invalid", {"chartAppearance": {"fillMode": "none"}}),
    )
    if not rejected.get("isError", False):
        leaked_id = _dashboard_id(first_json(rejected))
        mcp_client.call_tool(
            "signoz_delete_dashboard", {"searchContext": f"cleanup dashboard {leaked_id}", "id": leaked_id}
        )
        raise AssertionError("area chart accepted fillMode none, which only timeseries panels allow")

    created = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_create_dashboard",
            _area_dashboard(
                title,
                {
                    "visualization": {"stack": "normal"},
                    "chartAppearance": {"fillMode": "gradient", "fillOpacity": 0.4},
                    "legend": {"position": "bottom"},
                },
            ),
        )
    )
    dashboard_id = _dashboard_id(first_json(created))
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_dashboard", {"searchContext": f"get dashboard {dashboard_id}", "id": dashboard_id}
            )
        )
        plugin = _data(first_json(fetched))["spec"]["panels"]["volume"]["spec"]["plugin"]
        assert plugin["kind"] == "signoz/AreaChartPanel"
        assert plugin["spec"]["visualization"]["stack"] == "normal"
        assert plugin["spec"]["chartAppearance"]["fillMode"] == "gradient"
        assert plugin["spec"]["chartAppearance"]["fillOpacity"] == 0.4

        patched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_patch_dashboard",
                {
                    "searchContext": "switch the area chart to percent stacking",
                    "id": dashboard_id,
                    "patch": [
                        {
                            "op": "replace",
                            "path": "/spec/panels/volume/spec/plugin/spec/visualization/stack",
                            "value": "percent",
                        }
                    ],
                },
            )
        )
        patched_plugin = _data(first_json(patched))["spec"]["panels"]["volume"]["spec"]["plugin"]
        assert patched_plugin["spec"]["visualization"]["stack"] == "percent"
        assert patched_plugin["spec"]["chartAppearance"]["fillMode"] == "gradient"
    finally:
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


ENVELOPE_BY_PLUGIN = {
    "signoz/BuilderQuery": "builder_query",
    "signoz/Formula": "builder_formula",
    "signoz/TraceOperator": "builder_trace_operator",
    "signoz/PromQLQuery": "promql",
    "signoz/ClickHouseSQL": "clickhouse_sql",
}


def _execution_envelopes(plugin: dict) -> list[dict]:
    """Translate one saved panel query plugin as signoz://dashboard/widgets-instructions describes."""
    if plugin["kind"] == "signoz/CompositeQuery":
        return plugin["spec"]["queries"]
    return [{"type": ENVELOPE_BY_PLUGIN[plugin["kind"]], "spec": plugin["spec"]}]


def _count_query(name: str, disabled: bool = False, filter_expression: str = "") -> dict:
    spec = {
        "signal": "traces",
        "name": name,
        "aggregations": [{"expression": "count()"}],
        "order": [{"key": {"name": "count()"}, "direction": "desc"}],
        "limit": 10000 if disabled else 100,
        "disabled": disabled,
    }
    if filter_expression:
        spec["filter"] = {"expression": filter_expression}
    return spec


def test_saved_panel_queries_dry_run_unchanged_through_execute_builder_query(
    mcp_client: MCPClient, test_id: str
) -> None:
    """Saved Perses query specs, as GET returns them, execute unchanged inside their envelopes."""
    title = f"mcp-e2e-dryrun-{test_id}"
    direct = {"kind": "signoz/BuilderQuery", "spec": _count_query("A")}
    composite = {
        "kind": "signoz/CompositeQuery",
        "spec": {
            "queries": [
                {
                    "type": "builder_query",
                    "spec": _count_query("A", disabled=True, filter_expression="has_error = true"),
                },
                {"type": "builder_query", "spec": _count_query("B", disabled=True)},
                {
                    "type": "builder_formula",
                    "spec": {
                        "name": "F1",
                        "expression": "A * 100 / B",
                        "order": [{"key": {"name": "__result"}, "direction": "desc"}],
                        "limit": 100,
                    },
                },
            ]
        },
    }

    def panel(name: str, plugin: dict) -> dict:
        return {
            "kind": "Panel",
            "spec": {
                "display": {"name": name},
                "links": [],
                "plugin": {"kind": "signoz/NumberPanel", "spec": {}},
                "queries": [{"kind": "scalar", "spec": {"name": "A", "plugin": plugin}}],
            },
        }

    dashboard = {
        "searchContext": f"create a dashboard named {title} with direct and composite query panels",
        "schemaVersion": "v6",
        "tags": [],
        "spec": {
            "display": {"name": title},
            "variables": [],
            "panels": {
                "direct": panel("Requests", direct),
                "composite": panel("Error rate", composite),
                "promql": panel("PromQL", {"kind": "signoz/PromQLQuery", "spec": {"name": "A", "query": "vector(1)"}}),
                "clickhouse": panel(
                    "ClickHouse", {"kind": "signoz/ClickHouseSQL", "spec": {"name": "A", "query": "SELECT 1 AS value"}}
                ),
            },
            "layouts": [
                {
                    "kind": "Grid",
                    "spec": {
                        "items": [
                            {"x": 0, "y": 0, "width": 6, "height": 3, "content": {"$ref": "#/spec/panels/direct"}},
                            {"x": 6, "y": 0, "width": 6, "height": 3, "content": {"$ref": "#/spec/panels/composite"}},
                            {"x": 0, "y": 3, "width": 6, "height": 3, "content": {"$ref": "#/spec/panels/promql"}},
                            {"x": 6, "y": 3, "width": 6, "height": 3, "content": {"$ref": "#/spec/panels/clickhouse"}},
                        ]
                    },
                }
            ],
        },
    }
    created = assert_tool_ok(mcp_client.call_tool("signoz_create_dashboard", dashboard))
    dashboard_id = _dashboard_id(first_json(created))
    try:
        fetched = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_dashboard", {"searchContext": f"get dashboard {dashboard_id}", "id": dashboard_id}
            )
        )
        panels = _data(first_json(fetched))["spec"]["panels"]
        now = int(time.time() * 1000)
        for panel_id in ("direct", "composite", "promql", "clickhouse"):
            saved_query = panels[panel_id]["spec"]["queries"][0]
            query = {
                "schemaVersion": "v1",
                "start": now - 30 * 60 * 1000,
                "end": now,
                "requestType": saved_query["kind"],
                "compositeQuery": {"queries": _execution_envelopes(saved_query["spec"]["plugin"])},
            }
            assert_tool_ok(
                mcp_client.call_tool(
                    "signoz_execute_builder_query",
                    {"searchContext": f"dry-run the saved {panel_id} panel query", "query": query},
                )
            )
        unbounded = {key: value for key, value in query.items() if key not in ("start", "end")}
        missing_bounds = mcp_client.call_tool(
            "signoz_execute_builder_query", {"searchContext": "dry-run without bounds", "query": unbounded}
        )
        assert missing_bounds.get("isError", False), "a dry-run without start and end succeeded"
        assert "missing start or end timestamp" in text_blocks(missing_bounds)
    finally:
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
