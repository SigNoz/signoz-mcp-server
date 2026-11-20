"""SigNoz audit events through the log tools with source="audit"."""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, first_text_block, query_range_rows
from fixtures.telemetry import AuditEvent, seed_audit_events, wait_for


def _seed(test_id: str) -> tuple[str, str, str, str]:
    """Alice creates and deletes a dashboard; Bob fails to delete an alert rule."""
    alice, bob = f"alice-{test_id}@e2e.test", f"bob-{test_id}@e2e.test"
    dashboard, rule = f"dash-{test_id}", f"rule-{test_id}"

    def who(email: str, verb: str, outcome: str = "success") -> dict[str, str]:
        return {
            "signoz.principal.email": email,
            "signoz.principal.type": "user",
            "signoz.audit.verb": verb,
            "signoz.audit.outcome": outcome,
        }

    seed_audit_events(
        [
            AuditEvent(
                body=f"{alice} created dashboard ({dashboard})",
                event_name="signoz.audit.create",
                attributes=who(alice, "create"),
                resources={"signoz.resource.kind": "dashboard", "signoz.resource.id": dashboard},
            ),
            AuditEvent(
                body=f"{alice} deleted dashboard ({dashboard})",
                event_name="signoz.audit.delete",
                attributes=who(alice, "delete"),
                resources={"signoz.resource.kind": "dashboard", "signoz.resource.id": dashboard},
            ),
            AuditEvent(
                body=f"{bob} failed to delete rule ({rule}): forbidden",
                event_name="signoz.audit.delete",
                attributes={**who(bob, "delete", "failure"), "exception.type": "forbidden"},
                resources={"signoz.resource.kind": "rule", "signoz.resource.id": rule},
                severity="ERROR",
            ),
        ]
    )
    return alice, bob, dashboard, rule


def _search(mcp_client: MCPClient, **arguments: object) -> dict:
    return mcp_client.call_tool(
        "signoz_search_logs",
        {"searchContext": "who changed what in SigNoz", "timeRange": "1h", "limit": "50", **arguments},
    )


def _rows(result: dict) -> list[dict]:
    return query_range_rows(first_text_block(assert_tool_ok(result)))


def test_search_logs_source_audit_reads_audit_events(mcp_client: MCPClient, test_id: str) -> None:
    """source="audit" reads audit rows in their own shape; without it the same filter misses them."""
    alice, bob, dashboard, rule = _seed(test_id)
    by_alice = f"signoz.principal.email = '{alice}'"

    def alice_events() -> list[dict] | None:
        rows = _rows(_search(mcp_client, source="audit", filter=by_alice))
        return rows if len(rows) == 2 else None

    rows = wait_for(alice_events, f"two audit events by {alice}")
    assert {row["body"]["message"] for row in rows} == {
        f"{alice} created dashboard ({dashboard})",
        f"{alice} deleted dashboard ({dashboard})",
    }
    assert {row["attributes"]["signoz.audit.verb"] for row in rows} == {"create", "delete"}
    assert {row["resource"]["attributes"]["signoz.resource.id"] for row in rows} == {dashboard}

    assert _rows(_search(mcp_client, filter=by_alice)) == [], "audit events must not appear in application logs"

    by_resource = _rows(
        _search(mcp_client, source="audit", filter=f"signoz.resource.kind = 'rule' AND signoz.resource.id = '{rule}'")
    )
    assert [row["attributes"]["signoz.principal.email"] for row in by_resource] == [bob]

    failures = _rows(_search(mcp_client, source="audit", severity="ERROR", searchText=bob))
    assert [row["attributes"]["exception.type"] for row in failures] == ["forbidden"]


def test_aggregate_logs_source_audit_counts_audit_events(mcp_client: MCPClient, test_id: str) -> None:
    """source="audit" aggregates audit events, grouped by an audit attribute."""
    alice, bob, _, _ = _seed(test_id)

    def counts() -> dict[str, float] | None:
        result = mcp_client.call_tool(
            "signoz_aggregate_logs",
            {
                "searchContext": "changes per verb in SigNoz",
                "source": "audit",
                "aggregations": "count()",
                "groupBy": "signoz.audit.verb",
                "filter": f"signoz.principal.email IN ('{alice}', '{bob}')",
                "timeRange": "1h",
            },
        )
        if result.get("isError", False):
            return None
        rows = [row for r in first_block_json(result)["data"]["data"]["results"] for row in r.get("data") or []]
        found = {row[0]: row[-1] for row in rows}
        return found if sum(found.values()) == 3 else None

    assert wait_for(counts, "three audit events counted by verb") == {"create": 1, "delete": 2}
