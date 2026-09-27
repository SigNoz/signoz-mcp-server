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
# Foundry's container name for the ClickHouse schema migrator job.
MIGRATOR = "signoz-telemetrystore-migrator"


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


def _wait_for_migrations(timeout: float = 600.0) -> None:
    """Wait until the telemetry-store migrator exits cleanly.

    SigNoz answers on 8080 before the ClickHouse schema exists; queries in that
    window fail with "Unknown table" (e.g. signoz_metadata.distributed_column_evolution_metadata).
    """
    deadline = time.time() + timeout
    state = None

    while time.time() < deadline:
        result = subprocess.run(
            ["docker", "inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", MIGRATOR],
            capture_output=True,
            text=True,
            check=False,
        )
        state = result.stdout.strip() or result.stderr.strip()
        if state == "exited 0":
            return
        if state.startswith("exited "):
            raise RuntimeError(f"{MIGRATOR} failed (state={state}); check `docker logs {MIGRATOR}`")

        time.sleep(3)

    raise TimeoutError(f"{MIGRATOR} did not finish within {timeout}s (last state={state})")


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
    _wait_for_migrations()
    return ENDPOINT


def teardown() -> None:
    """`docker compose down` the cast environment and remove pours/."""
    compose = compose_file()
    if compose is None:
        return

    subprocess.run(["docker", "compose", "-f", str(compose), "down", "-v"], check=False)
    shutil.rmtree(POURS, ignore_errors=True)
