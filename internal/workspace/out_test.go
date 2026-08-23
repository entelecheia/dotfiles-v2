package workspace

import (
	"bytes"
	"os"
	"testing"
)

// TestOutOrStdout_NilYieldsStdoutOtherwiseIdentity verifies the package's nil
// normalizer: nil yields os.Stdout, anything else is returned unchanged. This
// is the reachable assertion for the InstallRequired and InstallOptional
// guards — both shell out to brew and cannot be driven deterministically end
// to end in a unit test.
func TestOutOrStdout_NilYieldsStdoutOtherwiseIdentity(t *testing.T) {
	if got := outOrStdout(nil); got != os.Stdout {
		t.Errorf("outOrStdout(nil) = %v, want os.Stdout", got)
	}

	var buf bytes.Buffer
	if got := outOrStdout(&buf); got != &buf {
		t.Errorf("outOrStdout(&buf) = %v, want the same writer", got)
	}
}
