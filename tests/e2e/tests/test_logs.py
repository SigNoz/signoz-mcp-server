from fixtures.logger import setup_logger
from fixtures.mcpclient import MCPClient, assert_tool_ok, first_json, text_blocks
from fixtures.telemetry import seed_logs, wait_for

logger = setup_logger(__name__)


def test_seeded_logs_are_searchable(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    """Logs seeded over OTLP become visible through signoz_search_logs."""
    service = f"mcp-e2e-{test_id}"
    marker = f"mcp-e2e-marker-{test_id}"
    seed_logs(service, marker, count=3)

    def search() -> bool:
        result = mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": f"find the e2e marker log line {marker}",
                "searchText": marker,
                "timeRange": "1h",
                "limit": "10",
            },
        )
        if result.get("isError", False):
            logger.info("signoz_search_logs errored, retrying: %s", text_blocks(result)[:200])
            return False
        return marker in text_blocks(result)

    wait_for(search, f"seeded log marker {marker} visible via signoz_search_logs")

    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_search_logs",
            {
                "searchContext": f"find logs from service {service}",
                "service": service,
                "timeRange": "1h",
                "limit": "10",
            },
        )
    )
    assert marker in text_blocks(result), f"service-filtered search missed the marker: {first_json(result)}"
