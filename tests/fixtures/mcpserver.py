import os
import socket
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path

import pytest
import requests

from fixtures.commander import Commander
from fixtures.logger import setup_logger
from fixtures.signoz import SigNoz

logger = setup_logger(__name__)

TESTS_DIR = Path(__file__).resolve().parent.parent
REPO_ROOT = TESTS_DIR.parent
BIN = TESTS_DIR / "tmp" / "bin" / "signoz-mcp-server"
SERVER_LOG = TESTS_DIR / "tmp" / "signoz-mcp-server.log"

READY_TIMEOUT = 60.0


@dataclass(frozen=True)
class MCPServer:
    base_url: str

    @property
    def mcp_url(self) -> str:
        return f"{self.base_url}/mcp"

    def __log__(self) -> str:
        return f"mcpserver(base_url={self.base_url})"


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _wait_ready(base_url: str, process: subprocess.Popen, timeout: float = READY_TIMEOUT) -> None:
    """Wait until /readyz returns 200 (it 503s while the docs index warms)."""
    deadline = time.time() + timeout
    last = None

    while time.time() < deadline:
        if process.poll() is not None:
            raise RuntimeError(
                f"MCP server exited with {process.returncode} before becoming ready; log:\n{_log_tail()}"
            )
        try:
            resp = requests.get(f"{base_url}/readyz", timeout=5)
            if resp.status_code == 200:
                logger.info("MCP server ready at %s", base_url)
                return
            last = (resp.status_code, resp.text[:200])
        except requests.RequestException as err:
            last = err
        time.sleep(1)

    raise TimeoutError(f"MCP server did not become ready within {timeout}s (last={last}); log:\n{_log_tail()}")


def _log_tail(lines: int = 120) -> str:
    if not SERVER_LOG.exists():
        return "<no server log>"
    return "".join(SERVER_LOG.read_text().splitlines(keepends=True)[-lines:])


@pytest.fixture(scope="session")
def mcp_server(request: pytest.FixtureRequest, signoz: SigNoz) -> MCPServer:
    """The MCP server binary, built from the working tree and pointed at SigNoz.

    Always built and started fresh per run (cheap); only the SigNoz stack is
    reused across --reuse runs.
    """
    go = Commander.from_path(request.config.getoption("--go-binary-path"), cwd=REPO_ROOT)

    BIN.parent.mkdir(parents=True, exist_ok=True)
    go.run("build", "-o", str(BIN), "./cmd/server", timeout=600)

    port = _free_port()
    base_url = f"http://127.0.0.1:{port}"
    env = os.environ | {
        "TRANSPORT_MODE": "http",
        "MCP_SERVER_HOST": "127.0.0.1",
        "MCP_SERVER_PORT": str(port),
        "SIGNOZ_URL": signoz.endpoint,
        "SIGNOZ_API_KEY": signoz.access_token,
        "LOG_LEVEL": "error",
        "ANALYTICS_ENABLED": "false",
        "OTEL_TRACES_EXPORTER": "none",
        "OTEL_METRICS_EXPORTER": "none",
    }

    SERVER_LOG.parent.mkdir(parents=True, exist_ok=True)
    with SERVER_LOG.open("w") as log_file:
        process = subprocess.Popen([str(BIN)], env=env, stdout=log_file, stderr=subprocess.STDOUT)

    def stop() -> None:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        logger.info("MCP server stopped; log at %s", SERVER_LOG)

    request.addfinalizer(stop)

    try:
        _wait_ready(base_url, process)
    except Exception:
        stop()
        raise

    return MCPServer(base_url=base_url)
