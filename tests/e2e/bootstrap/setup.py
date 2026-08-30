from http import HTTPStatus

import requests

from fixtures.logger import setup_logger
from fixtures.mcpclient import MCPClient
from fixtures.mcpserver import MCPServer
from fixtures.signoz import SigNoz

logger = setup_logger(__name__)


def test_setup(signoz: SigNoz, mcp_server: MCPServer) -> None:
    """Create (or reuse) the environment and confirm both ends are reachable."""
    version = requests.get(f"{signoz.endpoint}/api/v1/version", timeout=5)
    logger.info("signoz version response: %s", version.status_code)
    assert version.status_code == HTTPStatus.OK

    readyz = requests.get(f"{mcp_server.base_url}/readyz", timeout=5)
    logger.info("mcp server readyz response: %s", readyz.status_code)
    assert readyz.status_code == HTTPStatus.OK

    result = MCPClient(mcp_url=mcp_server.mcp_url).initialize()
    assert result["serverInfo"]["name"] == "SigNozMCP"
    assert result["protocolVersion"] == "2025-11-25"
    logger.info("mcp initialize handshake succeeded against %s", mcp_server.mcp_url)


def test_teardown(signoz: SigNoz) -> None:
    """Tear down the cached SigNoz environment (use with --teardown)."""
