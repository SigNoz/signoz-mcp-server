"""The step SigNoz applied, as the query tools report it, against the pinned release.

SigNoz raises a stepInterval below the window's minimum. The notes state
numbers that come from its rules, and each one is pinned here: for logs and
traces the minimum is the window over 1500 points (400 s over 7 days, so 60 s
fits 25 hours and 5 s fits 2 hours) and never below 5 s; for metrics it is
never below 60 s. Cost Meter reports its own minimum, which the note repeats.
"""

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
    assert _step_lines(week) == [
        (
            "note: SigNoz raised stepInterval from 60 s to 400 s, the minimum it allows for this window. "
            "For 60 s buckets, query a window of 25 hours or less."
        )
    ]
    # The window the note names allows the requested step, so no note.
    day = aggregate("25h", 60)
    assert (_applied_step(day), _step_lines(day)) == (60, []), "a 25-hour window must allow 60 s buckets"

    # The shortest window still has the 5 s floor; 2 hours allows it.
    minute = aggregate("1m", 1)
    assert _applied_step(minute) == 5, f"the logs and traces floor moved: {first_block_json(minute)['data']['meta']}"
    assert _step_lines(minute) == [
        (
            "note: SigNoz raised stepInterval from 1 s to 5 s, the minimum it allows for this window. "
            "Buckets are never smaller than 5 s."
        )
    ]
    two_hours = aggregate("2h", 5)
    assert (_applied_step(two_hours), _step_lines(two_hours)) == (5, []), "a 2-hour window must allow 5 s buckets"


def test_query_metrics_decision_names_the_raised_step(mcp_client: MCPClient, test_id: str, telemetry: None) -> None:
    metric = f"mcp_e2e_{test_id.replace('-', '_')}_gauge"
    seed_metrics(f"mcp-e2e-{test_id}", metric, value=1.0, count=2)

    def decision(time_range: str, step: int, source: str | None = None) -> tuple[int, list[str]] | None:
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
        steps = (first_block_json(result)["data"].get("meta") or {}).get("stepIntervals") or {}
        return (steps["A"], _step_lines(result)) if "A" in steps else None

    assert wait_for(lambda: decision("1h", 60), f"SigNoz reports a step for {metric}") == (
        60,
        ["stepInterval: 60s (caller-provided)"],
    )
    # 1 hour over 300 points is 12 s, so 60 s here is the floor, not the window.
    assert decision("1h", 10) == (
        60,
        [
            (
                "stepInterval: 60s (raised from the requested 10s, the minimum SigNoz allows for this window; "
                "metric steps are never smaller than 60s)"
            )
        ],
    )

    # Over 7 days the minimum is above both requests; a shorter window allows each.
    week = decision("7d", 10)
    assert week and week[0] > 60, f"SigNoz no longer raises 10 s over 7 days above the floor: {week}"
    assert week[1] == [
        (
            f"stepInterval: {week[0]}s (raised from the requested 10s, the minimum SigNoz allows for this window; "
            "metric steps are never smaller than 60s, so query a shorter window for 60s steps)"
        )
    ]
    week = decision("7d", 120)
    assert week and week[0] > 120, f"SigNoz no longer raises 120 s over 7 days: {week}"
    assert week[1] == [
        (
            f"stepInterval: {week[0]}s (raised from the requested 120s, the minimum SigNoz allows for this window; "
            "query a shorter window for 120s steps)"
        )
    ]
    assert decision("1h", 120) == (120, ["stepInterval: 120s (caller-provided)"])

    # Cost Meter has its own floor: the line names the step SigNoz applied, and passing that step is accepted.
    assert decision("1h", 60, source="meter") == (
        3600,
        [
            (
                "stepInterval: 3600s (raised from the requested 60s, the minimum SigNoz allows for this window; "
                "pass 3600 or more)"
            )
        ],
    )
    assert decision("1h", 3600, source="meter") == (3600, ["stepInterval: 3600s (caller-provided)"])
