import time
from dataclasses import dataclass

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
    access_token: str

    def __cache__(self) -> dict:
        return {"endpoint": self.endpoint, "access_token": self.access_token}

    def __log__(self) -> str:
        return f"signoz(endpoint={self.endpoint})"

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
) -> str:
    """Create a service account, assign it a role, and return a fresh API key."""
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

    key = requests.post(
        f"{endpoint}/api/v1/service_accounts/{sa_id}/keys",
        json={"name": name, "expiresAt": 0},
        headers={"Authorization": f"Bearer {bearer_token}"},
        timeout=10,
    )
    assert key.status_code == 201, key.text

    logger.info("minted service-account key for %s (role %s)", name, role)
    return key.json()["data"]["key"]


@pytest.fixture(scope="session")
def signoz(request: pytest.FixtureRequest, pytestconfig: pytest.Config) -> SigNoz:
    """A SigNoz instance with a service-account API key for the MCP server."""
    foundryctl = request.config.getoption("--foundry-binary-path")

    def empty() -> SigNoz:
        return SigNoz(endpoint="", access_token="")

    def create() -> SigNoz:
        try:
            endpoint = foundry.cast(foundryctl)
            bearer_token = _login_as_root(endpoint, ROOT_EMAIL, ROOT_PASSWORD)

            apply_license(endpoint, bearer_token, request.config.getoption("--license-key"))

            access_token = mint_service_account_key(endpoint, bearer_token)
            return SigNoz(endpoint=endpoint, access_token=access_token)
        except Exception:
            # reuse.wrap registers its finalizer only after create() returns, so
            # a failed bring-up must tear the stack down itself.
            foundry.teardown()
            raise

    def delete(_: SigNoz) -> None:
        foundry.teardown()

    def restore(cache: dict) -> SigNoz:
        return SigNoz(endpoint=cache["endpoint"], access_token=cache["access_token"])

    return reuse.wrap(request, pytestconfig, "signoz", empty, create, delete, restore)
