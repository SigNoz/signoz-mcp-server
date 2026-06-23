# Feature: MCP Registry Auto-Publish — Context & Discussion

## Original Prompt
> How can we list SigNoz mcp server here: https://github.com/modelcontextprotocol/registry?
>
> Let's automate it, get codex xhigh 5.5 review as well

## Reference Links
- [MCP Registry repo](https://github.com/modelcontextprotocol/registry)
- [Quickstart: publish a server](https://github.com/modelcontextprotocol/registry/blob/main/docs/modelcontextprotocol-io/quickstart.mdx)
- [Package Types — OCI verification](https://github.com/modelcontextprotocol/registry/blob/main/docs/modelcontextprotocol-io/package-types.mdx)
- [GitHub Actions publishing guide](https://github.com/modelcontextprotocol/registry/blob/main/docs/modelcontextprotocol-io/github-actions.mdx)
- [Publisher CLI reference](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/cli/commands.md)
- [Official registry requirements](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/official-registry-requirements.md)

## Key Decisions & Discussion Log

### 2026-06-23 — Discovery: already listed but stale
- Querying `https://registry.modelcontextprotocol.io/v0/servers?search=signoz` returns
  `io.github.SigNoz/signoz-mcp-server` **already published**, versions `0.0.1` and `0.0.4`
  (latest = `0.0.4`, published 2025-11-10), both `status: active`.
- These were one-off manual publishes with placeholder versions. The product is at `v0.5.1`.
- No `mcp-publisher` step exists anywhere in `.github/` — publishing was never automated, so the
  registry entry is frozen at `0.0.4`.
- Conclusion: the task is not "list it" (done) but "publish the current version automatically and
  stop it going stale."

### 2026-06-23 — Requirements already satisfied
- OCI ownership label `io.modelcontextprotocol.server.name="io.github.SigNoz/signoz-mcp-server"`
  is present in both `Dockerfile:26` and `Dockerfile.multi-arch:5` (CI builds with the multi-arch one).
- Image is published to Docker Hub `signoz/signoz-mcp-server`. **The release image tag carries the
  leading `v`** — confirmed tags include `v0.5.1` and `latest` (NOT `0.5.1`). The OCI identifier
  must match the tag exactly or ownership verification fails.
- `io.github.SigNoz/*` namespace authenticates via GitHub; from CI, GitHub OIDC
  (`mcp-publisher login github-oidc`) maps the repo owner (`SigNoz`) to the namespace — no secret needed.

### 2026-06-23 — Ordering is the crux
- The registry verifies the package by pulling the OCI image *at the tag in `server.json`*, so the
  Docker image must exist before publish.
- `dockerbuildci.yaml` (`push: tags: v*`) and any `release: published`-triggered job race; the
  pre-release.yaml comment confirms artifact workflows have merely *started* by the time the release
  event fires.
- Decision: add the publish as a job **inside `dockerbuildci.yaml` with `needs: build`**, gated to
  `v*` tags. `needs` guarantees the image manifest is pushed before publish runs. This reuses the
  workflow that already holds `id-token: write` and triggers on the tag.
- Rejected `workflow_run` trigger: tag handling runs against the default branch and is awkward to
  scope to the released tag.

### 2026-06-23 — Source of truth for version + identifier
- Decision: **the git tag is authoritative at publish time.** The publish job derives
  `VERSION=${GITHUB_REF_NAME#v}` and rewrites `server.json` `.version` + `.packages[0].identifier`
  to `docker.io/signoz/signoz-mcp-server:${GITHUB_REF_NAME}` before publishing. This mirrors the
  existing "extract version from tag" pattern in post-release.yaml and is race/fallback-proof.
- The committed `server.json` keeps a fully-qualified `:latest` identifier so it never drifts and
  stays valid for `mcp-publisher validate` / manual publish; CI always pins to the immutable tag.
  This avoids editing the pre/post-release bump steps.
- Bumped committed `$schema` from `2025-10-17` to `2025-12-11` (current) and added the `repository`
  field (was empty `{}` in the live entry).

### 2026-06-23 — Codex (gpt-5.5, xhigh) review → hardening
Codex verdict: fix-then-ship. It confirmed `needs: build` is the correct ordering (the reusable
`go-build.yaml` pushes the manifest before the job returns success) and found no OIDC/permission,
tag-prefix, or schema bug. Acted on all five findings:
- **Image-readiness preflight (med):** added a `docker buildx imagetools inspect` retry loop
  (6×10s) before publish — asserts the image tag is pullable, covering manifest/registry
  propagation lag; fails loudly if it never appears.
- **Rerun idempotency (med):** added a registry pre-check that skips publish when the exact
  `name + version` already exists. Fail-open: only skips on positive confirmation, so a query
  hiccup falls through to publish (which fails loudly on a true duplicate).
- **Manual path published mutable `:latest` (med):** CONTRIBUTING now tells maintainers to re-run
  the workflow, or includes the jq pin step for local publish.
- **Broad tag filter (low):** added a stable-semver regex guard in the pin step (fail-fast on a
  malformed `v*` tag).
- **API path (low):** verified `/v0/servers` and `/v0.1/servers` both return identical data live;
  standardized on `/v0/servers` (empirically validated) for the idempotency check and plan doc.
- **Added default (not from Codex):** gate now excludes pre-release tags
  (`!contains(github.ref, '-')`) so RCs do not clutter the public listing. Flag for owner review.

### 2026-06-23 — Reviewer feedback (PR #209, @therealpandey): bump in the prereleaser PR, don't rewrite at publish
Reviewer (CHANGES_REQUESTED) on `CONTRIBUTING.md`: "Can we create a prereleaser job which raises a
PR to bump `server.json`? Once that PR is merged, a tag will then publish the updated `server.json`.
Standard process for ensuring the commit contains the right version in server.json. See SigNoz for
how we do this."
- Checked `~/signoz/signoz`: pattern is `prereleaser.yaml` (manual dispatch → primus releaser raises
  a bump PR) + `releaser.yaml` (runs on `release: published`). No server.json/mcp-publisher there.
- This repo **already implements** that pattern via `pre-release.yaml` (raises a PR bumping
  `manifest.json` + `server.json.version` + CHANGELOG, merged before tagging) and `post-release.yaml`
  (fallback reconcile). The reviewer's invariant ("commit contains the right version") was already
  met for `.version`.
- **Reworked the approach to match the reviewer's model** (committed file is source of truth; the
  tag-publish ships it, no rewrite):
  - `pre-release.yaml` + `post-release.yaml` now bump BOTH `.version` and `.packages[0].identifier`
    (pinned `docker.io/signoz/signoz-mcp-server:vX.Y.Z`) in the bump PR.
  - Committed `server.json` identifier is now pinned (`:v0.5.1`) instead of `:latest`.
  - `dockerbuildci.yaml` publish job no longer rewrites server.json from the tag. It **verifies**
    the committed `version` and `identifier` match the tag (fail-loud, pointing at the prereleaser),
    then publishes the file as-is. Image-pullable preflight + idempotency skip retained.
- Net: the earlier "tag is authoritative / correct even if the pre-release PR wasn't merged" decision
  is reversed — the prereleaser-merged commit is the contract now, enforced by the verify step.

## Open Questions
- [x] Is it already listed? — Yes, stale at `0.0.4`. (resolved 2026-06-23)
- [x] What image tag does the registry need to verify? — `docker.io/signoz/signoz-mcp-server:v<semver>` (resolved 2026-06-23)
- [x] Where to put the publish step so the image exists first? — `needs: build` job in `dockerbuildci.yaml` (resolved 2026-06-23)
- [x] Re-run of an already-published version? — registry pre-check skips it (idempotent, fail-open). (resolved 2026-06-23)
- [ ] Bring `0.5.1` live now via a one-off manual publish, or let the automation catch it on the next stable release tag?
- [ ] Confirm excluding pre-release/RC tags from the public registry is the desired policy.
