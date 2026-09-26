package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	planStatuses           = map[string]bool{"Planning": true, "In Progress": true, "Done": true, "Abandoned": true}
	planSections           = []string{"Context", "Approach", "Files to Modify", "Key Decisions", "Reference Links", "Verification", "Outcome"}
	doneSubstantiveSection = []string{"Context", "Approach", "Files to Modify", "Key Decisions", "Verification", "Outcome"}

	// Files in plans/ that predate the dated-plan convention.
	nonPlanFiles  = map[string]bool{"README.md": true, "TEMPLATES.md": true, "analytics.md": true}
	legacySuffix  = []string{".context.md", ".plan.md"}
	planNameRE    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-[a-z0-9][a-z0-9-]*\.md$`)
	placeholderRE = regexp.MustCompile(`(?is)^(<.*>|TBD)$`)
	mdLinkRE      = regexp.MustCompile(`\[[^\]]+\]\(\s*(?:<([^>]+)>|([^\s)]+))`)
	codeSpanRE    = regexp.MustCompile("`([^`\n]+)`")
	lineSuffixRE  = regexp.MustCompile(`:\d+(?:-\d+)?$`)

	// Template bodies that count as unfilled.
	templateSectionBodies = map[string]string{
		"Files to Modify": "- `path/to/file.go` — change",
		"Key Decisions":   "### YYYY-MM-DD — <decision>\n\n- Decision:\n- Rationale:\n- Alternatives rejected, if relevant:",
	}
)

// A span containing any of these is a command, glob, or placeholder rather than a repo path.
const nonPathChars = " \t\"'`|&;:<>(){}[]$*?!,=+@"

// guideFiles are checked for stale backtick path references.
var guideFiles = []string{"README.md", "CONTRIBUTING.md", "CLAUDE.md"}

type options struct {
	// changed lists repo-relative plan paths added or modified by the pull request.
	// When non-nil, the ready-for-review rules apply to those plans.
	changed []string
	// added lists repo-relative paths the pull request adds.
	added []string
}

func validateRepository(root string, opts options) []string {
	var errs []string
	plansDir := filepath.Join(root, "plans")
	if _, err := os.Stat(filepath.Join(plansDir, "README.md")); err != nil {
		errs = append(errs, "plans/README.md: missing")
	}

	ready := map[string]bool{}
	for _, p := range opts.changed {
		ready[filepath.ToSlash(p)] = true
	}

	_ = filepath.WalkDir(plansDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if filepath.Dir(path) != plansDir {
			errs = append(errs, rel+": nested plan files are not allowed")
			return nil
		}
		name := d.Name()
		if nonPlanFiles[name] || isLegacyPlan(name) {
			return nil
		}
		errs = append(errs, validatePlan(root, rel, ready[rel])...)
		return nil
	})

	for _, p := range opts.added {
		p = filepath.ToSlash(p)
		if strings.HasPrefix(p, "plans/") && isLegacyPlan(filepath.Base(p)) {
			errs = append(errs, p+": new plans use plans/YYYY-MM-DD-<slug>.md; continue legacy pairs only by editing them")
		}
	}

	docs, _ := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	sort.Strings(docs)
	for _, g := range guideFiles {
		docs = append(docs, filepath.Join(root, g))
	}
	for _, path := range docs {
		errs = append(errs, validateCodeSpanPaths(root, path)...)
	}
	return errs
}

func isLegacyPlan(name string) bool {
	for _, s := range legacySuffix {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func validatePlan(root, rel string, ready bool) []string {
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", rel, err)}
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var errs []string
	fail := func(format string, a ...any) { errs = append(errs, rel+": "+fmt.Sprintf(format, a...)) }

	if !planNameRE.MatchString(filepath.Base(rel)) {
		fail("plan names must match YYYY-MM-DD-<slug>.md")
	}
	if first := firstContentLine(text); !strings.HasPrefix(first, "# Plan: ") || strings.TrimSpace(strings.TrimPrefix(first, "# Plan: ")) == "" {
		fail("document must start with a '# Plan: <Name>' heading")
	}

	status, hasStatus := field(text, "Status")
	if !hasStatus || !planStatuses[status] {
		fail("Status must be one of Planning, In Progress, Done, Abandoned")
	}
	for _, f := range []string{"Issue", "PR"} {
		if _, ok := field(text, f); !ok {
			fail("missing '%s:' field", f)
		}
	}
	sections := parseSections(text)
	for _, s := range planSections {
		if _, ok := sections[s]; !ok {
			fail("missing '## %s' section", s)
		}
	}

	switch status {
	case "Done":
		if pr, _ := field(text, "PR"); !substantive(pr) {
			fail("completed plans need a PR link")
		}
		for _, s := range doneSubstantiveSection {
			body, ok := sections[s]
			if ok && (!substantive(body) || body == templateSectionBodies[s]) {
				fail("completed plans need substantive '## %s' content", s)
			}
		}
	case "Abandoned":
		if !substantive(sections["Outcome"]) {
			fail("abandoned plans need an Outcome explaining why work stopped")
		}
	default:
		if ready && !substantive(sections["Outcome"]) {
			fail("mark the plan Done before review, or explain in Outcome why it merges as %q", status)
		}
	}

	errs = append(errs, validateLinks(root, rel, text)...)
	return errs
}

func firstContentLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// field returns the value of the first top-level "Name: value" line.
func field(text, name string) (string, bool) {
	prefix := name + ":"
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", false
}

// parseSections maps each "## Heading" to its trimmed body, ignoring fenced code.
func parseSections(text string) map[string]string {
	sections := map[string]string{}
	var current string
	var body []string
	inFence := false
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimSpace(strings.Join(body, "\n"))
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			body = nil
			continue
		}
		body = append(body, line)
	}
	flush()
	return sections
}

func substantive(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && !placeholderRE.MatchString(v)
}

func validateLinks(root, rel, text string) []string {
	var errs []string
	dir := filepath.Dir(filepath.Join(root, rel))
	for _, m := range mdLinkRE.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimSpace(m[1] + m[2])
		target := raw
		if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		target, _, _ = strings.Cut(target, "#")
		if unescaped, err := url.PathUnescape(target); err == nil {
			target = unescaped
		}
		if target == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: broken local link: %s", rel, raw))
		}
	}
	return errs
}

// validateCodeSpanPaths flags backtick-quoted repo paths that no longer exist.
func validateCodeSpanPaths(root, path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	rel, _ := filepath.Rel(root, path)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	topLevel := map[string]bool{}
	for _, e := range entries {
		topLevel[e.Name()] = true
	}
	var errs []string
	seen := map[string]bool{}
	for _, m := range codeSpanRE.FindAllStringSubmatch(string(raw), -1) {
		span := m[1]
		target, _, _ := strings.Cut(span, "#")
		target = lineSuffixRE.ReplaceAllString(target, "")
		if !strings.Contains(target, "/") || strings.ContainsAny(target, nonPathChars) {
			continue
		}
		first, _, _ := strings.Cut(target, "/")
		if !topLevel[first] || seen[target] {
			continue
		}
		seen[target] = true
		if _, err := os.Lstat(filepath.Join(root, target)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: referenced path does not exist: %s", filepath.ToSlash(rel), span))
		}
	}
	return errs
}
