import asyncio
import threading
from collections.abc import Coroutine
from contextlib import AsyncExitStack
from typing import Any

import pytest
from mcp import ClientSession
from mcp.client.streamable_http import create_mcp_http_client, streamable_http_client

from fixtures.logger import setup_logger
from fixtures.mcpserver import MCPServer

logger = setup_logger(__name__)

CALL_TIMEOUT = 90.0


class MCPClient:
    """Sync facade over the official Python MCP SDK's streamable-HTTP client.

    The SDK is async; the suite is sync, so a dedicated event loop runs in a
    daemon thread and every call is dispatched to it. The transport and session
    live inside one long-lived task — anyio cancel scopes must be entered and
    exited in the same task. Public methods return plain dicts (JSON wire
    shape) so assertions read like the protocol.
    """

    def __init__(self, mcp_url: str, headers: dict | None = None):
        self.mcp_url = mcp_url
        self._headers = headers
        self._loop = asyncio.new_event_loop()
        self._thread = threading.Thread(target=self._loop.run_forever, daemon=True, name="mcp-client-loop")
        self._thread.start()
        self._session: ClientSession | None = None
        self._close_requested = asyncio.Event()
        self._ready = threading.Event()
        self._client_task = None

    def _run(self, coro: Coroutine, timeout: float = CALL_TIMEOUT) -> Any:
        return asyncio.run_coroutine_threadsafe(coro, self._loop).result(timeout)

    @staticmethod
    def _dump(model: Any) -> dict:
        return model.model_dump(mode="json", by_alias=True, exclude_none=True)

    def initialize(self) -> dict:
        async def _hold_session():
            # terminate_on_close=False: the server is stateless and has no
            # session to DELETE.
            http_client = create_mcp_http_client(headers=self._headers) if self._headers else None
            async with AsyncExitStack() as stack:
                read, write = await stack.enter_async_context(
                    streamable_http_client(self.mcp_url, http_client=http_client, terminate_on_close=False)
                )
                self._session = await stack.enter_async_context(ClientSession(read, write))
                init = await self._session.initialize()
                logger.info("mcp initialize: protocol=%s server=%s", init.protocol_version, init.server_info.name)
                self._init_result = init
                self._ready.set()
                await self._close_requested.wait()

        self._client_task = asyncio.run_coroutine_threadsafe(_hold_session(), self._loop)
        if not self._ready.wait(timeout=CALL_TIMEOUT):
            self._client_task.cancel()
            raise TimeoutError(f"initialize against {self.mcp_url} timed out")
        if self._client_task.done():
            # Startup failed before the ready signal; surface the cause.
            self._client_task.result()
        return self._dump(self._init_result)

    def list_tools(self) -> list[dict]:
        assert self._session is not None, "initialize() first"
        result = self._run(self._session.list_tools())
        return [self._dump(tool) for tool in result.tools]

    def call_tool(self, name: str, arguments: dict | None = None) -> dict:
        """Call a tool and return the raw result dict (isError may be true)."""
        if self._session is None:
            self.initialize()
        logger.info("mcp -> tools/call %s", name)
        result = self._run(self._session.call_tool(name, arguments or {}))
        return self._dump(result)

    def call_tool_without_arguments(self, name: str) -> dict:
        """Call with the arguments object omitted entirely (the nil-arguments path)."""
        if self._session is None:
            self.initialize()
        logger.info("mcp -> tools/call %s (no arguments)", name)
        result = self._run(self._session.call_tool(name))
        return self._dump(result)

    def read_resource(self, uri: str) -> dict:
        """Read a signoz:// resource and return the result dict."""
        if self._session is None:
            self.initialize()
        logger.info("mcp -> resources/read %s", uri)
        result = self._run(self._session.read_resource(uri))
        return self._dump(result)

    def close(self) -> None:
        try:
            if self._client_task is not None and not self._client_task.done():
                self._loop.call_soon_threadsafe(self._close_requested.set)
                self._client_task.result(timeout=10)
        except Exception as err:  # noqa: BLE001 — closing must not mask a test failure
            logger.warning("error while closing MCP client: %s", err)
        finally:
            self._loop.call_soon_threadsafe(self._loop.stop)
            self._thread.join(timeout=5)


def text_blocks(result: dict) -> str:
    """Concatenate the text content blocks of a CallToolResult."""
    return "\n".join(block.get("text", "") for block in result.get("content", []) if block.get("type") == "text")


def first_json(result: dict) -> Any:
    """Parse the first text block of a CallToolResult as JSON."""
    import json

    text = text_blocks(result)
    assert text, f"result carried no text content: {result}"
    return json.loads(text)


def assert_tool_ok(result: dict) -> dict:
    assert not result.get("isError", False), f"tool returned error: {text_blocks(result)[:600]}"
    return result


@pytest.fixture(scope="session")
def mcp_client(mcp_server: MCPServer):
    client = MCPClient(mcp_url=mcp_server.mcp_url)
    client.initialize()
    yield client
    client.close()
