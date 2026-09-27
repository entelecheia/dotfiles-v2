package resourceguard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunInteractiveTTYHelper(t *testing.T) {
	if os.Getenv("DOT_RESOURCE_TTY_HELPER") != "1" {
		return
	}
	duration := 3 * time.Second
	canceled := os.Getenv("DOT_RESOURCE_TTY_CANCEL") == "1"
	if canceled {
		duration = 150 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	command := exec.Command("/bin/sh", "-c", `printf 'TTY_READY\n'; IFS= read -r answer; printf 'TTY_ANSWER=%s\n' "$answer"`)
	if canceled {
		command = exec.Command("/bin/sh", "-c", `printf 'TTY_READY\n'; sleep 60`)
	}
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	err := RunCommand(ctx, command)
	if canceled {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancellation: %v", err)
		}
		if _, err := fmt.Fprintln(os.Stdout, "TTY_CANCELED"); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	foreground, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	if err != nil || foreground != syscall.Getpgrp() {
		t.Fatalf("foreground not restored: %d %v", foreground, err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "TTY_RESTORED"); err != nil {
		t.Fatal(err)
	}
}
func TestRunInteractiveTTYReadsAndRestoresForeground(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 PTY fixture unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const fixture = `import errno, os, pty, select, signal, sys, time
pid, master = pty.fork()
if pid == 0:
    env = dict(os.environ, DOT_RESOURCE_TTY_HELPER="1", DOT_RESOURCE_TTY_CANCEL=sys.argv[2])
    os.execve(sys.argv[1], [sys.argv[1], "-test.run=^TestRunInteractiveTTYHelper$"], env)
output = b""
sent = False
reaped = False
try:
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], 0.1)
        if ready:
            try:
                chunk = os.read(master, 8192)
            except OSError as exc:
                if exc.errno == errno.EIO:
                    break
                raise
            if not chunk:
                break
            output += chunk
            if b"TTY_READY" in output and not sent:
                os.write(master, b"literal answer\n")
                sent = True
        if not reaped:
            result, status = os.waitpid(pid, os.WNOHANG)
            if result:
                reaped = True
                if status != 0:
                    raise RuntimeError("helper failed: " + output.decode(errors="replace"))
    expected = b"TTY_CANCELED" if sys.argv[2] == "1" else b"TTY_ANSWER=literal answer"
    if expected not in output or b"TTY_RESTORED" not in output:
        raise RuntimeError("terminal read or restore failed: " + output.decode(errors="replace"))
finally:
    if not reaped:
        try:
            os.killpg(pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass
        os.waitpid(pid, 0)
    os.close(master)
print(output.decode(errors="replace"))
`
	for _, mode := range []string{"0", "1"} {
		t.Run("cancel="+mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, python, "-c", fixture, executable, mode).CombinedOutput()
			if err != nil {
				t.Fatalf("PTY fixture: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "TTY_RESTORED") {
				t.Fatal(string(output))
			}
		})
	}
}
