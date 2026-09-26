from collections.abc import Callable
from typing import TypeVar

import pytest

from fixtures.logger import setup_logger

logger = setup_logger(__name__)

T = TypeVar("T")


def _release(resource: T) -> None:
    if hasattr(resource, "__release__"):
        resource.__release__()


def reuse(request: pytest.FixtureRequest) -> bool:
    return request.config.getoption("--reuse")


def teardown(request: pytest.FixtureRequest) -> bool:
    return request.config.getoption("--teardown")


def wrap(
    request: pytest.FixtureRequest,
    pytestconfig: pytest.Config,
    key: str,
    empty: Callable[[], T],
    create: Callable[[], T],
    delete: Callable[[T], None],
    restore: Callable[[dict], T],
) -> T:
    """Wrap a resource's create/cleanup with the --reuse and --teardown options.

    - --reuse: reuse the cached resource if present (skip create), and leave it in
      place at the end (skip delete) while caching it for the next run.
    - --teardown: skip create and instead restore the cached resource and delete it.
    - neither: create on setup, delete on teardown (the normal path).
    """
    resource = empty()

    if reuse(request):
        existing_resource = pytestconfig.cache.get(key, None)
        if existing_resource:
            assert isinstance(existing_resource, dict)
            logger.info("Reusing existing %s", key)
            resource = restore(existing_resource)
            # Rewrite legacy cache entries through the resource's current
            # serializer. This removes credentials previously cached by the
            # SigNoz fixture as soon as an existing environment is restored.
            pytestconfig.cache.set(
                key,
                resource.__cache__() if hasattr(resource, "__cache__") else resource,
            )
            request.addfinalizer(lambda: _release(resource))
            return resource

    if not teardown(request):
        resource = create()

    def finalizer():
        nonlocal resource
        if reuse(request):
            _release(resource)
            logger.info(
                "Skipping removal of %s",
                resource.__log__() if hasattr(resource, "__log__") else resource,
            )
            return

        if teardown(request):
            existing_resource = pytestconfig.cache.get(key, None)
            if not existing_resource:
                logger.info(
                    "Skipping removal of %s, no existing %s found. Maybe you ran teardown without reuse?",
                    key,
                    key,
                )
                return

            resource = restore(existing_resource)

        logger.info(
            "Removing %s",
            resource.__log__() if hasattr(resource, "__log__") else resource,
        )
        delete(resource)

        pytestconfig.cache.set(key, None)

    request.addfinalizer(finalizer)

    if reuse(request):
        pytestconfig.cache.set(key, resource.__cache__() if hasattr(resource, "__cache__") else resource)

    return resource
