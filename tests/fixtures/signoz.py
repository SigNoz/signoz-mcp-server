import re
import time
from dataclasses import dataclass, field
from uuid import uuid4

import pytest
import requests

from fixtures import foundry, reuse
from fixtures.logger import setup_logger

logger = setup_logger(__name__)

# Must match the SIGNOZ_USER_ROOT_* values in casting.yaml.
ROOT_EMAIL = "admin@e2e.test"
ROOT_PASSWORD = "password123Z$"

# Service account minted for the MCP server under test. signoz-admin so it can
# manage every resource the tools exercise.
SERVICE_ACCOUNT_NAME = "signoz-mcp-e2e"
SERVICE_ACCOUNT_ROLE = "signoz-admin"


@dataclass(frozen=True)
class SigNoz:
    endpoint: str
    access_token: str = field(repr=False)
    session_key_id: str = ""
    service_account_id: str = ""
    bearer_token: str = field(default="", repr=False)

    def __cache__(self) -> dict:
        # Authentication is restored in memory. Never persist API keys in the
        # pytest reuse cache.
        return {"endpoint": self.endpoint}

    def __log__(self) -> str:
        return f"signoz(endpoint={self.endpoint})"

    def __release__(self) -> None:
        """Revoke authentication minted only for this reused pytest session."""
        __tracebackhide__ = True
        if not (self.session_key_id and self.service_account_id and self.bearer_token):
            return
        response = requests.delete(
            f"{self.endpoint}/api/v1/service_accounts/{self.service_account_id}/keys/{self.session_key_id}",
            headers={"Authorization": f"Bearer {self.bearer_token}"},
            timeout=10,
        )
        assert response.status_code in (204, 404), "failed to revoke the reusable-session API key"
        logger.info("revoked reusable-session service-account key")

    def api(self, method: str, path: str, **kwargs) -> requests.Response:
        """Call the SigNoz HTTP API with the service-account key."""
        return requests.request(
            method,
            f"{self.endpoint}{path}",
            headers={"SIGNOZ-API-KEY": self.access_token},
            timeout=30,
            **kwargs,
        )


def _login_as_root(endpoint: str, email: str, password: str, *, ready_timeout: float = 240.0) -> str:
    """Return a bearer access token for the root user.

    Retries until SigNoz is up and the root user has been reconciled (it is
    created asynchronously shortly after the container starts).
    """
    __tracebackhide__ = True
    deadline = time.time() + ready_timeout
    last = None

    while time.time() < deadline:
        try:
            ctx = requests.get(
                f"{endpoint}/api/v2/sessions/context",
                params={"email": email, "ref": endpoint},
                timeout=10,
            )
            if ctx.status_code == 200 and ctx.json().get("data", {}).get("orgs"):
                org_id = ctx.json()["data"]["orgs"][0]["id"]

                login = requests.post(
                    f"{endpoint}/api/v2/sessions/email_password",
                    json={"email": email, "password": password, "orgId": org_id},
                    timeout=10,
                )
                if login.status_code == 200:
                    logger.info("logged in as root user %s", email)
                    return login.json()["data"]["accessToken"]

                last = (login.status_code, login.text[:200])
            else:
                last = (ctx.status_code, ctx.text[:200])
        except requests.RequestException as err:
            last = err

        time.sleep(3)

    raise TimeoutError(f"could not log in as {email} within {ready_timeout}s (last={last})")


def assert_backend_version(endpoint: str) -> None:
    """Fail unless the cast backend identifies itself as SigNoz v0.143.0+.

    Image digests pin the artifact, while this check catches a Foundry casting
    bug that renders a different service onto the published backend port.
    """
    deadline = time.time() + 240
    response = None
    version = ""
    while time.time() < deadline:
        try:
            response = requests.get(f"{endpoint}/api/v1/version", timeout=5)
            payload = response.json() if response.status_code == 200 else {}
            data = payload.get("data", {}) if isinstance(payload.get("data"), dict) else {}
            version = payload.get("version", "") or data.get("version", "")
        except (requests.RequestException, ValueError):
            version = ""
        if version:
            break
        time.sleep(3)
    assert response is not None, "backend version endpoint never responded"
    assert response.status_code == 200, f"backend version endpoint returned HTTP {response.status_code}"
    match = re.fullmatch(r"v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", version)
    assert match, f"backend returned a non-semantic version ({version!r})"
    major, minor, patch = (int(part) for part in match.groups())
    assert (major, minor, patch) >= (0, 143, 0), f"backend version {version} is older than v0.143.0"


def apply_license(endpoint: str, bearer_token: str, license_key: str) -> None:
    """Apply a license key to a freshly started SigNoz via the admin API.

    No-op when no key is given, so community-only runs (and forks without the
    secret) still work.
    """
    if not license_key:
        logger.info("no license key provided; skipping license application")
        return

    resp = requests.post(
        f"{endpoint}/api/v3/licenses",
        json={"key": license_key},
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=30,
    )
    assert resp.status_code == 202, resp.text

    logger.info("applied SigNoz license")


def mint_service_account_key(
    endpoint: str,
    bearer_token: str,
    *,
    name: str = SERVICE_ACCOUNT_NAME,
    role: str = SERVICE_ACCOUNT_ROLE,
) -> tuple[str, str, str]:
    """Create a service account, assign it a role, and mint a fresh API key.

    Returns (access_token, service_account_id, key_id).
    """
    sa = requests.post(
        f"{endpoint}/api/v1/service_accounts",
        json={"name": name},
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=10,
    )
    assert sa.status_code == 201, sa.text
    sa_id = sa.json()["data"]["id"]

    roles = requests.get(
        f"{endpoint}/api/v1/roles",
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=10,
    )
    assert roles.status_code == 200, roles.text
    role_id = next(r["id"] for r in roles.json()["data"] if r["name"] == role)

    assign = requests.post(
        f"{endpoint}/api/v1/service_account_roles",
        json={"serviceAccountId": sa_id, "roleId": role_id},
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=10,
    )
    assert assign.status_code == 201, assign.text

    access_token, key_id = mint_key_for_service_account(endpoint, bearer_token, sa_id, name=name)
    return access_token, sa_id, key_id


def mint_key_for_service_account(
    endpoint: str, bearer_token: str, service_account_id: str, *, name: str
) -> tuple[str, str]:
    """Mint an API key for an existing service account."""
    __tracebackhide__ = True
    key = requests.post(
        f"{endpoint}/api/v1/service_accounts/{service_account_id}/keys",
        json={"name": name, "expiresAt": 0},
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=10,
    )
    assert key.status_code == 201, "failed to mint a service-account API key"

    logger.info("minted service-account key for %s", name)
    data = key.json()["data"]
    return data["key"], data["id"]


def restore_service_account_key(endpoint: str) -> SigNoz:
    """Authenticate afresh and mint an in-memory key for the reused account."""
    __tracebackhide__ = True
    bearer_token = _login_as_root(endpoint, ROOT_EMAIL, ROOT_PASSWORD)
    response = requests.get(
        f"{endpoint}/api/v1/service_accounts",
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=10,
    )
    assert response.status_code == 200, "failed to list service accounts while restoring the reusable environment"
    accounts = response.json().get("data", [])
    service_account_id = next(
        (account.get("id") for account in accounts if account.get("name") == SERVICE_ACCOUNT_NAME),
        "",
    )
    assert service_account_id, f"reusable service account {SERVICE_ACCOUNT_NAME!r} was not found"
    key_name = f"{SERVICE_ACCOUNT_NAME}-session-{uuid4().hex[:12]}"
    access_token, session_key_id = mint_key_for_service_account(
        endpoint,
        bearer_token,
        service_account_id,
        name=key_name,
    )
    return SigNoz(
        endpoint=endpoint,
        access_token=access_token,
        session_key_id=session_key_id,
        service_account_id=service_account_id,
        bearer_token=bearer_token,
    )


@pytest.fixture(scope="session")
def signoz(request: pytest.FixtureRequest, pytestconfig: pytest.Config) -> SigNoz:
    """A SigNoz instance with a service-account API key for the MCP server."""
    foundryctl = request.config.getoption("--foundry-binary-path")

    def empty() -> SigNoz:
        return SigNoz(endpoint="", access_token="")

    def create() -> SigNoz:
        try:
            endpoint = foundry.cast(foundryctl)
            assert_backend_version(endpoint)
            bearer_token = _login_as_root(endpoint, ROOT_EMAIL, ROOT_PASSWORD)

            apply_license(endpoint, bearer_token, request.config.getoption("--license-key"))

            access_token, service_account_id, key_id = mint_service_account_key(endpoint, bearer_token)
            # Keep revocation metadata in memory so a reused stack does not
            # retain this non-expiring key; __cache__ still persists only the endpoint.
            return SigNoz(
                endpoint=endpoint,
                access_token=access_token,
                session_key_id=key_id,
                service_account_id=service_account_id,
                bearer_token=bearer_token,
            )
        except Exception:
            # reuse.wrap registers its finalizer only after create() returns, so
            # a failed bring-up must tear the stack down itself.
            foundry.teardown()
            raise

    def delete(_: SigNoz) -> None:
        foundry.teardown()

    def restore(cache: dict) -> SigNoz:
        endpoint = cache["endpoint"]
        assert_backend_version(endpoint)
        # Accept the old shape once so the next --reuse run migrates it to the
        # endpoint-only cache without making another API key.
        if access_token := cache.get("access_token"):
            return SigNoz(endpoint=endpoint, access_token=access_token)
        return restore_service_account_key(endpoint)

    return reuse.wrap(request, pytestconfig, "signoz", empty, create, delete, restore)
