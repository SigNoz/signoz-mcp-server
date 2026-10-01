import os
import socket
import subprocess
import time
from collections.abc import Callable, Iterator
from dataclasses import dataclass
from pathlib import Path

import docker
import pytest
import requests
from docker.errors import APIError, NotFound

from fixtures.commander import Commander
from fixtures.logger import setup_logger
from fixtures.signoz import SigNoz

logger = setup_logger(__name__)

REPO_ROOT = Path(__file__).resolve().parent.parent.parent

IMAGE = "signoz-mcp-server:e2e"
CONTAINER_PORT = 8000
READY_TIMEOUT = 60.0


@dataclass(frozen=True)
class MCPServer:
    base_url: str
    backend_url: str = ""
    web_url: str = ""

    @property
    def mcp_url(self) -> str:
        return f"{self.base_url}/mcp"

    def __log__(self) -> str:
        return f"mcpserver(base_url={self.base_url})"


def _container_logs(container, lines: int = 120) -> str:
    try:
        return container.logs(tail=lines).decode(errors="replace")
    except APIError as err:
        return f"<could not read container logs: {err}>"


def _wait_ready(
    base_url: str,
    is_running: Callable[[], bool],
    diagnostics: Callable[[], str],
    timeout: float = READY_TIMEOUT,
) -> None:
    """Wait until /readyz returns 200 (it 503s while the docs index warms)."""
    deadline = time.time() + timeout
    last = None

    while time.time() < deadline:
        if not is_running():
            raise RuntimeError(f"MCP server stopped before becoming ready: {diagnostics()}")
        try:
            resp = requests.get(f"{base_url}/readyz", timeout=5)
            if resp.status_code == 200:
                logger.info("MCP server ready at %s", base_url)
                return
            last = (resp.status_code, resp.text[:200])
        except requests.RequestException as err:
            last = err
        time.sleep(1)

    raise TimeoutError(f"MCP server did not become ready within {timeout}s (last={last}); {diagnostics()}")


@pytest.fixture(scope="session")
def mcp_server(request: pytest.FixtureRequest, signoz: SigNoz) -> MCPServer:
    """The MCP server image, built from the working tree and run as a container.

    The image is rebuilt per run (cheap via BuildKit cache mounts); only the
    SigNoz stack is reused across --reuse runs. The container reaches SigNoz
    through the host gateway, and its MCP port is published to a
    docker-assigned free host port.
    """
    # Build via the docker CLI, like the signoz repo tests: plain build with
    # the repo root as context. Dockerfile.e2e deliberately uses no
    # BuildKit-only features so both builders work everywhere.
    client = docker.from_env()
    if os.environ.get("E2E_SKIP_IMAGE_BUILD") == "1":
        try:
            client.images.get(IMAGE)
        except NotFound as err:
            raise pytest.UsageError(f"E2E_SKIP_IMAGE_BUILD=1 requires an existing {IMAGE} image") from err
        logger.info("using prebuilt image %s", IMAGE)
    else:
        docker_cli = Commander.from_path("docker", cwd=REPO_ROOT)
        docker_cli.run("build", "--file", "Dockerfile.e2e", "--tag", IMAGE, ".", timeout=900)

    container = client.containers.run(
        IMAGE,
        detach=True,
        environment={
            "TRANSPORT_MODE": "http",
            # The server must bind all interfaces for the published port to
            # be reachable through the docker proxy.
            "MCP_SERVER_HOST": "0.0.0.0",
            "MCP_SERVER_PORT": str(CONTAINER_PORT),
            # The cast SigNoz publishes 8080 on the host; containers reach the
            # host through the gateway alias.
            "SIGNOZ_URL": signoz.endpoint.replace("localhost", "host.docker.internal").replace(
                "127.0.0.1", "host.docker.internal"
            ),
            "SIGNOZ_API_KEY": signoz.access_token,
            "LOG_LEVEL": "error",
            "ANALYTICS_ENABLED": "false",
            "OTEL_TRACES_EXPORTER": "none",
            "OTEL_METRICS_EXPORTER": "none",
        },
        # An empty HostPort makes the docker daemon assign a free host port;
        # read it back with client.api.port below (docker-py's free-port
        # mechanism, the same one testcontainers' get_exposed_port wraps).
        ports={f"{CONTAINER_PORT}/tcp": ("127.0.0.1", None)},
        extra_hosts={"host.docker.internal": "host-gateway"},
    )

    def stop() -> None:
        try:
            container.stop(timeout=10)
        except (APIError, NotFound):
            pass
        try:
            container.remove(force=True)
        except (APIError, NotFound):
            pass
        logger.info("MCP server container stopped")

    request.addfinalizer(stop)

    def is_running() -> bool:
        container.reload()
        return container.status == "running"

    try:
        binding = client.api.port(container.id, CONTAINER_PORT)
        host_port = int(binding[0]["HostPort"])
        base_url = f"http://127.0.0.1:{host_port}"
        _wait_ready(base_url, is_running, lambda: _container_logs(container))
    except Exception:
        stop()
        raise

    return MCPServer(base_url=base_url)


@pytest.fixture(scope="session")
def mcp_server_binary(tmp_path_factory: pytest.TempPathFactory) -> Path:
    binary = tmp_path_factory.mktemp("mcp-server") / "signoz-mcp-server"
    Commander.from_path("go", cwd=REPO_ROOT).run("build", "-o", str(binary), "./cmd/server/", timeout=900)
    return binary


@pytest.fixture
def mcp_server_with_web_url(
    request: pytest.FixtureRequest, mcp_server_binary: Path, signoz: SigNoz
) -> Iterator[MCPServer]:
    """Run natively so literal localhost reaches the same cast SigNoz on Linux and macOS."""
    __tracebackhide__ = True
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]

    backend_url = signoz.endpoint.replace("127.0.0.1", "localhost")
    web_url = request.param
    base_url = f"http://127.0.0.1:{port}"
    process = subprocess.Popen(
        [str(mcp_server_binary)],
        cwd=REPO_ROOT,
        env=os.environ
        | {
            "TRANSPORT_MODE": "http",
            "MCP_SERVER_HOST": "127.0.0.1",
            "MCP_SERVER_PORT": str(port),
            "SIGNOZ_URL": backend_url,
            "SIGNOZ_WEB_URL": web_url,
            "SIGNOZ_API_KEY": signoz.access_token,
            "SIGNOZ_CUSTOM_HEADERS": "",
            "SIGNOZ_INSTANCE_URL_ALLOWLIST": "",
            "OAUTH_ENABLED": "false",
            "LOG_LEVEL": "error",
            "ANALYTICS_ENABLED": "false",
            "OTEL_TRACES_EXPORTER": "none",
            "OTEL_METRICS_EXPORTER": "none",
        },
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
    )
    try:
        _wait_ready(base_url, lambda: process.poll() is None, lambda: f"exit code: {process.poll()}")
        yield MCPServer(base_url=base_url, backend_url=backend_url, web_url=web_url)
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)
        logger.info("local MCP server process stopped and reaped")
