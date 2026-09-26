package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzRunnerFailureReplay(t *testing.T) {
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
			write(corpus+"/$(touch${IFS}PWNED)", []byte("go test fuzz v1\nint64(0)\n"), 0o644)
			write("bin/git", []byte("#!/bin/bash\necho fixture\n"), 0o755)
			// The failure line and corpus encoding match a recorded native Go fuzz failure.
			write("bin/go", []byte(`#!/bin/bash
set -eu
case "$1" in
  version) echo 'go version go1.26.0' ;;
  env) echo 'go1.26.0' ;;
  test)
    if [[ ${FUZZ_REPLAY:-} == 1 ]]; then
      printf '%s\n' "$@" >> "$FUZZ_REPLAY_ARGS"
      exit 0
    fi
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
			t.Setenv("FUZZ_REPLAY", "")
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
			replayFile := filepath.Join(runs[0], "replay.txt")
			replay, err := os.ReadFile(replayFile)
			if err != nil {
				t.Fatal(err)
			}
			argsFile := filepath.Join(root, "replayed-args")
			t.Setenv("FUZZ_REPLAY", "1")
			t.Setenv("FUZZ_REPLAY_ARGS", argsFile)
			command = exec.Command("bash", replayFile)
			command.Dir = root
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("replay failed: %v\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(root, "PWNED")); !os.IsNotExist(err) {
				t.Error("replay executed a command embedded in a corpus filename")
			}
			files, err := filepath.Glob(filepath.Join(runs[0], corpus, "*"))
			if err != nil {
				t.Fatal(err)
			}
			pattern := "^" + target + "$"
			if scenario == "generated" {
				pattern = "^" + target + "/" + input + "$"
				if len(files) != 1 || filepath.Base(files[0]) != input {
					t.Errorf("artifact must contain only the reported failure: %v", files)
				}
			} else if len(files) != 0 || !strings.Contains(string(replay), "without a new saved input") {
				t.Errorf("existing corpus files were mislabeled as new failures: files=%v replay=%s", files, replay)
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			want := "test\n./pkg/timeutil\n-run=" + pattern + "\n-count=1\n"
			if string(args) != want {
				t.Errorf("replay selected the wrong inputs: want %q, got %q", want, args)
			}
		})
	}
}
