package log

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

func TestNew_KeepsStdoutFreeForStdioFrames(t *testing.T) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW
	t.Cleanup(func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
	})

	logger := New("info")
	logger.InfoContext(context.Background(), "stdio-safe")

	if err := stderrW.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}

	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	stdoutBytes, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	if len(stdoutBytes) != 0 {
		t.Fatalf("expected no stdout log output, got %q", string(stdoutBytes))
	}
	if !bytes.Contains(stderrBytes, []byte("stdio-safe")) {
		t.Fatalf("expected stderr to contain log message, got %q", string(stderrBytes))
	}
}
