import json
from dataclasses import dataclass, field
from typing import Any

import pytest
import requests

from fixtures.logger import setup_logger
from fixtures.mcpserver import MCPServer

logger = setup_logger(__name__)

# The legacy protocol era the server's HTTP transport negotiates (see
# scripts/test-mcp-protocol.sh).
PROTOCOL_VERSION = "2025-11-25"
CLIENT_INFO = {"name": "signoz-mcp-e2e", "version": "1.0.0"}


class MCPProtocolError(AssertionError):
    """A JSON-RPC error object came back for a request."""


@dataclass
class MCPClient:
    """Thin JSON-RPC-over-HTTP client for the stateless MCP HTTP transport.

    Each request is an independent POST; the server issues no session id.
    Deliberately hand-rolled: protocol conformance is owned by the inspector /
    conformance lanes, this client only carries tool calls to a live backend.
    """

    mcp_url: str
    session: requests.Session = field(default_factory=requests.Session)
    _next_id: int = 0
    _initialized: bool = False

    def _parse(self, resp: requests.Response) -> dict:
        content_type = resp.headers.get("content-type", "")
        if content_type.startswith("text/event-stream"):
            payload = None
            for line in resp.text.splitlines():
                if line.startswith("data:"):
                    payload = json.loads(line.removeprefix("data:").strip())
            if payload is None:
                raise MCPProtocolError(f"SSE response carried no data frame: {resp.text[:400]}")
            return payload
        return resp.json()

    def rpc(self, method: str, params: dict | None = None, *, expect_result: bool = True) -> Any:
        self._next_id += 1
        request_id = self._next_id
        body = {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}}
        logger.info("mcp -> %s (id=%d)", method, request_id)
        resp = self.session.post(
            self.mcp_url,
            json=body,
            headers={
                "Content-Type": "application/json",
                "Accept": "application/json, text/event-stream",
            },
            timeout=60,
        )
        assert resp.status_code == 200, f"{method} returned HTTP {resp.status_code}: {resp.text[:400]}"
        payload = self._parse(resp)
        assert payload.get("id") == request_id, f"{method} response id mismatch: {payload}"
        if "error" in payload:
            raise MCPProtocolError(f"{method} returned JSON-RPC error: {payload['error']}")
        return payload.get("result") if expect_result else payload

    def initialize(self) -> dict:
        result = self.rpc(
            "initialize",
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": CLIENT_INFO,
            },
        )
        self.session.post(
            self.mcp_url,
            json={"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}},
            headers={
                "Content-Type": "application/json",
                "Accept": "application/json, text/event-stream",
            },
            timeout=30,
        )
        self._initialized = True
        return result

    def list_tools(self) -> list[dict]:
        return self.rpc("tools/list")["tools"]

    def call_tool(self, name: str, arguments: dict | None = None) -> dict:
        """Call a tool and return the raw result object (isError may be true)."""
        if not self._initialized:
            self.initialize()
        return self.rpc("tools/call", {"name": name, "arguments": arguments or {}})


def text_blocks(result: dict) -> str:
    """Concatenate the text content blocks of a CallToolResult."""
    return "\n".join(block.get("text", "") for block in result.get("content", []) if block.get("type") == "text")


def first_json(result: dict) -> Any:
    """Parse the first text block of a CallToolResult as JSON."""
    text = text_blocks(result)
    assert text, f"result carried no text content: {result}"
    return json.loads(text)


def assert_tool_ok(result: dict) -> dict:
    assert not result.get("isError", False), f"tool returned error: {text_blocks(result)[:600]}"
    return result


@pytest.fixture(scope="session")
def mcp_client(mcp_server: MCPServer) -> MCPClient:
    client = MCPClient(mcp_url=mcp_server.mcp_url)
    client.initialize()
    return client
