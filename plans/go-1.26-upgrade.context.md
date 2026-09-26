# Feature: Go 1.26 Toolchain Upgrade — Context & Discussion

## Original Prompt
> Now let's upgrade go version

## Reference Links
- [Go release policy](https://go.dev/doc/devel/release)
- [Go 1.26 release notes](https://go.dev/doc/go1.26)
- [Go toolchain configuration](https://go.dev/doc/toolchain)

## Key Decisions & Discussion Log

### 2026-08-14 — Target and scope
- Upgrade this repository from Go 1.25 to the supported Go 1.26 release line.
- Set the module minimum to `go 1.26.0`. The `go` directive is a minimum
  requirement, so do not add a `toolchain` directive that would force one patch
  release on contributors.
- Update every active build surface together: `go.mod`, the builder Dockerfile,
  reusable CI inputs, secret-free protocol/guardrail checks, the manual docs
  refresh workflow, and the README badge. Do not rewrite historical planning
  baselines that mention Go 1.25.
- Current direct and transitive dependencies declare at most Go 1.25, so this
  is a compiler/toolchain upgrade only; do not bundle dependency upgrades.
- Go 1.26 maintains the Go 1 compatibility promise. The relevant release-note
  changes do not affect this server's source or build targets; verification will
  compile, test, race-test the affected transport-heavy packages, and build the
  container.

## Open Questions
- [x] Which target line? Go 1.26, the currently supported major line available
  in the local toolchain and official release documentation.
- [x] Pin a patch release with `toolchain`? No; require Go 1.26.0 or later and
  let CI/Docker resolve the current 1.26 patch.
- [x] Upgrade dependencies at the same time? No; all known minimums are at or
  below Go 1.25 and unrelated dependency movement would obscure this migration.

### 2026-08-15 — Completion
- The Go 1.26 upgrade and its planned verification are complete. Mark the plan
  `Done` so the planning record reflects the shipped implementation.
