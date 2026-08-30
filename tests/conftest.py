import pytest

pytest_plugins = [
    "fixtures.signoz",
    "fixtures.mcpserver",
    "fixtures.mcpclient",
    "fixtures.telemetry",
    "fixtures.naming",
]


def pytest_addoption(parser: pytest.Parser):
    parser.addoption(
        "--reuse",
        action="store_true",
        default=False,
        help="Reuse a cached SigNoz environment across runs instead of creating a new one. Run e.g. `pytest --reuse e2e/bootstrap/setup.py::test_setup` to stand one up.",
    )
    parser.addoption(
        "--teardown",
        action="store_true",
        default=False,
        help="Tear down the cached SigNoz environment. Run `pytest --teardown e2e/bootstrap/setup.py::test_teardown`.",
    )
    parser.addoption(
        "--foundry-binary-path",
        action="store",
        default="foundryctl",
        help="Path to the foundryctl binary used to cast the SigNoz environment.",
    )
    parser.addoption(
        "--go-binary-path",
        action="store",
        default="go",
        help="Path to the go binary used to build the MCP server under test.",
    )
    parser.addoption(
        "--license-key",
        action="store",
        default="",
        help="SigNoz license key applied after startup; empty skips license application.",
    )
