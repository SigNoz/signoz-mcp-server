import time
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


def _wait_ready(base_url: str, container, timeout: float = READY_TIMEOUT) -> None:
    """Wait until /readyz returns 200 (it 503s while the docs index warms)."""
    deadline = time.time() + timeout
    last = None

    while time.time() < deadline:
        container.reload()
        if container.status != "running":
            raise RuntimeError(
                f"MCP server container is {container.status} before becoming ready; logs:\n{_container_logs(container)}"
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

    raise TimeoutError(
        f"MCP server did not become ready within {timeout}s (last={last}); logs:\n{_container_logs(container)}"
    )


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
    docker_cli = Commander.from_path("docker", cwd=REPO_ROOT)
    docker_cli.run("build", "--file", "Dockerfile.e2e", "--tag", IMAGE, ".", timeout=900)

    client = docker.from_env()
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

    try:
        binding = client.api.port(container.id, CONTAINER_PORT)
        host_port = int(binding[0]["HostPort"])
        base_url = f"http://127.0.0.1:{host_port}"
        _wait_ready(base_url, container)
    except Exception:
        stop()
        raise

    return MCPServer(base_url=base_url)
