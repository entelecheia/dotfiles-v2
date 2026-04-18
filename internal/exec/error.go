package exec

import (
	"fmt"
	"strings"
)

// CmdError captures structured context from a failed command execution.
// Use errors.As to extract it from error chains produced by Runner methods.
type CmdError struct {
	Cmd      string
	Stderr   string
	ExitCode int
	Err      error
}

// Error returns a human-readable message that embeds the captured stderr so
// error chains produced by `fmt.Errorf("...%w", err)` remain informative
// without callers having to inspect the struct. CLI formatters that want to
// display stderr as a separate block can call Details() after matching via
// errors.As.
func (e *CmdError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("command %q failed: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("command %q failed: %v\nstderr: %s", e.Cmd, e.Err, e.Stderr)
}

// Unwrap exposes the underlying os/exec error for errors.Is chains.
func (e *CmdError) Unwrap() error { return e.Err }

// Details returns the captured stderr with surrounding whitespace trimmed,
// suitable for rendering as a separate block beneath a one-line summary.
// Returns "" when no stderr was captured.
func (e *CmdError) Details() string { return strings.TrimSpace(e.Stderr) }
