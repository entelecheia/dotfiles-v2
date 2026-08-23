package ws

import (
	"os"
	"testing"
)

// TestInitOptionsOut_NilDefaultsToStdout verifies that a zero-value
// InitOptions falls back to os.Stdout from its value-receiver accessor. This
// is a live case, not a hypothetical: internal/cli/ws_cmd.go builds
// InitOptions without an Out, so `dot ws init` reaches Init with the field
// unset while `dot open` reaches it with the field set from rc.out().
func TestInitOptionsOut_NilDefaultsToStdout(t *testing.T) {
	opts := InitOptions{}
	if got := opts.out(); got != os.Stdout {
		t.Errorf("zero-value out() = %v, want os.Stdout", got)
	}
}
