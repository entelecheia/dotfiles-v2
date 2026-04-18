package exec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCmdError_Error_WithStderr(t *testing.T) {
	e := &CmdError{
		Cmd:      "rclone lsd gdrive:",
		Stderr:   "Failed to lsd: directory not found\n",
		ExitCode: 1,
		Err:      errors.New("exit status 1"),
	}
	got := e.Error()
	if !strings.Contains(got, "rclone lsd gdrive:") {
		t.Errorf("missing cmd in output: %q", got)
	}
	if !strings.Contains(got, "stderr: Failed to lsd") {
		t.Errorf("missing stderr block: %q", got)
	}
}

func TestCmdError_Error_WithoutStderr(t *testing.T) {
	e := &CmdError{Cmd: "age -d ...", Err: errors.New("exit status 1")}
	got := e.Error()
	if strings.Contains(got, "stderr:") {
		t.Errorf("should not include stderr label when empty: %q", got)
	}
}

func TestCmdError_Unwrap(t *testing.T) {
	underlying := errors.New("exit status 1")
	e := &CmdError{Cmd: "foo", Err: underlying}
	if errors.Unwrap(e) != underlying {
		t.Error("Unwrap did not return the wrapped error")
	}
}

func TestCmdError_ErrorsAs_RoundTripsThroughFmtErrorf(t *testing.T) {
	orig := &CmdError{
		Cmd:    "rclone copy ...",
		Stderr: "permission denied: /path",
		Err:    errors.New("exit status 2"),
	}
	wrapped := fmt.Errorf("pull failed: %w", orig)

	var ce *CmdError
	if !errors.As(wrapped, &ce) {
		t.Fatalf("errors.As did not match CmdError in chain")
	}
	if ce.Cmd != "rclone copy ..." {
		t.Errorf("Cmd round-trip mismatch: %q", ce.Cmd)
	}
	if got := ce.Details(); got != "permission denied: /path" {
		t.Errorf("Details() = %q, want %q", got, "permission denied: /path")
	}
}

func TestCmdError_Details_TrimsWhitespace(t *testing.T) {
	e := &CmdError{Stderr: "  line one\nline two\n  \n"}
	want := "line one\nline two"
	if got := e.Details(); got != want {
		t.Errorf("Details() = %q, want %q", got, want)
	}
}
