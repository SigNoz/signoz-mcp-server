import re
import time

import pytest


@pytest.fixture
def test_id(request: pytest.FixtureRequest) -> str:
    """A slug unique to the running test, for naming created resources."""
    slug = re.sub(r"[^a-z0-9]+", "-", request.node.name.lower()).removeprefix("test-").strip("-")
    return f"{slug[:40].strip('-')}-{int(time.monotonic() * 1000) % 100000}"
