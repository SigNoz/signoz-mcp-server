"""Premise tripwires for the MCP server's compensating saved-view guards.

The MCP server rejects some saved-view writes that SigNoz accepts, because the
accepted result is a view the Explorer cannot render (docs/testing.md, the
guards' own comments). Each test here pins that premise by driving SigNoz's
API directly, bypassing the MCP guard. The day one of these fails, SigNoz has
started enforcing the rule itself: delete the matching guard in
internal/handler/tools/views.go, its handler tests, and the tripwire, in one
change.
"""

from fixtures.logger import setup_logger
from fixtures.signoz import SigNoz

logger = setup_logger(__name__)


def _view_body(name: str, source: str, signal: str) -> dict:
    return {
        "name": name,
        "source": source,
        "schemaVersion": "v2",
        "spec": {
            "displayName": name,
            "panelType": "list",
            "requestType": "raw",
            "queries": [{"type": "builder_query", "spec": {"name": "A", "signal": signal, "limit": 10}}],
        },
    }


def _create_raw(signoz: SigNoz, body: dict) -> str:
    response = signoz.api("POST", "/api/v2/saved_views", json=body)
    assert response.status_code == 201, (
        f"premise broken: SigNoz now rejects this view ({response.status_code}: {response.text[:300]}). "
        "If it rejects for the rule under test, the matching MCP guard is now a duplicate: "
        "delete it, its tests, and this tripwire together."
    )
    view_id = response.json()["data"]["id"]
    assert view_id
    return view_id


def _delete_raw(signoz: SigNoz, view_id: str) -> None:
    deleted = signoz.api("DELETE", f"/api/v2/saved_views/{view_id}")
    assert deleted.status_code in (204, 404), f"cleanup failed for {view_id}: {deleted.status_code}"
    gone = signoz.api("GET", f"/api/v2/saved_views/{view_id}")
    assert gone.status_code == 404, f"view {view_id} still present after cleanup"


def test_signoz_accepts_signal_source_mismatch(signoz: SigNoz, test_id: str) -> None:
    """SigNoz saves a view whose builder query signal differs from its source.

    This is the premise behind the MCP-side signal/source guard: upstream
    accepts the write and the Explorer then cannot render the view.
    """
    view_id = _create_raw(signoz, _view_body(f"mcp-e2e-premise-mismatch-{test_id}", "logs", "traces"))
    try:
        fetched = signoz.api("GET", f"/api/v2/saved_views/{view_id}")
        assert fetched.status_code == 200
        data = fetched.json()["data"]
        assert data["source"] == "logs", f"stored source changed: {data['source']}"
        logger.info("premise holds: mismatched view %s saved by upstream", view_id)
    finally:
        _delete_raw(signoz, view_id)


def test_signoz_allows_source_change_on_update(signoz: SigNoz, test_id: str) -> None:
    """SigNoz overwrites a view's source on update with no lock.

    This is the premise behind the MCP-side source-change rejection: the update
    handler GETs the existing view and refuses a differing source, because
    upstream applies the overwrite blindly.
    """
    name = f"mcp-e2e-premise-sourcechange-{test_id}"
    view_id = _create_raw(signoz, _view_body(name, "logs", "logs"))
    try:
        update = {
            "source": "traces",
            "schemaVersion": "v2",
            "spec": {
                "displayName": name,
                "panelType": "list",
                "requestType": "raw",
                "queries": [{"type": "builder_query", "spec": {"name": "A", "signal": "traces", "limit": 10}}],
            },
        }
        updated = signoz.api("PUT", f"/api/v2/saved_views/{view_id}", json=update)
        assert updated.status_code == 204, (
            f"premise broken: SigNoz now rejects the source change ({updated.status_code}: "
            f"{updated.text[:300]}). Delete the MCP source-lock guard, its tests, and this tripwire together."
        )
        fetched = signoz.api("GET", f"/api/v2/saved_views/{view_id}")
        assert fetched.status_code == 200
        assert fetched.json()["data"]["source"] == "traces", "upstream did not apply the source change"
        logger.info("premise holds: upstream overwrote source on view %s", view_id)
    finally:
        _delete_raw(signoz, view_id)
