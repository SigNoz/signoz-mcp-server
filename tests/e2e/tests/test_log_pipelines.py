"""Log pipeline read tools against the pinned SigNoz.

The tools are read-only, so a fresh cast has no user pipelines. The listing
always runs; the get call uses a returned id when one exists and otherwise
verifies the coded NOT_FOUND path for an absent id.
"""

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import assert_structured_matches_text, first_block_json, result_code

GHOST_PIPELINE_ID = "01900000-0000-7000-8000-000000000000"


def test_list_log_pipelines_returns_a_consistent_envelope(mcp_client: MCPClient) -> None:
    result = assert_tool_ok(mcp_client.call_tool("signoz_list_log_pipelines", {"searchContext": "list log pipelines"}))
    assert_structured_matches_text(result)
    payload = first_block_json(result)
    pipelines = payload["pipelines"]
    assert isinstance(pipelines, list)
    assert payload["total"] == len(pipelines)

    for pipeline in pipelines:
        assert isinstance(pipeline["id"], str) and pipeline["id"]
        assert isinstance(pipeline["name"], str)
        assert isinstance(pipeline["enabled"], bool)
        assert isinstance(pipeline["processorCount"], int)
        assert "config" not in pipeline
        assert "filter" not in pipeline


def test_get_log_pipeline_returns_configured_processors_or_coded_not_found(mcp_client: MCPClient) -> None:
    listing = first_block_json(
        assert_tool_ok(mcp_client.call_tool("signoz_list_log_pipelines", {"searchContext": "find a pipeline id"}))
    )
    pipelines = listing["pipelines"]
    if not pipelines:
        result = mcp_client.call_tool(
            "signoz_get_log_pipeline",
            {"searchContext": "get an absent pipeline", "id": GHOST_PIPELINE_ID},
        )
        assert result.get("isError", False), "an absent pipeline id should be an error result"
        assert result_code(result) == "NOT_FOUND"
        return

    pipeline_id = pipelines[0]["id"]
    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_get_log_pipeline", {"searchContext": f"get pipeline {pipeline_id}", "id": pipeline_id}
        )
    )
    assert_structured_matches_text(result)
    pipeline = first_block_json(result)
    assert pipeline["id"] == pipeline_id
    assert isinstance(pipeline["config"], (list, type(None)))
    assert isinstance(pipeline["filter"], (dict, type(None)))
