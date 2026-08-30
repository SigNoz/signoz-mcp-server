import shutil
import subprocess
import time
from pathlib import Path

import pytest
import requests

from fixtures.logger import setup_logger

logger = setup_logger(__name__)

ENDPOINT = "http://localhost:8080"

TESTS_DIR = Path(__file__).resolve().parent.parent
CASTING = TESTS_DIR / "casting.yaml"
POURS = TESTS_DIR / "pours"


def _wait_for_port(endpoint: str, timeout: float = 240.0) -> None:
    """Wait until the SigNoz HTTP port answers at all (any status)."""
    deadline = time.time() + timeout
    last = None

    while time.time() < deadline:
        try:
            requests.get(endpoint, timeout=5)
            return
        except requests.RequestException as err:
            last = err

        time.sleep(3)

    raise TimeoutError(f"{endpoint} did not respond within {timeout}s (last={last})")


def compose_file() -> Path | None:
    candidates = sorted(POURS.rglob("compose.yaml"))
    return candidates[0] if candidates else None


def cast(foundryctl: str) -> str:
    """Bring up SigNoz with foundry and return its endpoint."""
    if shutil.which(foundryctl) is None:
        raise pytest.UsageError(f"{foundryctl} not on PATH; pass --foundry-binary-path")

    logger.info("casting SigNoz with %s (%s)", foundryctl, CASTING)
    subprocess.run(
        [foundryctl, "cast", "--no-ledger", "-f", str(CASTING), "-p", str(POURS)],
        cwd=TESTS_DIR,
        check=True,
    )

    _wait_for_port(ENDPOINT)
    return ENDPOINT


def teardown() -> None:
    """`docker compose down` the cast environment and remove pours/."""
    compose = compose_file()
    if compose is None:
        return

    subprocess.run(["docker", "compose", "-f", str(compose), "down", "-v"], check=False)
    shutil.rmtree(POURS, ignore_errors=True)
