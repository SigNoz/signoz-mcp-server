import json
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


def _attr(key: str, value: str | float) -> dict:
    if isinstance(value, str):
        return {"key": key, "value": {"stringValue": value}}
    if isinstance(value, int):
        return {"key": key, "value": {"intValue": str(value)}}
    return {"key": key, "value": {"doubleValue": value}}


def seed_ai_trace(service_name: str, *, model: str = "gpt-4o-mini", llm_calls: int = 2, tool_calls: int = 12) -> str:
    """Push one AI agent trace via OTLP/HTTP and return its trace id (hex).

    The trace has an agent root span covering the whole trace, `llm_calls` LLM
    calls on `model` with gen_ai token usage and a per-call cost, and
    `tool_calls` calls alternating between two tools, so with the defaults
    trace.tool_call_count > 10 AND trace.distinct_tool_count < 3 holds. Every
    child span has a distinct duration. The last tool call is marked ERROR. Cost
    is set on the span directly; a pricing rule in SigNoz may restamp it at ingest.
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


def seed_ai_conversation(service_name: str, *, long_message_chars: int = 1500) -> str:
    """Push one AI agent conversation via OTLP/HTTP and return its trace id (hex).

    Spans in time order: an agent root, an LLM call whose user message is
    `long_message_chars` long and which asks for a tool, the tool call, a db
    child span with no gen_ai attributes, and a final LLM answer. LLM spans carry
    gen_ai.input.messages and gen_ai.output.messages in the OpenTelemetry GenAI
    JSON message format, so SigNoz can decode them into the trace thread.
    """
    now = time.time_ns()
    trace_id = uuid.uuid4().hex
    root_id = uuid.uuid4().hex[:16]
    start = now - 5_000_000_000

    def span(name: str, offset_ms: int, attributes: list[dict], *, span_id: str = "", parent: str = root_id) -> dict:
        begin = start + offset_ms * 1_000_000
        return {
            "traceId": trace_id,
            "spanId": span_id or uuid.uuid4().hex[:16],
            "parentSpanId": parent,
            "name": name,
            "kind": 1,  # SPAN_KIND_INTERNAL
            "startTimeUnixNano": str(begin),
            "endTimeUnixNano": str(begin + 50_000_000),
            "attributes": attributes,
            "status": {"code": 1},
        }

    def messages(value: list[dict]) -> str:
        return json.dumps(value)

    question = "Q" * long_message_chars
    tool_call = {"type": "tool_call", "id": "call_1", "name": "search_docs", "arguments": {"q": "pricing"}}
    tool_id = uuid.uuid4().hex[:16]
    spans = [
        span(
            "invoke_agent e2e-agent",
            0,
            [_attr("gen_ai.operation.name", "invoke_agent"), _attr("gen_ai.agent.name", "e2e-agent")],
            span_id=root_id,
            parent="",
        ),
        span(
            "chat gpt-4o-mini",
            100,
            [
                _attr("gen_ai.operation.name", "chat"),
                _attr("gen_ai.request.model", "gpt-4o-mini"),
                _attr(
                    "gen_ai.input.messages",
                    messages([{"role": "user", "parts": [{"type": "text", "content": question}]}]),
                ),
                _attr(
                    "gen_ai.output.messages",
                    messages([{"role": "assistant", "parts": [tool_call], "finish_reason": "tool_call"}]),
                ),
            ],
        ),
        span(
            "execute_tool search_docs",
            200,
            [_attr("gen_ai.operation.name", "execute_tool"), _attr("gen_ai.tool.name", "search_docs")],
            span_id=tool_id,
        ),
        span("SELECT docs", 250, [_attr("db.system", "postgresql")], parent=tool_id),
        span(
            "chat gpt-4o-mini",
            300,
            [
                _attr("gen_ai.operation.name", "chat"),
                _attr("gen_ai.request.model", "gpt-4o-mini"),
                _attr(
                    "gen_ai.output.messages",
                    messages(
                        [
                            {
                                "role": "assistant",
                                "parts": [{"type": "text", "content": "Done."}],
                                "finish_reason": "stop",
                            }
                        ]
                    ),
                ),
            ],
        ),
    ]
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
    logger.info("seeded AI conversation %s for service %s", trace_id, service_name)
    return trace_id


def seed_metrics(
    service_name: str,
    metric_name: str,
    *,
    value: float = 42.0,
    count: int = 1,
    age_seconds: float = 0,
) -> None:
    """Push gauge data points for `metric_name` via OTLP/HTTP.

    `age_seconds` lets tests place samples behind a completed query bucket.
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
