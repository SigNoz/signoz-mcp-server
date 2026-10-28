"""AI pricing and attribute mapping reads against real SigNoz.

The pricing tool pages SigNoz's pricing rules and the unpriced-model list, and the
mappings tool joins each mapping group with its mappers. Both parse real responses here
so upstream drift fails a test, not a user. Self-hosted SigNoz ships no prices, so
the rules view is checked for its envelope, and a seeded model no rule prices gives the
unpriced view a real entry.
"""

import uuid

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.results import assert_structured_matches_text, first_block_json
from fixtures.telemetry import seed_ai_trace, wait_for


def test_ai_pricing_views_return_paginated_envelopes(mcp_client: MCPClient) -> None:
    """Both views return a list and pagination metadata consistent with it."""
    for view in ("rules", "unpriced_models"):
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_ai_pricing",
                {"searchContext": f"which AI model prices does SigNoz use ({view})", "view": view},
            )
        )
        assert_structured_matches_text(result)
        payload = first_block_json(result)
        assert payload["view"] == view, payload
        assert isinstance(payload["data"], list), payload
        pagination = payload["pagination"]
        assert pagination["total"] >= len(payload["data"]), pagination
        assert pagination["hasMore"] == (pagination["nextOffset"] != -1), pagination


def test_ai_pricing_lists_a_seeded_model_as_unpriced(mcp_client: MCPClient, telemetry: None) -> None:
    """A model no rule prices shows up in unpriced_models, found by searchText, with its span count."""
    model = f"e2e-model-{uuid.uuid4().hex[:8]}"
    seed_ai_trace(f"mcp-e2e-pricing-{model}", model=model, llm_calls=2, tool_calls=0)

    def unpriced() -> list[dict]:
        result = assert_tool_ok(
            mcp_client.call_tool(
                "signoz_get_ai_pricing",
                {"searchContext": f"is {model} priced", "view": "unpriced_models", "searchText": model.upper()},
            )
        )
        return first_block_json(result)["data"]

    entries = wait_for(unpriced, f"{model} listed as unpriced")
    assert [entry["modelName"] for entry in entries] == [model], entries
    assert entries[0]["spanCount"] >= 2, entries


def test_ai_attribute_mappings_include_the_shipped_llm_group(mcp_client: MCPClient) -> None:
    """SigNoz provisions the gen_ai.llm group; its mappers come back with source keys."""
    result = assert_tool_ok(
        mcp_client.call_tool(
            "signoz_list_ai_attribute_mappings",
            {"searchContext": "which vendor attributes does SigNoz map to gen_ai"},
        )
    )
    payload = first_block_json(result)
    assert payload["incomplete"] is False, payload.get("failedGroups")
    groups = {group["name"]: group for group in payload["data"]}
    llm = groups.get("gen_ai.llm")
    assert llm is not None, f"shipped gen_ai.llm group missing: {sorted(groups)}"
    targets = {mapper["name"]: mapper for mapper in llm["mappers"]}
    model = targets.get("gen_ai.request.model")
    assert model is not None, f"gen_ai.request.model mapper missing: {sorted(targets)}"
    source_keys = {source["key"] for source in model["config"]["sources"]}
    assert "llm.model_name" in source_keys, source_keys
