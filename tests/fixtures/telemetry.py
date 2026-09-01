import time
import uuid
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


def seed_traces(service_name: str, *, span_name: str = "e2e-operation", count: int = 1) -> list[str]:
    """Push spans to the cast SigNoz via OTLP/HTTP; return the trace ids (hex).

    The last span is marked ERROR so error=true/false searches both have data.
    """
    now = time.time_ns()
    trace_ids: list[str] = []
    spans = []
    for i in range(count):
        trace_id = uuid.uuid4().hex
        is_error = i == count - 1
        start = now - (count - i) * 1_000_000_000
        spans.append(
            {
                "traceId": trace_id,
                "spanId": uuid.uuid4().hex[:16],
                "name": span_name,
                "kind": 2,  # SPAN_KIND_SERVER
                "startTimeUnixNano": str(start),
                "endTimeUnixNano": str(start + 250_000_000),
                "attributes": [{"key": "e2e.marker", "value": {"stringValue": service_name}}],
                "status": {"code": 2 if is_error else 1},  # STATUS_CODE_ERROR / OK
            }
        )
        trace_ids.append(trace_id)
    payload = {
        "resourceSpans": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": service_name}},
                        {"key": "telemetry.sdk.language", "value": {"stringValue": "e2e"}},
                    ]
                },
                "scopeSpans": [{"scope": {"name": "signoz-mcp-e2e"}, "spans": spans}],
            }
        ]
    }
    resp = requests.post(f"{OTLP_ENDPOINT}/v1/traces", json=payload, timeout=30)
    resp.raise_for_status()
    logger.info("seeded %d span(s) for service %s", count, service_name)
    return trace_ids


def seed_metrics(service_name: str, metric_name: str, *, value: float = 42.0, count: int = 1) -> None:
    """Push gauge data points for `metric_name` via OTLP/HTTP."""
    now = time.time_ns()
    points = [
        {
            "asDouble": value + i,
            "startTimeUnixNano": str(now - 60_000_000_000),
            "timeUnixNano": str(now - i * 10_000_000_000),
            "attributes": [{"key": "service.name", "value": {"stringValue": service_name}}],
        }
        for i in range(count)
    ]
    payload = {
        "resourceMetrics": [
            {
                "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": service_name}}]},
                "scopeMetrics": [
                    {
                        "scope": {"name": "signoz-mcp-e2e"},
                        "metrics": [{"name": metric_name, "unit": "1", "gauge": {"dataPoints": points}}],
                    }
                ],
            }
        ]
    }
    resp = requests.post(f"{OTLP_ENDPOINT}/v1/metrics", json=payload, timeout=30)
    resp.raise_for_status()
    logger.info("seeded %d metric point(s) for %s", count, metric_name)


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
