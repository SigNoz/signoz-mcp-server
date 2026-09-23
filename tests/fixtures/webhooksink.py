import time
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

import docker
import pytest
import requests
from docker.errors import NotFound

from fixtures.commander import Commander
from fixtures.logger import setup_logger
from fixtures.signoz import SigNoz

logger = setup_logger(__name__)

SINK_DIR = Path(__file__).resolve().parent.parent / "e2e" / "webhook_sink"
IMAGE = "signoz-mcp-webhook-sink:e2e"
CONTAINER_PORT = 8080
READY_TIMEOUT = 30.0


@dataclass(frozen=True)
class WebhookSink:
    url: str
    control_url: str

    def captured_requests(self) -> list[dict]:
        response = requests.get(f"{self.control_url}/requests", timeout=5)
        response.raise_for_status()
        payload = response.json()
        assert isinstance(payload, list), f"webhook sink returned a non-list capture payload: {payload!r}"
        return payload


def _signoz_network(client) -> str:
    containers = client.containers.list(filters={"label": "com.docker.compose.service=signoz-signoz-0"})
    if len(containers) != 1:
        names = [container.name for container in containers]
        raise RuntimeError(f"expected one running foundry SigNoz container, found {names!r}")

    container = containers[0]
    container.reload()
    networks = list(container.attrs.get("NetworkSettings", {}).get("Networks", {}))
    if len(networks) != 1:
        raise RuntimeError(f"expected foundry SigNoz container on one network, found {networks!r}")
    return networks[0]


def _wait_ready(control_url: str, container) -> None:
    deadline = time.time() + READY_TIMEOUT
    last = None
    while time.time() < deadline:
        container.reload()
        if container.status != "running":
            raise RuntimeError(f"webhook sink container stopped before becoming ready: {container.status}")
        try:
            response = requests.get(f"{control_url}/readyz", timeout=2)
            if response.status_code == 204:
                return
            last = response.status_code
        except requests.RequestException as err:
            last = err
        time.sleep(0.2)
    raise TimeoutError(f"webhook sink did not become ready within {READY_TIMEOUT}s (last={last})")


@pytest.fixture(scope="session")
def webhook_sink_image() -> str:
    docker_cli = Commander.from_path("docker", cwd=SINK_DIR)
    docker_cli.run("build", "--tag", IMAGE, ".", timeout=300)
    return IMAGE


@pytest.fixture
def local_webhook_sink(signoz: SigNoz, webhook_sink_image: str) -> Iterator[WebhookSink]:
    """Run a capture sink beside SigNoz on its foundry-generated Docker network."""
    del signoz  # Ensure the foundry stack is running before its network is inspected.
    client = docker.from_env()
    network = _signoz_network(client)
    name = f"signoz-mcp-webhook-sink-{uuid4().hex[:12]}"
    container = client.containers.run(
        webhook_sink_image,
        detach=True,
        name=name,
        network=network,
        ports={f"{CONTAINER_PORT}/tcp": ("127.0.0.1", None)},
    )

    def remove() -> None:
        try:
            container.remove(force=True)
        except NotFound:
            pass
        try:
            client.containers.get(container.id)
        except NotFound:
            logger.info("local webhook sink container removed and confirmed absent")
            return
        raise AssertionError(f"local webhook sink container {container.id[:12]} remained after force removal")

    try:
        binding = client.api.port(container.id, CONTAINER_PORT)
        host_port = int(binding[0]["HostPort"])
        control_url = f"http://127.0.0.1:{host_port}"
        _wait_ready(control_url, container)
        yield WebhookSink(url=f"http://{name}:{CONTAINER_PORT}/e2e", control_url=control_url)
    finally:
        remove()
