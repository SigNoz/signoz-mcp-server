"""Executable ledger of upstream validation gaps (SigNoz/nerve-pod#401).

Each test asserts a behavior SigNoz currently permits that it arguably should
reject, driving the SigNoz API directly. The MCP server does not compensate for
any of these (docs/testing.md: validation is SigNoz's alone), so this file is
the only thing keeping the gap list honest: when a SigNoz release fixes a gap,
the version bump makes its test fail, and the failure message says what to
retire. A gap test failing is good news; follow its instructions instead of
"fixing" the test.

Rules payloads here use notificationSettings.usePolicy=true so no notification
channel needs to exist, and disabled=true so no evaluation task is built.
"""

from fixtures.logger import setup_logger
from fixtures.signoz import SigNoz

logger = setup_logger(__name__)


def _minimal_rule(name: str) -> dict:
    return {
        "alert": name,
        "alertType": "METRIC_BASED_ALERT",
        "ruleType": "threshold_rule",
        "version": "v5",
        "schemaVersion": "v2alpha1",
        "disabled": True,
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
                                {
                                    "metricName": "system.cpu.time",
                                    "timeAggregation": "avg",
                                    "spaceAggregation": "max",
                                }
                            ],
                        },
                    }
                ],
            },
            "selectedQueryName": "A",
            "thresholds": {
                "kind": "basic",
                "spec": [{"name": "critical", "op": "above", "matchType": "all_the_times", "target": 0.8}],
            },
        },
        "evaluation": {"kind": "rolling", "spec": {"evalWindow": "15m", "frequency": "1m"}},
        "notificationSettings": {"usePolicy": True},
    }


def _create_rule(signoz: SigNoz, body: dict, gap: str) -> str:
    response = signoz.api("POST", "/api/v2/rules", json=body)
    assert response.status_code == 201, (
        f"gap closed? SigNoz rejected this rule ({response.status_code}: {response.text[:300]}). "
        f"If it rejects for the behavior under test, {gap}"
    )
    rule_id = response.json()["data"]["id"]
    assert rule_id
    return rule_id


def _delete_rule(signoz: SigNoz, rule_id: str) -> None:
    deleted = signoz.api("DELETE", f"/api/v2/rules/{rule_id}")
    assert deleted.status_code in (204, 404), f"cleanup failed for rule {rule_id}: {deleted.status_code}"
    gone = signoz.api("GET", f"/api/v2/rules/{rule_id}")
    assert gone.status_code == 404, f"rule {rule_id} still present after cleanup"


def test_rule_unknown_top_level_key_accepted(signoz: SigNoz, test_id: str) -> None:
    """#401 item 1: unknown top-level keys are silently dropped, not rejected."""
    body = _minimal_rule(f"mcp-e2e-gap-unknown-key-{test_id}")
    body["descriptoin"] = "a typo an agent would make"
    rule_id = _create_rule(
        signoz,
        body,
        "close #401 item 1 and delete this test; misspelled keys now fail loudly.",
    )
    _delete_rule(signoz, rule_id)


def test_rule_missing_alert_type_accepted(signoz: SigNoz, test_id: str) -> None:
    """#401 item 2: alertType is optional in practice though the spec marks it required."""
    body = _minimal_rule(f"mcp-e2e-gap-no-alerttype-{test_id}")
    del body["alertType"]
    rule_id = _create_rule(
        signoz,
        body,
        "close #401 item 2 and delete this test; the spec and server now agree.",
    )
    _delete_rule(signoz, rule_id)


def test_rule_v2_preferred_channels_ignored(signoz: SigNoz, test_id: str) -> None:
    """#401 item 4: v2alpha1 ignores top-level preferredChannels entirely.

    The channel name here does not exist; acceptance proves the field is
    neither existence-checked nor routed on v2alpha1 rules.
    """
    body = _minimal_rule(f"mcp-e2e-gap-preferred-{test_id}")
    body["preferredChannels"] = [f"no-such-channel-{test_id}"]
    rule_id = _create_rule(
        signoz,
        body,
        "close #401 item 4, delete this test, and update the signoz_create_alert / "
        "signoz_update_alert descriptions and signoz://alert/instructions, which say "
        "SigNoz ignores v2 preferredChannels.",
    )
    _delete_rule(signoz, rule_id)


def test_rule_malformed_json_returns_500(signoz: SigNoz) -> None:
    """#401 item 8: a malformed rule body returns 500, not 400."""
    # The rules handler reads the raw body with no binding layer, so the
    # content type does not matter here.
    response = signoz.api("POST", "/api/v2/rules", data="{")
    assert response.status_code == 500, (
        f"gap closed? malformed JSON now returns {response.status_code}. If it is 400, "
        "close #401 item 8 and delete this test; the MCP error mapping already handles 400."
    )


def test_rule_duplicate_names_accepted(signoz: SigNoz, test_id: str) -> None:
    """#401 item 11: two rules may share the same alert name."""
    name = f"mcp-e2e-gap-dup-{test_id}"
    first = _create_rule(signoz, _minimal_rule(name), "close #401 item 11 and delete this test.")
    second = ""
    try:
        second = _create_rule(
            signoz,
            _minimal_rule(name),
            "close #401 item 11 and delete this test; duplicate alert names are now rejected.",
        )
    finally:
        if second:
            _delete_rule(signoz, second)
        _delete_rule(signoz, first)


def test_dashboard_duplicate_names_accepted(signoz: SigNoz, test_id: str) -> None:
    """#401 item 13: two dashboards may share the same name (uniqueness is a TODO upstream)."""

    def dashboard() -> dict:
        return {
            "name": f"mcp-e2e-gap-dash-{test_id}",
            "schemaVersion": "v6",
            "tags": [],
            "spec": {
                "display": {"name": f"gap ledger {test_id}"},
                "variables": [],
                "panels": {},
                "layouts": [],
            },
        }

    def create() -> str:
        response = signoz.api("POST", "/api/v2/dashboards", json=dashboard())
        assert response.status_code == 201, (
            f"gap closed? SigNoz rejected the dashboard ({response.status_code}: {response.text[:300]}). "
            "If the second create now returns 409, close #401 item 13 and delete this test."
        )
        return response.json()["data"]["id"]

    first = create()
    second = ""
    try:
        second = create()
    finally:
        for dash_id in (dash for dash in (second, first) if dash):
            deleted = signoz.api("DELETE", f"/api/v2/dashboards/{dash_id}")
            assert deleted.status_code in (204, 404), f"cleanup failed for dashboard {dash_id}"
            gone = signoz.api("GET", f"/api/v2/dashboards/{dash_id}")
            assert gone.status_code == 404, f"dashboard {dash_id} still present after cleanup"
