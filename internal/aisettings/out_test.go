package aisettings

import (
	"os"
	"testing"
)

// TestAgentsManagerOut_NilDefaultsToStdout verifies that a manager built by
// NewAgentsManager (which assigns no Out) and a nil *AgentsManager both fall
// back to os.Stdout. Without this guarantee `dot ai agents author` — which
// reaches the prompts in authorInteractive on every production path today —
// would write through a nil writer and panic.
func TestAgentsManagerOut_NilDefaultsToStdout(t *testing.T) {
	m := NewAgentsManager(nil, "")
	if got := m.out(); got != os.Stdout {
		t.Errorf("NewAgentsManager out() = %v, want os.Stdout", got)
	}

	var nilM *AgentsManager
	if got := nilM.out(); got != os.Stdout {
		t.Errorf("nil receiver out() = %v, want os.Stdout", got)
	}
}
