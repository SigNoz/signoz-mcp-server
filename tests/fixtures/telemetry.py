import json
import subprocess
import time
import uuid
from dataclasses import dataclass
from typing import Any

import pytest
import requests

from fixtures.foundry import CLICKHOUSE
from fixtures.logger import setup_logger

logger = setup_logger(__name__)

# OTLP/HTTP endpoint of the cast SigNoz's OpenTelemetry collector. The foundry
# compose flavor publishes it on the host like the standard SigNoz compose
# deployment.
OTLP_ENDPOINT = "http://localhost:4318"

POLL_TIMEOUT = 180.0
POLL_INTERVAL = 3.0


def seed_logs(service_name: str, body: str, *, severity: str = "INFO", count: int = 1, age_seconds: float = 0) -> None:
    """Push log records carrying `body` to the cast SigNoz via OTLP/HTTP.

    `age_seconds` places the records that far in the past.
    """
    now = time.time_ns() - int(age_seconds * 1_000_000_000)
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


def seed_field_tokens(
    service_name: str,
    *,
    body_token: str,
    attribute_token: str,
    resource_token: str,
) -> None:
    """Seed one log whose body, attribute, and resource carry distinct tokens."""
    now = time.time_ns()
    payload = {
        "resourceLogs": [
            {
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": service_name}},
                        {"key": "telemetry.sdk.language", "value": {"stringValue": "e2e"}},
                        {"key": "e2e.resource.token", "value": {"stringValue": resource_token}},
                    ]
                },
                "scopeLogs": [
                    {
                        "scope": {"name": "signoz-mcp-e2e"},
                        "logRecords": [
                            {
                                "timeUnixNano": str(now),
                                "severityText": "INFO",
                                "body": {"stringValue": body_token},
                                "attributes": [
                                    {"key": "e2e.marker", "value": {"stringValue": body_token}},
                                    {"key": "e2e.attribute.token", "value": {"stringValue": attribute_token}},
                                ],
                            }
                        ],
                    }
                ],
            }
        ]
    }
    response = requests.post(f"{OTLP_ENDPOINT}/v1/logs", json=payload, timeout=30)
    response.raise_for_status()
    logger.info("seeded distinct body/attribute/resource tokens for %s", service_name)


def seed_traces(
    service_name: str, *, span_name: str = "e2e-operation", count: int = 1, with_child: bool = False
) -> list[str]:
    """Push spans to the cast SigNoz via OTLP/HTTP; return the trace ids (hex).

    The last span is marked ERROR so error=true/false searches both have data.
    `with_child` gives each trace a child span named `<span_name>-child` under its root.
    """
    now = time.time_ns()
    trace_ids: list[str] = []
    spans = []
    for i in range(count):
        trace_id = uuid.uuid4().hex
        span_id = uuid.uuid4().hex[:16]
        is_error = i == count - 1
        start = now - (count - i) * 1_000_000_000
        spans.append(
            {
                "traceId": trace_id,
                "spanId": span_id,
                "name": span_name,
                "kind": 2,  # SPAN_KIND_SERVER
                "startTimeUnixNano": str(start),
                "endTimeUnixNano": str(start + 250_000_000),
                "attributes": [{"key": "e2e.marker", "value": {"stringValue": service_name}}],
                "status": {"code": 2 if is_error else 1},  # STATUS_CODE_ERROR / OK
            }
        )
        if with_child:
            spans.append(
                {
                    "traceId": trace_id,
                    "spanId": uuid.uuid4().hex[:16],
                    "parentSpanId": span_id,
                    "name": f"{span_name}-child",
                    "kind": 1,  # SPAN_KIND_INTERNAL
                    "startTimeUnixNano": str(start + 50_000_000),
                    "endTimeUnixNano": str(start + 150_000_000),
                    "attributes": [{"key": "e2e.marker", "value": {"stringValue": service_name}}],
                    "status": {"code": 1},
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


def seed_service_calls(
    caller: str,
    callee: str,
    *,
    count: int,
    callee_duration_ns: int,
    environment: str,
    inner_server_span: bool = False,
    server_address: str | None = None,
) -> None:
    """Push `count` traces in which `caller` calls `callee` and every call fails.

    Each trace is a caller server span, a caller client span under it, and a
    callee server span under the client span, so SigNoz's dependency graph
    records a caller -> callee edge. Every span goes out in one export, because
    the graph pairs a parent and child span only when they are ingested together.
    Both services carry `environment` as deployment.environment, the key the
    graph stores for filtering. `inner_server_span` adds a second caller server
    span between the root and the client span, as a web framework's own span does.
    `server_address` sets server.address on the client spans, as HTTP client instrumentation does.
    """
    now = time.time_ns()
    caller_spans, callee_spans = [], []
    for i in range(count):
        trace_id = uuid.uuid4().hex
        root_id, client_id = uuid.uuid4().hex[:16], uuid.uuid4().hex[:16]
        start = now - (count - i) * 1_000_000_000
        end = start + callee_duration_ns + 20_000_000
        caller_spans.append(
            {
                "traceId": trace_id,
                "spanId": root_id,
                "name": "GET /checkout",
                "kind": 2,  # SPAN_KIND_SERVER
                "startTimeUnixNano": str(start),
                "endTimeUnixNano": str(end),
                "status": {"code": 1},
            }
        )
        client_parent = root_id
        if inner_server_span:
            client_parent = uuid.uuid4().hex[:16]
            caller_spans.append(
                {
                    "traceId": trace_id,
                    "spanId": client_parent,
                    "parentSpanId": root_id,
                    "name": "handle checkout",
                    "kind": 2,  # SPAN_KIND_SERVER
                    "startTimeUnixNano": str(start + 1_000_000),
                    "endTimeUnixNano": str(end - 1_000_000),
                    "status": {"code": 1},
                }
            )
        caller_spans += [
            {
                "traceId": trace_id,
                "spanId": client_id,
                "parentSpanId": client_parent,
                "name": "POST /reserve",
                "kind": 3,  # SPAN_KIND_CLIENT
                "startTimeUnixNano": str(start + 5_000_000),
                "endTimeUnixNano": str(end - 5_000_000),
                "status": {"code": 1},
                "attributes": [_attr("server.address", server_address)] if server_address else [],
            },
        ]
        callee_spans.append(
            {
                "traceId": trace_id,
                "spanId": uuid.uuid4().hex[:16],
                "parentSpanId": client_id,
                "name": "POST /reserve",
                "kind": 2,  # SPAN_KIND_SERVER
                "startTimeUnixNano": str(start + 10_000_000),
                "endTimeUnixNano": str(start + 10_000_000 + callee_duration_ns),
                "status": {"code": 2},  # STATUS_CODE_ERROR
            }
        )

    def resource_spans(service_name: str, spans: list[dict]) -> dict:
        return {
            "resource": {
                "attributes": [_attr("service.name", service_name), _attr("deployment.environment", environment)]
            },
            "scopeSpans": [{"scope": {"name": "signoz-mcp-e2e"}, "spans": spans}],
        }

    payload = {"resourceSpans": [resource_spans(caller, caller_spans), resource_spans(callee, callee_spans)]}
    resp = requests.post(f"{OTLP_ENDPOINT}/v1/traces", json=payload, timeout=30)
    resp.raise_for_status()
    logger.info("seeded %d call(s) from %s to %s", count, caller, callee)


def _attr(key: str, value: str | float) -> dict:
    if isinstance(value, str):
        return {"key": key, "value": {"stringValue": value}}
    if isinstance(value, int):
        return {"key": key, "value": {"intValue": str(value)}}
    return {"key": key, "value": {"doubleValue": value}}


# The child span seed_ai_trace adds without gen_ai attributes.
PLAIN_AI_CHILD_SPAN = "SELECT documents"


def seed_ai_trace(service_name: str, *, model: str = "gpt-4o-mini", llm_calls: int = 2, tool_calls: int = 12) -> str:
    """Push one AI agent trace via OTLP/HTTP and return its trace id (hex).

    The trace has an agent root span covering the whole trace, `llm_calls` LLM
    calls on `model` with gen_ai token usage and a per-call cost, and
    `tool_calls` calls alternating between two tools, so with the defaults
    trace.tool_call_count > 10 AND trace.distinct_tool_count < 3 holds. Every
    child span has a distinct duration. The last tool call is marked ERROR. One
    more child span, PLAIN_AI_CHILD_SPAN, has no gen_ai attributes. Cost
    is set on the span directly, but SigNoz stamps cost at ingest and a fresh
    SigNoz reads it back as null, so tests assert on token counts instead.
    """
    now = time.time_ns()
    trace_id = uuid.uuid4().hex
    root_id = uuid.uuid4().hex[:16]
    start = now - 5_000_000_000

    def span(
        name: str,
        offset_ms: int,
        duration_ms: int,
        attributes: list[dict],
        *,
        parent: str = root_id,
        error: bool = False,
    ) -> dict:
        begin = start + offset_ms * 1_000_000
        return {
            "traceId": trace_id,
            "spanId": uuid.uuid4().hex[:16],
            "parentSpanId": parent,
            "name": name,
            "kind": 1,  # SPAN_KIND_INTERNAL
            "startTimeUnixNano": str(begin),
            "endTimeUnixNano": str(begin + duration_ms * 1_000_000),
            "attributes": attributes,
            "status": {"code": 2 if error else 1},
        }

    root_duration_ms = 400 + tool_calls * 60 + 50
    root = span(
        "invoke_agent e2e-agent",
        0,
        root_duration_ms,
        [_attr("gen_ai.operation.name", "invoke_agent"), _attr("gen_ai.agent.name", "e2e-agent")],
        parent="",
    )
    root["spanId"] = root_id
    spans = [root]
    for i in range(llm_calls):
        spans.append(
            span(
                f"chat {model}",
                100 + i * 100,
                80 + i * 5,
                [
                    _attr("gen_ai.operation.name", "chat"),
                    _attr("gen_ai.provider.name", "openai"),
                    _attr("gen_ai.request.model", model),
                    _attr("gen_ai.usage.input_tokens", 1000),
                    _attr("gen_ai.usage.output_tokens", 200),
                    _attr("signoz.gen_ai.usage.tokens.cost", 0.0025),
                ],
            )
        )
    for i in range(tool_calls):
        tool = "search_docs" if i % 2 == 0 else "read_file"
        spans.append(
            span(
                f"execute_tool {tool}",
                400 + i * 60,
                (i + 1) * 4,
                [_attr("gen_ai.operation.name", "execute_tool"), _attr("gen_ai.tool.name", tool)],
                error=i == tool_calls - 1,
            )
        )
    spans.append(span(PLAIN_AI_CHILD_SPAN, 350, 30, [_attr("db.system", "postgresql")]))
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
    logger.info("seeded AI trace %s (%d spans) for service %s", trace_id, len(spans), service_name)
    return trace_id


def seed_metrics(
    service_name: str,
    metric_name: str,
    *,
    value: float = 42.0,
    count: int = 1,
    age_seconds: float = 0,
    kind: str = "gauge",
) -> None:
    """Push data points for `metric_name` via OTLP/HTTP.

    `kind` is "gauge", "counter" (cumulative monotonic sum rising to
    value+count-1 at the newest point), or "histogram" (delta, bounds 10 and 100, one
    observation per bucket). `age_seconds` lets tests place samples behind a
    completed query bucket.
    """
    now = time.time_ns() - int(age_seconds * 1_000_000_000)
    points = [
        {
            "asDouble": value + i,
            "startTimeUnixNano": str(now - 60_000_000_000),
            "timeUnixNano": str(now - i * 10_000_000_000),
            "attributes": [{"key": "service.name", "value": {"stringValue": service_name}}],
        }
        for i in range(count)
    ]
    if kind == "gauge":
        data = {"gauge": {"dataPoints": points}}
    elif kind == "counter":
        # Newest point first in `points`, so it gets the largest running total.
        for i, point in enumerate(points):
            point["asDouble"] = value + (count - 1 - i)
        data = {"sum": {"dataPoints": points, "aggregationTemporality": 2, "isMonotonic": True}}
    elif kind == "histogram":
        for point in points:
            del point["asDouble"]
            point.update({"count": "3", "sum": 165.0, "bucketCounts": ["1", "1", "1"], "explicitBounds": [10.0, 100.0]})
        data = {"histogram": {"dataPoints": points, "aggregationTemporality": 1}}
    else:
        raise ValueError(f"unknown metric kind {kind!r}")
    payload = {
        "resourceMetrics": [
            {
                "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": service_name}}]},
                "scopeMetrics": [
                    {
                        "scope": {"name": "signoz-mcp-e2e"},
                        "metrics": [{"name": metric_name, "unit": "1", **data}],
                    }
                ],
            }
        ]
    }
    resp = requests.post(f"{OTLP_ENDPOINT}/v1/metrics", json=payload, timeout=30)
    resp.raise_for_status()
    logger.info("seeded %d metric point(s) for %s", count, metric_name)


def _post_metric(service_name: str, metric: dict) -> None:
    payload = {
        "resourceMetrics": [
            {
                "resource": {"attributes": [_attr("service.name", service_name)]},
                "scopeMetrics": [{"scope": {"name": "signoz-mcp-e2e"}, "metrics": [metric]}],
            }
        ]
    }
    resp = requests.post(f"{OTLP_ENDPOINT}/v1/metrics", json=payload, timeout=30)
    resp.raise_for_status()


def seed_histogram_minutes(
    service_name: str, metric_name: str, minutes: list[list[int]], bounds: list[float], *, cumulative: bool
) -> int:
    """Push one histogram point per minute, `minutes[i]` holding that minute's bucket counts.

    The points sit mid-minute in completed minutes that end two minutes ago, so a 60s step
    puts each in its own bucket. A cumulative histogram starts with an all-zero point
    and then carries running totals; a delta one carries each minute's counts.
    Returns the start of the first minute in Unix nanoseconds.
    """
    first = int((time.time() // 60 - len(minutes) - 2) * 60) * 1_000_000_000
    started = first - 60_000_000_000
    attributes = [_attr("service.name", service_name)]

    def point(at_ns: int, counts: list[int], start_ns: int) -> dict:
        return {
            "startTimeUnixNano": str(start_ns),
            "timeUnixNano": str(at_ns),
            "count": str(sum(counts)),
            "sum": 0.0,
            "bucketCounts": [str(c) for c in counts],
            "explicitBounds": bounds,
            "attributes": attributes,
        }

    points = [point(first - 30_000_000_000, [0] * len(minutes[0]), started)] if cumulative else []
    running = [0] * len(minutes[0])
    for i, counts in enumerate(minutes):
        at = first + (i * 60 + 30) * 1_000_000_000
        running = [a + b for a, b in zip(running, counts)]
        if cumulative:
            points.append(point(at, running, started))
        else:
            points.append(point(at, counts, at - 60_000_000_000))
    temporality = 2 if cumulative else 1
    _post_metric(
        service_name,
        {"name": metric_name, "unit": "ms", "histogram": {"dataPoints": points, "aggregationTemporality": temporality}},
    )
    logger.info("seeded %d histogram minute(s) for %s", len(minutes), metric_name)
    return first


def seed_steady_counter(service_name: str, metric_name: str, *, per_second: float, minutes: int) -> None:
    """Push a cumulative counter rising `per_second` with a point every 10s for `minutes` minutes up to one minute ago."""
    end = int(time.time() - 60) * 1_000_000_000
    count = minutes * 6 + 1
    first = end - (count - 1) * 10_000_000_000
    points = [
        {
            "asDouble": per_second * 10 * i,
            "startTimeUnixNano": str(first - 10_000_000_000),
            "timeUnixNano": str(first + i * 10_000_000_000),
            "attributes": [_attr("service.name", service_name)],
        }
        for i in range(count)
    ]
    _post_metric(
        service_name,
        {
            "name": metric_name,
            "unit": "1",
            "sum": {"dataPoints": points, "aggregationTemporality": 2, "isMonotonic": True},
        },
    )
    logger.info("seeded %d counter point(s) for %s", count, metric_name)


@dataclass(frozen=True)
class AuditEvent:
    """One SigNoz audit event; attributes and resources use the audit attribute names."""

    body: str
    event_name: str
    attributes: dict[str, str]
    resources: dict[str, str]
    severity: str = "INFO"


def seed_audit_events(events: list[AuditEvent]) -> None:
    """Insert audit events straight into signoz_audit, as SigNoz's querier tests do.

    The cast SigNoz has no license, and its auditor drops events without one, so
    no API call records a real audit event here.
    """
    now = time.time_ns()
    resource_rows: dict[str, dict] = {}
    log_rows = []
    for i, event in enumerate(events):
        timestamp = now - i * 1_000_000
        bucket = timestamp // 1_000_000_000 // 1800 * 1800
        resources = {"service.name": "signoz", **event.resources}
        # The querier joins rows to resources by fingerprint; any value both tables share works.
        fingerprint = ";".join(f"{key}={resources[key]}" for key in sorted(resources))
        resource_rows[fingerprint] = {
            "labels": json.dumps(resources, separators=(",", ":")),
            "fingerprint": fingerprint,
            "seen_at_ts_bucket_start": bucket,
        }
        log_rows.append(
            {
                "ts_bucket_start": bucket,
                "resource_fingerprint": fingerprint,
                "timestamp": timestamp,
                "observed_timestamp": timestamp,
                "id": f"{timestamp:020d}-{uuid.uuid4().hex[:8]}",
                "trace_id": "",
                "span_id": "",
                "trace_flags": 0,
                "severity_text": event.severity,
                "severity_number": 9 if event.severity == "INFO" else 17,
                "body": {"message": event.body},
                "scope": {"name": "signoz.audit", "version": "", "attributes": {}},
                "attributes": event.attributes,
                "resource": {"attributes": resources},
                "event_name": event.event_name,
            }
        )
    _clickhouse_insert("signoz_audit.distributed_logs_resource", list(resource_rows.values()))
    _clickhouse_insert("signoz_audit.distributed_logs", log_rows)
    logger.info("seeded %d audit event(s)", len(events))


def _clickhouse_insert(table: str, rows: list[dict]) -> None:
    subprocess.run(
        ["docker", "exec", "-i", CLICKHOUSE, "clickhouse-client", "--query", f"INSERT INTO {table} FORMAT JSONEachRow"],
        input="\n".join(json.dumps(row) for row in rows),
        text=True,
        capture_output=True,
        check=True,
    )


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
