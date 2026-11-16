package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const runMainEnv = "SIGNOZ_MCP_TEST_RUN_MAIN"

// runMain re-executes this test binary with runMainEnv set, which runs main
// instead of the tests, with real argv, stdout, stderr, and exit code.
func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMain runs main in a child process with no server configuration in its
// environment, as on a fresh install.
func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(envWithoutServerConfig(), runMainEnv+"=1")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run main %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return code, out.String(), errOut.String()
}

func envWithoutServerConfig() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "SIGNOZ_") || strings.HasPrefix(name, "MCP_") || name == "TRANSPORT_MODE" || name == "LOG_LEVEL" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func TestVersionFlag_PrintsVersionAndExitsWithoutConfig(t *testing.T) {
	// Test binaries are built without the release ldflags, so the version is "dev".
	for _, arg := range []string{"--version", "-version"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr := runMain(t, arg)

			if code != 0 || stdout != "signoz-mcp-server/dev\n" || stderr != "" {
				t.Fatalf("%s must print the version to stdout and exit 0 before loading config;\n"+
					"got exit %d, stdout %q, stderr %q.\n"+
					"Check that main calls printVersion before config.LoadConfig.",
					arg, code, stdout, stderr)
			}
		})
	}
}

func TestVersionFlag_AfterEndOfOptionsStartsServerNormally(t *testing.T) {
	code, stdout, stderr := runMain(t, "--", "--version")

	if code == 0 || strings.Contains(stdout, "signoz-mcp-server/") {
		t.Fatalf("--version after \"--\" is an operand, not the flag, so startup must continue and fail on missing config;\n"+
			"got exit %d, stdout %q, stderr %q.\n"+
			"Check that printVersion stops scanning at \"--\".",
			code, stdout, stderr)
	}
}
