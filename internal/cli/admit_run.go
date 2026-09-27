package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/admission"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// The run side of `dot admit`: lease identity, process launch, heartbeat,
// signal forwarding. The gate and slot acquisition live in admit_cmd.go.

// buildSelfLease fills the process-identity fields of the lease this run
// would hold. PIDStart is normalized exactly the way the watchdog compares
// it, so a later stale-owner probe matches this incarnation. It fails
// closed: without a verified start time the lease's identity is
// unverifiable, and an unverifiable lease could later be reclaimed from a
// live owner — so no lease is better than a guess.
func buildSelfLease(ctx context.Context, runner *exec.Runner, cwd string) (admission.Lease, error) {
	owner := os.Getenv(admission.OwnerEnv)
	if owner == "" {
		username := "unknown"
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
		host, _ := os.Hostname()
		owner = username + "@" + host
	}
	started, err := psStartTime(ctx, runner, os.Getpid())
	if err != nil {
		return admission.Lease{}, fmt.Errorf("cannot verify this process's start time (%v); refusing to acquire a slot with an unverifiable identity", err)
	}
	pgid, _ := syscall.Getpgid(os.Getpid())
	return admission.Lease{
		Owner:    owner,
		Session:  os.Getenv(admission.SessionEnv),
		PID:      os.Getpid(),
		PIDStart: started,
		PGID:     pgid,
		CWD:      cwd,
	}, nil
}

// psStartTime reads one process's lstart, normalized the way
// watchdog.ProcessStartMatches compares it. An empty result is an error:
// it would record an unverifiable identity.
func psStartTime(ctx context.Context, runner *exec.Runner, pid int) (string, error) {
	res, err := runner.RunQuery(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", err
	}
	started := strings.Join(strings.Fields(res.Stdout), " ")
	if started == "" {
		return "", fmt.Errorf("ps returned no start time for pid %d", pid)
	}
	return started, nil
}

// runAdmittedChild executes the wrapped command with stdio and signal
// forwarding, a heartbeat goroutine while a slot is held, and the nested-run
// marker in the environment. The child's exit code becomes dot's.
//
// Two contracts live here. First, once the child starts, the lease is
// rewired to the CHILD's identity: the wrapper can be SIGKILLed while the
// workload keeps running in its own process group, and only a lease that
// names the child keeps the slot busy until the workload actually exits.
// Failing to verify the child's identity kills the child and aborts rather
// than holding the slot with an unverifiable one. Second, with --json the
// completion record goes to STDERR: the wrapped command's stdout is the
// payload, and mixing the JSON object into it would corrupt the stream for
// downstream consumers (the process exit code is the machine-readable
// result). Defer outcomes never run a child, so they stay on stdout.
func runAdmittedChild(ctx context.Context, p *Printer, runner *exec.Runner, args []string, scope, class string, slot *admission.Slot, asJSON bool) error {
	child := osexec.Command(args[0], args[1:]...) // #nosec G204 -- the command is the user's own invocation
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Env = append(os.Environ(), admission.NestedEnv+"="+admission.NestedEnvValue(scope, class))
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		return fmt.Errorf("starting %q: %w", strings.Join(args, " "), err)
	}
	if slot != nil {
		started, err := psStartTime(ctx, runner, child.Process.Pid)
		if err == nil {
			err = slot.UpdateIdentity(child.Process.Pid, started)
		}
		if err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			return fmt.Errorf("binding the slot lease to the child workload: %w", err)
		}
	}

	stopHB := make(chan struct{})
	if slot != nil {
		go func() {
			ticker := time.NewTicker(admission.DefaultHeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					// A failed heartbeat (slot lost) only shows up in the
					// next probe; the child keeps running either way.
					_ = slot.Heartbeat()
				case <-stopHB:
					return
				}
			}
		}()
	}
	defer close(stopHB)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-sigCh:
				if s, ok := sig.(syscall.Signal); ok && child.Process != nil {
					if pgid, err := syscall.Getpgid(child.Process.Pid); err == nil {
						_ = syscall.Kill(-pgid, s)
					}
				}
			case <-done:
				return
			}
		}
	}()

	waitErr := child.Wait()
	exitCode := 0
	if waitErr != nil {
		exitCode = 1
		var exitErr *osexec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				exitCode = 128 + int(ws.Signal())
			}
		}
	}
	if asJSON {
		data, err := json.MarshalIndent(map[string]any{
			"outcome":   "completed",
			"scope":     scope,
			"class":     class,
			"exit_code": exitCode,
		}, "", "  ")
		if err != nil {
			return err
		}
		// STDERR, deliberately: the child's stdout is the payload, and the
		// exit code is the machine-readable result.
		if _, err := fmt.Fprintf(p.Err, "%s\n", data); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitCodeError{Code: exitCode, Err: fmt.Errorf("command exited with status %d", exitCode)}
	}
	return nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
