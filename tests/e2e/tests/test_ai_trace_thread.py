"""signoz_get_trace_details view=thread against real SigNoz.

The thread endpoint is alpha upstream, so these tests pin what the tool relies
on: SigNoz returns a trace's model, tool, and agent spans in time order with
decoded messages and cursor paging, and the tool drops the raw message
attributes and cuts long message values.
"""

import uuid

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, result_code
from fixtures.telemetry import seed_ai_conversation, wait_for

# seed_ai_conversation spans, in time order, without the db span SigNoz leaves out.
THREAD_SPANS = ["invoke_agent e2e-agent", "chat gpt-4o-mini", "execute_tool search_docs", "chat gpt-4o-mini"]


def thread(client: MCPClient, trace_id: str, **arguments: object) -> dict:
    result = client.call_tool(
        "signoz_get_trace_details",
        {"searchContext": "show the agent conversation", "traceId": trace_id, "view": "thread", **arguments},
    )
    return first_block_json(assert_tool_ok(result))


@pytest.fixture(scope="module")
def conversation(mcp_client: MCPClient, telemetry: None) -> str:
    trace_id = seed_ai_conversation(f"mcp-e2e-thread-{uuid.uuid4().hex[:8]}")

    def ingested() -> bool:
        # SigNoz answers NOT_FOUND until the trace is ingested.
        result = mcp_client.call_tool(
            "signoz_get_trace_details",
            {"searchContext": "show the agent conversation", "traceId": trace_id, "view": "thread"},
        )
        return not result.get("isError") and len(first_block_json(result)["spans"]) == len(THREAD_SPANS)

    wait_for(ingested, "seeded conversation in the thread")
    return trace_id


def test_thread_returns_ai_spans_in_order_with_decoded_messages(mcp_client: MCPClient, conversation: str) -> None:
    payload = thread(mcp_client, conversation)
    spans = payload["spans"]
    assert [span["name"] for span in spans] == THREAD_SPANS, spans
    first_call = spans[1]
    assert first_call["formattedOutput"][0]["parts"][0]["name"] == "search_docs", first_call["formattedOutput"]
    assert "gen_ai.input.messages" not in first_call["attributes"], sorted(first_call["attributes"])
    assert "gen_ai.output.messages" not in first_call["attributes"], sorted(first_call["attributes"])


def test_thread_cuts_long_messages_and_says_so(mcp_client: MCPClient, conversation: str) -> None:
    payload = thread(mcp_client, conversation)
    first_call = payload["spans"][1]
    content = first_call["formattedInput"][0]["parts"][0]["content"]
    assert content == "Q" * 1000, len(content)
    assert first_call["truncated"] is True
    assert payload["truncated"] is True
    assert "signoz_search_traces" in payload["nextAction"], payload["nextAction"]


def test_thread_pages_by_cursor_without_gaps(mcp_client: MCPClient, conversation: str) -> None:
    first = thread(mcp_client, conversation, limit=2)
    assert first.get("nextCursor"), first
    second = thread(mcp_client, conversation, limit=2, after=first["nextCursor"])
    ids = [span["span_id"] for span in first["spans"] + second["spans"]]
    assert len(ids) == len(THREAD_SPANS) and len(set(ids)) == len(ids), ids
    assert "nextCursor" not in second, second


def test_thread_of_an_unknown_trace_is_not_found(mcp_client: MCPClient, telemetry: None) -> None:
    result = mcp_client.call_tool(
        "signoz_get_trace_details",
        {"searchContext": "show the agent conversation", "traceId": uuid.uuid4().hex, "view": "thread"},
    )
    assert result.get("isError", False), result
    assert result_code(result) == "NOT_FOUND"
