import time
from typing import Any

import pytest
import requests

from fixtures.logger import setup_logger

logger = setup_logger(__name__)

# OTLP/HTTP endpoint of the cast SigNoz's OpenTelemetry collector. The foundry
# compose flavor publishes it on the host like the standard SigNoz compose
# deployment.
OTLP_ENDPOINT = "http://localhost:4318"

POLL_TIMEOUT = 180.0
POLL_INTERVAL = 3.0


def seed_logs(service_name: str, body: str, *, severity: str = "INFO", count: int = 1) -> None:
    """Push log records carrying `body` to the cast SigNoz via OTLP/HTTP."""
    now = time.time_ns()
    records = [
        {
            "timeUnixNano": str(now - i * 1_000_000),
            "severityText": severity,
            "body": {"stringValue": body},
            "attributes": [{"key": "e2e.marker", "value": {"stringValue": body}}],
        }
        for i in range(count)
    ]
    payload = {
        "resourceLogs": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": service_name}},
                        {"key": "telemetry.sdk.language", "value": {"stringValue": "e2e"}},
                    ]
                },
                "scopeLogs": [{"scope": {"name": "signoz-mcp-e2e"}, "logRecords": records}],
            }
        ]
    }
    resp = requests.post(f"{OTLP_ENDPOINT}/v1/logs", json=payload, timeout=30)
    resp.raise_for_status()
    logger.info("seeded %d log record(s) for service %s", count, service_name)


def wait_for(check, description: str, timeout: float = POLL_TIMEOUT, interval: float = POLL_INTERVAL) -> Any:
    """Poll `check` until it returns a truthy value; fail with the last value."""
    deadline = time.monotonic() + timeout
    last: Any = None
    while time.monotonic() < deadline:
        last = check()
        if last:
            return last
        time.sleep(interval)
    raise AssertionError(f"timed out after {timeout}s waiting for {description}; last value was {last!r}")


@pytest.fixture(scope="session")
def telemetry() -> None:
    """Confirms the OTLP endpoint is ingesting before suites seed.

    On a cold cast the collector first waits for ClickHouse migrations, so its
    published ports reset connections for the first few minutes. Poll until an
    empty export is accepted instead of failing the first seed attempt.
    """

    def ready() -> bool:
        try:
            resp = requests.post(f"{OTLP_ENDPOINT}/v1/logs", json={"resourceLogs": []}, timeout=10)
            # An empty export is a 200; a 4xx means the endpoint shape is wrong.
            if resp.status_code < 500:
                return True
            logger.info("OTLP endpoint not ready: HTTP %s: %s", resp.status_code, resp.text[:200])
        except requests.RequestException as err:
            logger.info("OTLP endpoint not ready: %s", err)
        return False

    wait_for(ready, "OTLP endpoint to accept exports", timeout=360.0)
