package dashboard

import (
	"strings"
	"testing"
)

func TestPatchInstructionsCoverKeyPathsAndCrossLinks(t *testing.T) {
	for _, want := range []string{
		"/spec/panels/",
		"/spec/layouts/0/spec/items/-",
		"/spec/display/name",
		"/spec/variables/-",
		"/tags/-",
		"signoz/CompositeQuery",
		"signoz/TextPanel",
		`"queries":[]`,
		"/spec/panels/runbook/spec/plugin/spec/text",
		`"$ref":"#/spec/panels/runbook"`,
		"signoz://dashboard/widgets-examples",
		"signoz://dashboard/instructions",
	} {
		if !strings.Contains(PatchInstructions, want) {
			t.Errorf("patch instructions missing %q", want)
		}
	}
}
