package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzRunnerFailureArtifacts(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("the fuzz runner requires Bash")
	}
	runner, err := os.ReadFile("test-fuzz.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"generated", "seed", "build"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			write := func(name string, content []byte, mode os.FileMode) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, content, mode); err != nil {
					t.Fatal(err)
				}
			}
			const target = "FuzzExplicitTimestampUnits"
			const input = "b98d2f0b4719c1e0"
			const corpus = "pkg/timeutil/testdata/fuzz/" + target
			write("scripts/test-fuzz.sh", runner, 0o755)
			write(corpus+"/old-regression", []byte("go test fuzz v1\nint64(0)\n"), 0o644)
			write("bin/git", []byte("#!/bin/bash\necho fixture\n"), 0o755)
			// The failure line and corpus encoding match a recorded native Go fuzz failure.
			write("bin/go", []byte(`#!/bin/bash
set -eu
case "$1" in
  version) echo 'go version go1.26.0' ;;
  test)
    case "$FUZZ_SCENARIO" in
      generated)
        printf 'go test fuzz v1\nint64(1)\n' > pkg/timeutil/testdata/fuzz/FuzzExplicitTimestampUnits/b98d2f0b4719c1e0
        echo '    Failing input written to testdata/fuzz/FuzzExplicitTimestampUnits/b98d2f0b4719c1e0'
        ;;
      seed) echo '--- FAIL: FuzzExplicitTimestampUnits/old-regression' ;;
      build) echo 'FAIL [build failed]' ;;
    esac
    exit 1
    ;;
esac
`), 0o755)
			t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FUZZ_TARGET", target)
			t.Setenv("FUZZ_SCENARIO", scenario)
			command := exec.Command("bash", "scripts/test-fuzz.sh")
			command.Dir = root
			output, runErr := command.CombinedOutput()
			if runErr == nil || command.ProcessState.ExitCode() != 1 {
				t.Fatalf("runner must report the failed campaign: %v\n%s", runErr, output)
			}
			runs, err := filepath.Glob(filepath.Join(root, ".fuzz-artifacts", "run.*"))
			if err != nil || len(runs) != 1 {
				t.Fatalf("expected one artifact directory: %v, %v", runs, err)
			}
			log, err := os.ReadFile(filepath.Join(runs[0], target+".log"))
			if err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(runs[0], corpus, "*"))
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "generated" {
				if len(files) != 1 || filepath.Base(files[0]) != input {
					t.Fatalf("artifact must contain only the reported failure: %v", files)
				}
				data, err := os.ReadFile(files[0])
				if err != nil || string(data) != "go test fuzz v1\nint64(1)\n" {
					t.Errorf("saved input is not replayable: %q, %v", data, err)
				}
			} else if len(files) != 0 || !strings.Contains(string(log), "without a new saved input") {
				t.Errorf("existing corpus files were mislabeled as new failures: files=%v log=%s", files, log)
			}
		})
	}
}
