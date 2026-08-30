"""Org overview — port of e2e_org_overview_test.go.

The Go test replayed one captured upstream response through a mock for an exact
conservation check. Through the transport we compare the tool's output against
a direct GET /api/v1/stats: the key sets must match exactly, and values must
match too — except counters/timestamps that can advance between the two live
calls (seeded telemetry keeps arriving), which must never go backwards.
"""

import json

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block
from fixtures.signoz import SigNoz

# Fields that legitimately advance between the upstream fetch and the tool call.
VOLATILE_PREFIXES = ("telemetry.",)
VOLATILE_KEYS = {
    "alert.firing.count",
    "alert.last_fired.time",
    "alert.last_fired.time_unix",
    "auth_token.last_observed_at.max.time",
    "auth_token.last_observed_at.max.time_unix",
}


def _volatile(key: str) -> bool:
    return key.startswith(VOLATILE_PREFIXES) or key in VOLATILE_KEYS


def test_org_overview(mcp_client: MCPClient, signoz: SigNoz) -> None:
    upstream_resp = signoz.api("GET", "/api/v1/stats")
    assert upstream_resp.status_code == 200, f"/api/v1/stats: {upstream_resp.status_code} {upstream_resp.text[:200]}"
    upstream = upstream_resp.json()
    assert upstream.get("status") == "success" and isinstance(upstream.get("data"), dict)
    source = upstream["data"]

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_get_org_overview", {"searchContext": "Give me a complete SigNoz deployment posture overview"}
        )
    )

    # structuredContent round-trips the exact-number text result.
    structured = result.get("structuredContent")
    assert structured is not None, "overview must return structuredContent"
    output = json.loads(first_text_block(result))
    assert output == structured, "structuredContent differs from the text result"

    data = output["data"]
    source_stats = data["sourceStats"]

    # Conservation: every upstream field is preserved in sourceStats.
    assert set(source_stats) == set(source), (
        f"sourceStats key drift: only-upstream={sorted(set(source) - set(source_stats))}, "
        f"only-tool={sorted(set(source_stats) - set(source))}"
    )
    for key, want in source.items():
        actual = source_stats[key]
        if _volatile(key):
            if isinstance(want, (int, float)) and isinstance(actual, (int, float)):
                assert actual >= want, f"volatile counter {key} went backwards: {actual} < {want}"
            continue
        assert actual == want, f"sourceStats changed the value of {key}: {actual!r} != {want!r}"

    # Metadata reconciliation.
    metadata = data["metadata"]
    assert metadata["reportedStatCount"] == len(source_stats)
    assert metadata["projectedStatCount"] + metadata["unprojectedStatCount"] == metadata["reportedStatCount"]
    assert metadata["projectionPartial"] == (len(metadata.get("incompleteGroups") or []) > 0)
    for group in metadata.get("incompleteGroups") or []:
        assert group.get("group") and group.get("fields") and group.get("reason") and group.get("nextAction"), (
            f"incomplete group lacks recovery metadata: {group}"
        )

    # Typed projections, stable on this deployment: one root user, sqlite store.
    assert data["users"]["count"] >= 1
    assert isinstance(data["signals"]["logs"]["available"], bool)
    assert data["configuration"]["sqlStoreProvider"] == "sqlite"
