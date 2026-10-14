"""Pure helpers for parsing MCP CallToolResult wire dicts.

Ports of the Go e2e helpers (countDataArrayRows, noteBlocks,
assertStructuredMatchesText, codeOf) so the ported suites assert the same
contracts by the same rules.
"""

import json
from typing import Any


def first_text_block(result: dict) -> str:
    """Text of content block 0 (the JSON payload block)."""
    content = result.get("content", [])
    if not content:
        return ""
    block = content[0]
    return block.get("text", "") if block.get("type") == "text" else ""


def note_blocks(result: dict) -> str:
    """All text blocks after block 0 (completeness / clamp / warning notes)."""
    content = result.get("content", [])
    if len(content) < 2:
        return ""
    return "\n".join(b.get("text", "") for b in content[1:] if b.get("type") == "text")


def first_block_json(result: dict) -> Any:
    """Parse content block 0 as JSON."""
    text = first_text_block(result)
    assert text, f"result carried no text block 0: {result}"
    return json.loads(text)


def count_data_array_rows(body: str, key: str) -> tuple[int, bool]:
    """Count elements of the array at data.<key> (e.g. metrics, samples)."""
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        return 0, False
    data = payload.get("data")
    if not isinstance(data, dict):
        return 0, False
    if key not in data:
        return 0, False
    value = data[key]
    if value is None:
        return 0, True
    if not isinstance(value, list):
        return 0, False
    return len(value), True


def assert_structured_matches_text(result: dict) -> None:
    """structuredContent must round-trip to the same JSON as text block 0."""
    structured = result.get("structuredContent")
    assert structured is not None, (
        f"code-controlled tool must populate structuredContent: {first_text_block(result)[:400]}"
    )
    text = first_text_block(result)
    parsed = json.loads(text)
    assert parsed == structured, "structuredContent does not match text block 0"


def assert_no_structured(result: dict) -> None:
    """Raw passthrough tools must NOT populate structuredContent."""
    assert result.get("structuredContent") is None, "raw passthrough must not populate structuredContent"


def result_code(result: dict) -> str:
    """The machine-readable code from an error result's structuredContent."""
    structured = result.get("structuredContent")
    assert isinstance(structured, dict), f"error result missing structuredContent: {first_text_block(result)[:400]}"
    code = structured.get("code")
    assert isinstance(code, str), f"structuredContent.code missing: {structured}"
    return code


def query_range_rows(body: str) -> list[dict]:
    """All row.data maps across data.data.results[].rows[] (fails on drift)."""
    payload = json.loads(body)
    rows = []
    for result in payload["data"]["data"]["results"]:
        for row in result.get("rows") or []:
            rows.append(row["data"])
    return rows


def aggregate_columns_and_row_count(body: str) -> tuple[list[str], int]:
    """Column names and total row count of an aggregate query_range body."""
    payload = json.loads(body)
    columns: list[str] = []
    count = 0
    for result in payload["data"]["data"]["results"]:
        for column in result.get("columns") or []:
            if isinstance(column, dict) and column.get("name"):
                columns.append(column["name"])
            elif isinstance(column, str):
                columns.append(column)
        data = result.get("data")
        if isinstance(data, list):
            count += len(data)
    return columns, count


def dig_id(payload: dict) -> str:
    """Extract an id from a create-channel-style body (channel wrapper or data)."""
    channel = payload.get("channel")
    if isinstance(channel, dict):
        found = _dig(channel)
        if found:
            return found
    return _dig(payload)


def _dig(mapping: dict) -> str:
    value = mapping.get("id")
    if isinstance(value, str) and value:
        return value
    data = mapping.get("data")
    if isinstance(data, dict):
        value = data.get("id")
        if isinstance(value, str) and value:
            return value
    return ""
