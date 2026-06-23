# Plan: MCP Registry Auto-Publish

## Status
In Progress

## Context
`io.github.SigNoz/signoz-mcp-server` is already in the official MCP Registry but frozen at a
placeholder `0.0.4` (published 2025-11-10) because publishing was only ever done manually. The
product is at `v0.5.1`. We want every release to publish the current `server.json` to the registry
automatically, with the OCI package pinned to the immutable image tag the release just pushed.

## Approach

Add a tag-gated publish job to the existing release path and make `server.json` valid/current.

1. **`server.json` (committed, source for `validate`):**
   - Bump `$schema` to the current `2025-12-11`.
   - Add `repository` (`https://github.com/SigNoz/signoz-mcp-server`, source `github`).
   - Use a fully-qualified `:latest` identifier (`docker.io/signoz/signoz-mcp-server:latest`) so the
     committed file never drifts and stays valid for `mcp-publisher validate` / manual publish.

2. **`dockerbuildci.yaml` — new `publish-mcp-registry` job:**
   - `needs: build` → runs only after the multi-arch image manifest is pushed (image must exist
     before the registry can verify the OCI ownership label). Codex confirmed the reusable
     `go-build.yaml` pushes the manifest before the job returns success.
   - `if: startsWith(github.ref, 'refs/tags/v') && !contains(github.ref, '-')` → skips `main`
     branch pushes and pre-release/RC tags (RCs should not appear in the public listing).
   - Job permissions: `id-token: write` (OIDC to the registry) + `contents: read`.
   - Steps:
     1. **checkout** the tag.
     2. **pin** — validate `TAG` is stable `vX.Y.Z` (fail-fast otherwise), then rewrite
        `server.json` `.version` (`${TAG#v}`) and `.packages[0].identifier`
        (`docker.io/signoz/signoz-mcp-server:${TAG}`). The git tag is authoritative, so this is
        correct even if the pre-release bump PR was not merged, and pins to the immutable tag.
     3. **verify image** — `docker buildx imagetools inspect` in a 6×10s retry loop; covers
        registry propagation lag and fails loudly if the tag never appears.
     4. **idempotency check** — query `/v0/servers?search=<name>`; if this exact `name + version`
        already exists, skip publish (fail-open: only skip on positive confirmation).
     5. **publish** (gated on the check) — install `mcp-publisher` → `validate` → `login
        github-oidc` → `publish`.

3. **`CONTRIBUTING.md`:** short note that releases auto-publish to the MCP Registry.

## Files to Modify
- `server.json` — schema bump, `repository`, fully-qualified `:latest` identifier.
- `.github/workflows/dockerbuildci.yaml` — add `publish-mcp-registry` job (`needs: build`, tag-gated, OIDC).
- `CONTRIBUTING.md` — document the release → registry automation.
- `plans/mcp-registry-publish.{context,plan}.md` — this pair.

## Verification
- `mcp-publisher validate server.json` passes locally against the current schema.
- Dry check the jq rewrite locally with `GITHUB_REF_NAME=v0.5.1` and confirm the resulting
  `identifier` is `docker.io/signoz/signoz-mcp-server:v0.5.1` (a tag that exists on Docker Hub) and
  `version` is `0.5.1`.
- On the next `v*` tag: `dockerbuildci` builds+pushes the image, then `publish-mcp-registry` runs and
  `curl "https://registry.modelcontextprotocol.io/v0/servers?search=signoz"` shows the new version as
  `isLatest`.
- One-off: to bring `0.5.1` live before the next release, run `mcp-publisher login github` (as a
  SigNoz org member) + `mcp-publisher publish` locally, or re-run after merging.

## Known behavior / follow-ups
- Re-runs are idempotent: the registry pre-check skips publish when the version already exists.
- Pre-release/RC tags (`vX.Y.Z-rc.N`) are intentionally not published to the public registry.
- `0.5.1` will not be published retroactively by this change — it lands on the next stable release
  tag unless published manually now (see CONTRIBUTING for the pinned local-publish snippet).
