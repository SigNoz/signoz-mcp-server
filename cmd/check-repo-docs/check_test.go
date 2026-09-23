package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const donePlan = `# Plan: Example

Status: Done
Issue:
PR: https://github.com/SigNoz/signoz-mcp-server/pull/1

## Context

Why.

## Approach

How.

## Files to Modify

- ` + "`cmd/server/main.go`" + ` — wiring

## Key Decisions

### 2026-09-23 — Keep it small

- Decision: small.
- Rationale: fewer moving parts.

## Reference Links

- [Plans README](README.md)

## Verification

go test ./... passed.

## Outcome

Shipped.
`

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["plans/README.md"] = "# Plans\n"
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assertErrors(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(want) == 0 && len(got) > 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(got, "\n"))
	}
	joined := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing error containing %q; got:\n%s", w, joined)
		}
	}
}

func TestValidPlanAndLegacyFilesPass(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"plans/2026-09-23-example.md":  donePlan,
		"plans/old-feature.plan.md":    "free-form legacy plan",
		"plans/old-feature.context.md": "free-form legacy log",
		"plans/analytics.md":           "free-form",
		"cmd/server/main.go":           "package main",
	})
	assertErrors(t, validateRepository(root, options{changed: []string{"plans/2026-09-23-example.md"}}))
}

func TestPlanStructureErrors(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"plans/Example.md":         "# Example\n\nStatus: Done (mostly)\n\n## Context\n\nx\n",
		"plans/nested/2026-x-y.md": donePlan,
	})
	assertErrors(t, validateRepository(root, options{}),
		"plans/Example.md: plan names must match",
		"'# Plan: <Name>' heading",
		"Status must be one of",
		"missing 'PR:' field",
		"missing '## Verification' section",
		"plans/nested/2026-x-y.md: nested plan files are not allowed",
	)
}

func TestDonePlanNeedsFilledSections(t *testing.T) {
	plan := strings.Replace(donePlan, "PR: https://github.com/SigNoz/signoz-mcp-server/pull/1", "PR:", 1)
	plan = strings.Replace(plan, "go test ./... passed.", "<Commands, results, and gaps.>", 1)
	plan = strings.Replace(plan, "- `cmd/server/main.go` — wiring", "- `path/to/file.go` — change", 1)
	root := writeRepo(t, map[string]string{"plans/2026-09-23-example.md": plan})
	assertErrors(t, validateRepository(root, options{}),
		"completed plans need a PR link",
		"substantive '## Verification' content",
		"substantive '## Files to Modify' content",
	)
}

func TestReadyRulesApplyOnlyToChangedPlans(t *testing.T) {
	inProgress := strings.Replace(donePlan, "Status: Done", "Status: In Progress", 1)
	inProgress = strings.Replace(inProgress, "## Outcome\n\nShipped.\n", "## Outcome\n", 1)
	explained := strings.Replace(donePlan, "Status: Done", "Status: In Progress", 1)
	explained = strings.Replace(explained, "Shipped.", "Phase 1 merged; phase 2 tracked in #2.", 1)
	root := writeRepo(t, map[string]string{
		"plans/2026-09-01-untouched.md":  inProgress,
		"plans/2026-09-02-changed.md":    inProgress,
		"plans/2026-09-03-explained.md":  explained,
		"plans/2026-09-04-abandoned.md":  strings.Replace(inProgress, "Status: In Progress", "Status: Abandoned", 1),
		"cmd/server/main.go":             "package main",
		"plans/2026-09-05-other-done.md": donePlan,
	})

	assertErrors(t, validateRepository(root, options{}),
		"plans/2026-09-04-abandoned.md: abandoned plans need an Outcome")

	errs := validateRepository(root, options{changed: []string{
		"plans/2026-09-02-changed.md", "plans/2026-09-03-explained.md",
	}})
	assertErrors(t, errs, `plans/2026-09-02-changed.md: mark the plan Done before review, or explain in Outcome why it merges as "In Progress"`)
	for _, e := range errs {
		if strings.Contains(e, "untouched") || strings.Contains(e, "explained") {
			t.Errorf("unexpected error: %s", e)
		}
	}
}

func TestNewLegacyPlanFilesAreRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{"plans/new-thing.plan.md": "x"})
	assertErrors(t, validateRepository(root, options{added: []string{"plans/new-thing.plan.md"}}),
		"plans/new-thing.plan.md: new plans use plans/YYYY-MM-DD-<slug>.md")
}

func TestBrokenLinksAndStaleDocPaths(t *testing.T) {
	plan := strings.Replace(donePlan, "[Plans README](README.md)", "[Gone](../docs/gone.md)", 1)
	root := writeRepo(t, map[string]string{
		"plans/2026-09-23-example.md": plan,
		"cmd/server/main.go":          "package main",
		"docs/guide.md": "See `cmd/server/main.go:12`, `cmd/server/removed.go`, `cmd/*.go`, " +
			"`go test ./cmd/...`, and `unknown/root.go`.",
		"CLAUDE.md": "Run `cmd/check-repo-docs` checks.",
	})
	errs := validateRepository(root, options{})
	assertErrors(t, errs,
		"plans/2026-09-23-example.md: broken local link: ../docs/gone.md",
		"docs/guide.md: referenced path does not exist: cmd/server/removed.go",
		"CLAUDE.md: referenced path does not exist: cmd/check-repo-docs",
	)
	if len(errs) != 3 {
		t.Errorf("want exactly 3 errors, got:\n%s", strings.Join(errs, "\n"))
	}
}
