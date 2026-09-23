// Command check-repo-docs validates plans/ against plans/README.md and flags
// stale repo paths in docs/, README.md, CONTRIBUTING.md, and CLAUDE.md.
//
// With -ready, plans changed since -base must be Done, Abandoned, or explain in
// Outcome why they merge incomplete.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	ready := flag.Bool("ready", false, "apply ready-for-review rules to plans changed since -base")
	base := flag.String("base", "origin/main", "base ref for -ready")
	flag.Parse()

	var opts options
	if *ready {
		changed, added, err := changedPlans(*root, *base)
		if err != nil {
			fmt.Fprintf(os.Stderr, "check-repo-docs: %v\n", err)
			os.Exit(2)
		}
		opts = options{changed: changed, added: added}
	}

	if errs := validateRepository(*root, opts); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "Repository docs validation failed:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "- %s\n", e)
		}
		os.Exit(1)
	}
	fmt.Println("Repository docs validation passed.")
}

// changedPlans lists plans changed between the merge base and the working tree,
// including untracked files, so local runs see uncommitted work.
func changedPlans(root, base string) (changed, added []string, err error) {
	mergeBase, err := git(root, "merge-base", base, "HEAD")
	if err != nil {
		return nil, nil, err
	}
	diff, err := git(root, "diff", "--name-status", "--no-renames", "--diff-filter=AM", strings.TrimSpace(mergeBase), "--", "plans/")
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(diff, "\n") {
		kind, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		changed = append(changed, path)
		if kind == "A" {
			added = append(added, path)
		}
	}
	untracked, err := git(root, "ls-files", "--others", "--exclude-standard", "--", "plans/")
	if err != nil {
		return nil, nil, err
	}
	for _, path := range strings.Fields(untracked) {
		changed = append(changed, path)
		added = append(added, path)
	}
	return changed, added, nil
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out), nil
}
