"""Timestamp magnitude auto-detect against the services API — port of Family E K1.

SigNoz's services endpoints take nanoseconds, so a millisecond window only
finds seeded data if the server converts it. Argument parsing that never
reaches SigNoz is pinned in the Go handler tests.
"""

import time

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_text_block
from fixtures.telemetry import seed_traces, wait_for


def test_services_ns_and_ms_windows(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """The seeded service and its operation come back for ns AND ms windows (K1)."""
    service = f"mcp-e2e-{test_id}"
    seed_traces(service, count=2)

    def visible() -> bool:
        result = mcp_client.call_tool(
            "signoz_list_services", {"searchContext": "services", "timeRange": "1h", "limit": "1000"}
        )
        return not result.get("isError", False) and service in first_text_block(result)

    wait_for(visible, f"service {service} visible")

    end_ms = int(time.time() * 1000)
    start_ms = end_ms - 6 * 3_600_000
    windows = {
        "ns": {"start": str(start_ms * 1_000_000), "end": str(end_ms * 1_000_000)},
        "ms": {"start": str(start_ms), "end": str(end_ms)},
    }
    for unit, window in windows.items():
        services = assert_tool_ok(
            mcp_client.call_tool("signoz_list_services", {"searchContext": "services", "limit": "1000", **window})
        )
        assert service in first_text_block(services), f"{unit} window missed the seeded service {service}"
        operations = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_service_top_operations",
                {"searchContext": f"top operations for {service}", "service": service, **window},
            )
        )
        assert "e2e-operation" in first_text_block(operations), f"{unit} window missed the seeded operation"
