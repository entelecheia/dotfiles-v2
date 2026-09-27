package resourceguard

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// foregroundTerminal keeps the isolated owned process group usable with a real
// controlling terminal. Pipes and terminals not controlling this session need
// no transfer. A background caller must not steal another job's foreground TTY.
func foregroundTerminal(supervisor *exec.Cmd, input io.Reader) (func() error, error) {
	noop := func() error { return nil }
	terminal, ok := input.(*os.File)
	if !ok {
		return noop, nil
	}
	fd := int(terminal.Fd())
	original, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err == unix.ENOTTY || err == unix.ENODEV {
		return noop, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect command terminal: %w", err)
	}
	if original != syscall.Getpgrp() {
		return nil, fmt.Errorf("interactive resource command requires its caller's foreground terminal")
	}
	supervisor.SysProcAttr.Foreground = true
	supervisor.SysProcAttr.Ctty = fd
	return func() error {
		current, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		if err != nil {
			return fmt.Errorf("inspect terminal during restore: %w", err)
		}
		if current == original {
			return nil
		}
		// Go's fork/exec implementation performs its foreground ioctl with signals
		// blocked. A tiny child can safely restore our original group without changing
		// process-wide SIGTTOU handlers in this process or signaling the parent group.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		restore := exec.CommandContext(ctx, "/bin/sh", "-c", "exit 0")
		restore.SysProcAttr = &syscall.SysProcAttr{Foreground: true, Pgid: original, Ctty: fd}
		if err := restore.Run(); err != nil {
			return fmt.Errorf("restore caller terminal foreground: %w", err)
		}
		return nil
	}, nil
}
