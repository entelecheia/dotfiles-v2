package exec

import "fmt"

// CmdError captures structured context from a failed command execution.
// Use errors.As to extract it from error chains produced by Runner methods.
type CmdError struct {
	Cmd      string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *CmdError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("command %q failed: %v", e.Cmd, e.Err)
	}
	return fmt.Sprintf("command %q failed: %v\nstderr: %s", e.Cmd, e.Err, e.Stderr)
}

func (e *CmdError) Unwrap() error { return e.Err }
