"""The step SigNoz applied, as the query tools report it, against the pinned release.

SigNoz raises a stepInterval below the window's minimum. The notes and the
metrics guide state numbers that come from its rules, and this suite checks
each against the real backend: for logs and traces the minimum over 7 days is
400 s and never below 5 s, and the window a note names for the requested
buckets allows them; a metric step is raised over a long window; and a Cost
Meter step is never below 3600 s, which SigNoz accepts when passed. The
handler tests pin the notes' wording.
"""

import re

import pytest

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import first_block_json, note_blocks
from fixtures.telemetry import seed_metrics, wait_for


def _applied_step(result: dict) -> int:
    """The step SigNoz reports it ran query A at."""
    return first_block_json(result)["data"]["meta"]["stepIntervals"]["A"]


def _step_lines(result: dict) -> list[str]:
    """Note lines that mention stepInterval."""
    return [line.strip() for line in note_blocks(result).splitlines() if "stepInterval" in line]


def _hours_named(line: str) -> int:
    """The window, in hours, a step note says allows the buckets it names."""
    named = re.search(r"window of (\d+) hours or less", line)
    assert named, f"the note names no window: {line}"
    return int(named.group(1))


@pytest.mark.parametrize("tool", ["signoz_aggregate_logs", "signoz_aggregate_traces"])
def test_aggregate_note_names_the_raised_step_and_a_window_that_allows_the_requested_one(
    mcp_client: MCPClient, tool: str
) -> None:
    def aggregate(time_range: str, step: int) -> dict:
        return assert_tool_ok(
            mcp_client.call_tool(
                tool,
                {
                    "searchContext": "count over time in small buckets",
                    "aggregations": "count()",
                    "requestType": "time_series",
                    "timeRange": time_range,
                    "stepInterval": step,
                },
            )
        )

    week = aggregate("7d", 60)
    assert _applied_step(week) == 400, f"SigNoz's minimum over 7 days moved: {first_block_json(week)['data']['meta']}"
    notes = _step_lines(week)
    assert len(notes) == 1 and "400" in notes[0], f"one note must carry the step SigNoz applied: {notes}"
    # The window the note names allows the requested step, so no note.
    hours = _hours_named(notes[0])
    allowed = aggregate(f"{hours}h", 60)
    assert (_applied_step(allowed), _step_lines(allowed)) == (60, []), f"a {hours}-hour window must allow 60 s buckets"

    # The shortest window still runs at the 5 s floor.
    minute = aggregate("1m", 1)
    assert _applied_step(minute) == 5, f"the logs and traces floor moved: {first_block_json(minute)['data']['meta']}"
    assert len(_step_lines(minute)) == 1, f"a step raised to the floor is reported once: {_step_lines(minute)}"
    # Raised past the floor, the note names a window for 5 s buckets, and that window allows them.
    notes = _step_lines(aggregate("7d", 2))
    assert len(notes) == 1, f"a step raised past the floor is reported once: {notes}"
    hours = _hours_named(notes[0])
    allowed = aggregate(f"{hours}h", 5)
    assert (_applied_step(allowed), _step_lines(allowed)) == (5, []), f"a {hours}-hour window must allow 5 s buckets"


def test_metric_and_cost_meter_steps_are_raised_to_the_window_minimum(
    mcp_client: MCPClient, test_id: str, telemetry: None
) -> None:
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, value=1.0, count=2)

    def applied_step(time_range: str, step: int, source: str | None = None) -> int | None:
        """The step SigNoz reports it ran the query at, or None while it reports none."""
        arguments = {
            "searchContext": f"{metric} over time in small steps",
            "metricName": metric,
            "metricType": "gauge",
            "timeRange": time_range,
            "stepInterval": step,
        }
        if source:
            arguments["source"] = source
        result = mcp_client.call_tool("signoz_query_metrics", arguments)
        if result.get("isError", False):
            return None
        # SigNoz reports no step for a metric it cannot find in the window yet.
        return ((first_block_json(result)["data"].get("meta") or {}).get("stepIntervals") or {}).get("A")

    wait_for(lambda: applied_step("1h", 60), f"SigNoz reports a step for {metric}")
    week = applied_step("7d", 120)
    assert week and week > 120, f"SigNoz no longer raises a 120 s step over 7 days: {week}"

    # The guide says a Cost Meter step is never below 3600 s, and that passing 3600 gives hourly buckets.
    assert applied_step("1h", 60, source="meter") == 3600, "the Cost Meter floor moved"
    assert applied_step("1h", 3600, source="meter") == 3600, "Cost Meter must accept its own minimum"
