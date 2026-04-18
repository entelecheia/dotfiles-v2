package exec

import (
	"context"
	"log/slog"
	"os"
	osexec "os/exec"
	"strings"
)

// Runner wraps shell command execution and file I/O with dry-run support.
type Runner struct {
	DryRun bool
	Logger *slog.Logger
}

// Result holds the output of a command execution.
type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
}

// NewRunner creates a new Runner.
func NewRunner(dryRun bool, logger *slog.Logger) *Runner {
	return &Runner{DryRun: dryRun, Logger: logger}
}

// Run executes a command. In dry-run mode, logs but does not execute.
func (r *Runner) Run(ctx context.Context, name string, args ...string) (*Result, error) {
	cmdStr := name + " " + strings.Join(args, " ")

	if r.DryRun {
		r.Logger.Info("dry-run", "cmd", cmdStr)
		return &Result{Command: cmdStr}, nil
	}

	r.Logger.Info("exec", "cmd", cmdStr)
	cmd := osexec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &Result{
		Command: cmdStr,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		return result, &CmdError{Cmd: cmdStr, Stderr: result.Stderr, ExitCode: result.ExitCode, Err: err}
	}
	return result, nil
}

// RunQuery executes a read-only command that always runs, even in dry-run mode.
// Use for detection/query commands (brew list, git status, gh auth status) that
// don't modify the system but are needed to determine what changes are required.
func (r *Runner) RunQuery(ctx context.Context, name string, args ...string) (*Result, error) {
	cmdStr := name + " " + strings.Join(args, " ")
	r.Logger.Debug("query", "cmd", cmdStr)

	cmd := osexec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &Result{
		Command: cmdStr,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		return result, &CmdError{Cmd: cmdStr, Stderr: result.Stderr, ExitCode: result.ExitCode, Err: err}
	}
	return result, nil
}

// RunAttached executes a command with stdout/stderr connected to the terminal.
// Use for long-running commands where the user needs to see progress.
func (r *Runner) RunAttached(ctx context.Context, name string, args ...string) error {
	cmdStr := name + " " + strings.Join(args, " ")

	if r.DryRun {
		r.Logger.Info("dry-run", "cmd", cmdStr)
		return nil
	}

	r.Logger.Info("exec (attached)", "cmd", cmdStr)
	cmd := osexec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return &CmdError{Cmd: cmdStr, Err: err}
	}
	return nil
}

// RunInteractive executes a command with stdin, stdout, stderr all attached to
// the terminal. Use for interactive flows that need a TTY (e.g. OAuth prompts,
// editor invocations, password entry).
//
// In dry-run mode, logs a warning and skips execution. Callers must treat
// dry-run as a non-op — interactive side-effects cannot be simulated.
func (r *Runner) RunInteractive(ctx context.Context, name string, args ...string) error {
	cmdStr := name + " " + strings.Join(args, " ")

	if r.DryRun {
		r.Logger.Warn("dry-run: skipped interactive command (requires real TTY)", "cmd", cmdStr)
		return nil
	}

	r.Logger.Info("exec (interactive)", "cmd", cmdStr)
	cmd := osexec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return &CmdError{Cmd: cmdStr, Err: err}
	}
	return nil
}

// RunShell executes a command via "sh -c" for pipes and redirects.
func (r *Runner) RunShell(ctx context.Context, script string) (*Result, error) {
	return r.Run(ctx, "sh", "-c", script)
}

// CommandExists checks if a command is available in PATH (never dry-run gated).
func (r *Runner) CommandExists(name string) bool {
	_, err := osexec.LookPath(name)
	return err == nil
}

// FileExists checks if a path exists (never dry-run gated).
func (r *Runner) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir checks if a path is a directory (never dry-run gated).
func (r *Runner) IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// ReadFile reads a file (never dry-run gated — reads are always real).
func (r *Runner) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes content to a path. Respects dry-run.
func (r *Runner) WriteFile(path string, content []byte, perm os.FileMode) error {
	if r.DryRun {
		r.Logger.Info("dry-run: write file", "path", path, "size", len(content))
		return nil
	}
	r.Logger.Info("write file", "path", path, "size", len(content))
	return os.WriteFile(path, content, perm)
}

// MkdirAll creates directories. Respects dry-run.
//
// Logs at Debug level — setup flows create many dirs (30+ in a fresh install)
// so keeping these at Info would drown out the interesting events like command
// execution and file writes. Enable with `-v debug` to audit every mkdir.
func (r *Runner) MkdirAll(path string, perm os.FileMode) error {
	if r.DryRun {
		r.Logger.Debug("dry-run: mkdir", "path", path)
		return nil
	}
	r.Logger.Debug("mkdir", "path", path)
	return os.MkdirAll(path, perm)
}

// Symlink creates a symbolic link. Respects dry-run.
func (r *Runner) Symlink(target, link string) error {
	if r.DryRun {
		r.Logger.Info("dry-run: symlink", "target", target, "link", link)
		return nil
	}
	r.Logger.Info("symlink", "target", target, "link", link)
	return os.Symlink(target, link)
}

// Remove removes a file or empty directory. Respects dry-run.
func (r *Runner) Remove(path string) error {
	if r.DryRun {
		r.Logger.Info("dry-run: remove", "path", path)
		return nil
	}
	r.Logger.Info("remove", "path", path)
	return os.Remove(path)
}

// RemoveAll removes a path and any children it contains. Respects dry-run.
func (r *Runner) RemoveAll(path string) error {
	if r.DryRun {
		r.Logger.Info("dry-run: remove-all", "path", path)
		return nil
	}
	r.Logger.Info("remove-all", "path", path)
	return os.RemoveAll(path)
}

// Readlink reads a symlink target (never dry-run gated).
func (r *Runner) Readlink(path string) (string, error) {
	return os.Readlink(path)
}

// IsSymlink checks if a path is a symlink (never dry-run gated).
func (r *Runner) IsSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
