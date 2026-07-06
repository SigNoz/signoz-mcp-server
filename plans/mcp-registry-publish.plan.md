# Plan: MCP Registry Auto-Publish

## Status
In Progress

## Context
`io.github.SigNoz/signoz-mcp-server` is already in the official MCP Registry but frozen at a
placeholder `0.0.4` (published 2025-11-10) because publishing was only ever done manually. The
product is at `v0.5.1`. We want every release to publish the current `server.json` to the registry
automatically, with the OCI package pinned to the immutable image tag the release just pushed.

## Approach

Per reviewer feedback (PR #209): the committed `server.json` is the source of truth, bumped by the
existing prereleaser PR and merged before tagging; the tag-publish ships that file as-is (no rewrite).

1. **`server.json` (committed source of truth):**
   - `$schema` `2025-12-11`, `repository` field, OCI identifier **pinned** to the released version
     (`docker.io/signoz/signoz-mcp-server:vX.Y.Z`), `.version` semver.

2. **`pre-release.yaml` + `post-release.yaml` (the prereleaser & its fallback):**
   - The bump PR now updates BOTH `.version` and `.packages[0].identifier` (pinned image tag), so the
     merged/tagged commit carries the exact `server.json` to publish.

3. **`dockerbuildci.yaml` — `publish-mcp-registry` job:**
   - `needs: build` → runs only after the multi-arch image manifest is pushed (image must exist
     before the registry verifies the OCI ownership label). Codex confirmed the reusable
     `go-build.yaml` pushes the manifest before the job returns success.
   - `if: startsWith(github.ref, 'refs/tags/v') && !contains(github.ref, '-')` → skips `main`
     branch pushes and pre-release/RC tags (RCs should not appear in the public listing).
   - Job permissions: `id-token: write` (OIDC) + `contents: read`.
   - Steps:
     1. **checkout** the tag.
     2. **verify** — assert the committed `.version` and `.packages[0].identifier` match the tag;
        fail loud pointing at the prereleaser if not (enforces "the commit contains the right
        version"). Does NOT rewrite the file.
     3. **verify image** — `docker buildx imagetools inspect` in a 6×10s retry loop; covers
        registry propagation lag and fails loudly if the tag never appears.
     4. **idempotency check** — query `/v0/servers?search=<name>`; if this exact `name + version`
        already exists, skip publish (fail-open: only skip on positive confirmation).
     5. **publish** (gated on the check) — install `mcp-publisher` → `validate` → `login
        github-oidc` → `publish` the committed file.

4. **`CONTRIBUTING.md`:** document the prereleaser → merge → tag → publish flow.

## Files to Modify
- `server.json` — schema bump, `repository`, pinned OCI identifier.
- `.github/workflows/pre-release.yaml` + `post-release.yaml` — bump `.version` AND the pinned identifier.
- `.github/workflows/dockerbuildci.yaml` — `publish-mcp-registry` job (`needs: build`, tag-gated, verify-and-publish, OIDC).
- `CONTRIBUTING.md` — document the release → registry flow.
- `plans/mcp-registry-publish.{context,plan}.md` — this pair.

## Verification
- `mcp-publisher validate server.json` passes locally against the current schema.
- Dry-run the prereleaser bump jq with `version=0.6.0`/`tag=v0.6.0` → `.version=0.6.0`,
  `.identifier=docker.io/signoz/signoz-mcp-server:v0.6.0`.
- Dry-run the publish-job verify step: committed `v0.5.1` + `:v0.5.1` against `GITHUB_REF_NAME=v0.5.1`
  passes; a mismatched version/identifier fails loud.
- On the next stable `vX.Y.Z` tag (after merging the prereleaser PR): `dockerbuildci` builds+pushes
  the image, then `publish-mcp-registry` publishes and
  `curl "https://registry.modelcontextprotocol.io/v0/servers?search=signoz"` shows it as `isLatest`.

## Known behavior / follow-ups
- Re-runs are idempotent: the registry pre-check skips publish when the version already exists.
- Pre-release/RC tags (`vX.Y.Z-rc.N`) are intentionally not published to the public registry.
- `0.5.1` will not be published retroactively by this change — it lands on the next stable release
  tag unless published manually now (see CONTRIBUTING for the pinned local-publish snippet).
