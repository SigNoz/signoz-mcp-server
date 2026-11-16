package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
)

const runMainEnv = "SIGNOZ_MCP_TEST_RUN_MAIN"

// TestRunMain is not a test on its own: TestVersionFlag re-executes the test
// binary with runMainEnv set so main() runs in a child process with real
// argv, stdout, stderr, and exit code.
func TestRunMain(t *testing.T) {
	if os.Getenv(runMainEnv) != "1" {
		t.Skip("helper process for TestVersionFlag")
	}
	os.Args = append([]string{"signoz-mcp-server"}, flag.Args()...)
	main()
	os.Exit(0)
}

func TestVersionFlag(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "double dash prints version without config", args: []string{"--version"}, wantStdout: version.UserAgent() + "\n"},
		{name: "single dash prints version without config", args: []string{"-version"}, wantStdout: version.UserAgent() + "\n"},
		{name: "no args still requires config", args: nil, wantCode: 1, wantStderr: "Configuration validation failed"},
		{name: "version after end of options is not a flag", args: []string{"--", "--version"}, wantCode: 1, wantStderr: "Configuration validation failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestRunMain$", "--"}, tt.args...)...)
			cmd.Env = append(envWithoutServerConfig(), runMainEnv+"=1")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			code := 0
			if err := cmd.Run(); err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("run main: %v", err)
				}
				code = exitErr.ExitCode()
			}

			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if stdout.String() != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr == "" && stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// envWithoutServerConfig drops server configuration so the child behaves as a
// fresh install regardless of the developer's or CI's environment.
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
