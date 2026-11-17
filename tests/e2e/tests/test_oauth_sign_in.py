"""Sign in with SigNoz: the OAuth password flow against the real session API.

Unit tests cover PKCE, CSRF, client binding, and token packing against fakes.
These cover what only the pinned SigNoz can prove: it accepts our session
context, password, rotate, delete, and users/me calls, and returns the shapes
we parse.
"""

import base64
import hashlib
import re
import secrets
import time
from html.parser import HTMLParser
from urllib.parse import parse_qs, urlsplit

import requests

from fixtures.mcpclient import MCPClient, assert_tool_ok
from fixtures.mcpserver import MCPServer
from fixtures.signoz import ROOT_EMAIL, ROOT_PASSWORD

REDIRECT_URI = "http://127.0.0.1:9/callback"
# /mcp caches a successful session check for 30s; wait past it, not much longer.
REVOKED_SESSION_DEADLINE = 45.0


class _Forms(HTMLParser):
    """Collect each <form>'s named inputs as a name -> value map."""

    def __init__(self) -> None:
        super().__init__()
        self.forms: list[dict[str, str]] = []
        self._current: dict[str, str] | None = None

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if tag == "form":
            self._current = {}
            self.forms.append(self._current)
        elif tag == "input" and self._current is not None and values.get("name"):
            self._current[values["name"]] = values.get("value") or ""

    def handle_endtag(self, tag: str) -> None:
        if tag == "form":
            self._current = None


def _forms(html: str) -> list[dict[str, str]]:
    parser = _Forms()
    parser.feed(html)
    return parser.forms


def _form_with_step(html: str, step: str) -> dict[str, str]:
    matches = [form for form in _forms(html) if form.get("step") == step]
    assert matches, f"page has no {step!r} form"
    return matches[0]


class _Authorization:
    """One browser walking the authorize page: a cookie jar plus the PKCE pair."""

    def __init__(self, server: MCPServer) -> None:
        self.server = server
        self.browser = requests.Session()
        self.verifier = secrets.token_urlsafe(48)
        self.state = secrets.token_urlsafe(16)
        registered = requests.post(
            f"{server.base_url}/oauth/register",
            json={"client_name": "mcp-e2e-oauth", "redirect_uris": [REDIRECT_URI]},
            timeout=10,
        )
        assert registered.status_code == 201, f"client registration failed: HTTP {registered.status_code}"
        self.client_id = registered.json()["client_id"]

    def open(self) -> requests.Response:
        challenge = base64.urlsafe_b64encode(hashlib.sha256(self.verifier.encode()).digest()).rstrip(b"=").decode()
        page = self.browser.get(
            f"{self.server.base_url}/oauth/authorize",
            params={
                "response_type": "code",
                "client_id": self.client_id,
                "redirect_uri": REDIRECT_URI,
                "state": self.state,
                "code_challenge": challenge,
                "code_challenge_method": "S256",
            },
            timeout=10,
        )
        assert page.status_code == 200, f"authorize page returned HTTP {page.status_code}"
        return page

    def submit(self, form: dict[str, str], **fields: str) -> requests.Response:
        # Every rendered page rotates the CSRF cookie; the form carries its partner token.
        return self.browser.post(
            f"{self.server.base_url}/oauth/authorize",
            data=form | fields,
            allow_redirects=False,
            timeout=30,
        )

    @property
    def instance(self) -> str:
        # Sign-in forms reject literal localhost, so users reach this SigNoz through the allowlisted 127.0.0.1.
        return self.server.backend_url.replace("localhost", "127.0.0.1")

    def discover(self) -> requests.Response:
        start = self.open()
        return self.submit(_form_with_step(start.text, "discover"), signoz_url=self.instance, email=ROOT_EMAIL)

    def token(self, **fields: str) -> requests.Response:
        return requests.post(f"{self.server.base_url}/oauth/token", data=fields, timeout=30)

    def revoke(self, token: str) -> requests.Response:
        return requests.post(
            f"{self.server.base_url}/oauth/revoke", data={"token": token, "client_id": self.client_id}, timeout=30
        )


def _banner(response: requests.Response) -> str:
    match = re.search(r'role="alert">(.*?)<', response.text, re.DOTALL)
    return match.group(1).strip() if match else response.text[:200]


def _assert_session_token(token: dict, instance: str) -> None:
    assert token.get("token_type") == "Bearer", f"token_type is {token.get('token_type')!r}"
    assert isinstance(token.get("expires_in"), int) and token["expires_in"] > 0, (
        f"expires_in must be a positive integer, got {token.get('expires_in')!r}"
    )
    assert token.get("refresh_token"), "token response carried no refresh_token"
    prefix, encoded, signoz_token = token["access_token"].split(".", 2)
    assert prefix == "sz1", "access token is not a session token"
    assert base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4)).decode() == instance
    assert signoz_token, "access token carried no SigNoz session token"


def _assert_mcp_tool_works(server: MCPServer, access_token: str) -> None:
    client = MCPClient(mcp_url=server.mcp_url, headers={"Authorization": f"Bearer {access_token}"})
    try:
        assert_tool_ok(
            client.call_tool("signoz_list_dashboards", {"searchContext": "list dashboards as the signed-in user"})
        )
    finally:
        client.close()


def _mcp_status(server: MCPServer, access_token: str) -> requests.Response:
    return requests.post(
        server.mcp_url,
        json={
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "mcp-e2e-oauth", "version": "0"},
            },
        },
        headers={"Authorization": f"Bearer {access_token}", "Accept": "application/json, text/event-stream"},
        timeout=10,
    )


def test_password_sign_in_issues_a_session_that_refreshes_and_revokes(mcp_server_with_oauth: MCPServer) -> None:
    server = mcp_server_with_oauth
    flow = _Authorization(server)

    sign_in = flow.discover()
    assert sign_in.status_code == 200, f"discover returned HTTP {sign_in.status_code}: {_banner(sign_in)}"
    password_form = _form_with_step(sign_in.text, "password")
    assert password_form.get("org_id"), "the real org offered no password sign-in with an org id"

    redirect = flow.submit(password_form, password=ROOT_PASSWORD)
    assert redirect.status_code == 302, f"password sign-in returned HTTP {redirect.status_code}: {_banner(redirect)}"
    location = urlsplit(redirect.headers["Location"])
    assert f"{location.scheme}://{location.netloc}{location.path}" == REDIRECT_URI
    query = parse_qs(location.query)
    assert query.get("state") == [flow.state], "redirect did not echo state"
    assert query.get("code"), f"redirect carried no code (error={query.get('error')})"

    exchanged = flow.token(
        grant_type="authorization_code",
        client_id=flow.client_id,
        code=query["code"][0],
        redirect_uri=REDIRECT_URI,
        code_verifier=flow.verifier,
    )
    assert exchanged.status_code == 200, f"code exchange returned HTTP {exchanged.status_code}: {exchanged.text}"
    token = exchanged.json()
    latest_refresh = token["refresh_token"]
    try:
        _assert_session_token(token, flow.instance)
        _assert_mcp_tool_works(server, token["access_token"])

        refreshed = flow.token(grant_type="refresh_token", client_id=flow.client_id, refresh_token=latest_refresh)
        assert refreshed.status_code == 200, f"refresh returned HTTP {refreshed.status_code}: {refreshed.text}"
        rotated = refreshed.json()
        latest_refresh = rotated["refresh_token"]
        _assert_session_token(rotated, flow.instance)
        assert rotated["access_token"] != token["access_token"], "SigNoz rotation returned the same access token"
        _assert_mcp_tool_works(server, rotated["access_token"])

        revoked = flow.revoke(latest_refresh)
        assert revoked.status_code == 200, f"revoke returned HTTP {revoked.status_code}: {revoked.text}"

        after = flow.token(grant_type="refresh_token", client_id=flow.client_id, refresh_token=latest_refresh)
        assert after.status_code == 400, f"refresh after revoke returned HTTP {after.status_code}: {after.text}"
        assert after.json().get("error") == "invalid_grant"

        deadline = time.monotonic() + REVOKED_SESSION_DEADLINE
        while (response := _mcp_status(server, rotated["access_token"])).status_code != 401:
            assert time.monotonic() < deadline, f"/mcp still accepts the revoked session (HTTP {response.status_code})"
            time.sleep(2)
        assert "invalid_token" in response.headers.get("WWW-Authenticate", ""), (
            "a revoked session should get an invalid_token challenge"
        )
    finally:
        # SigNoz keeps a few sessions per user and evicts the oldest; never leave one behind.
        flow.revoke(latest_refresh)


def test_wrong_password_rerenders_sign_in_with_401(mcp_server_with_oauth: MCPServer) -> None:
    flow = _Authorization(mcp_server_with_oauth)
    sign_in = flow.discover()
    assert sign_in.status_code == 200, f"discover returned HTTP {sign_in.status_code}: {_banner(sign_in)}"

    rejected = flow.submit(_form_with_step(sign_in.text, "password"), password="mcp-e2e-not-the-password")

    assert rejected.status_code == 401, f"wrong password returned HTTP {rejected.status_code}: {_banner(rejected)}"
    assert "Location" not in rejected.headers, "a wrong password must not redirect with a code"
    assert _form_with_step(rejected.text, "password").get("org_id"), "the sign-in page should offer the password again"
