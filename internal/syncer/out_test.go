package syncer

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

// TestConfigOut_NilDefaultsToStdout verifies that a zero-value Config and a
// nil *Config both fall back to os.Stdout. Without this guarantee a caller
// that forgets to assign Out would panic or silently discard the only record
// of what a sync did to the user's files.
func TestConfigOut_NilDefaultsToStdout(t *testing.T) {
	cfg := &Config{}
	if got := cfg.out(); got != os.Stdout {
		t.Errorf("zero-value out() = %v, want os.Stdout", got)
	}

	var nilCfg *Config
	if got := nilCfg.out(); got != os.Stdout {
		t.Errorf("nil receiver out() = %v, want os.Stdout", got)
	}
}

// TestConfigOut_SharedWriterInterleavesInCallOrder verifies that two Config
// values sharing one writer emit their lines in call order with no added
// synchronization or buffering. Without this guarantee a sync interrupted
// mid-transfer could lose lines it already printed.
func TestConfigOut_SharedWriterInterleavesInCallOrder(t *testing.T) {
	var buf bytes.Buffer
	a := &Config{Out: &buf}
	b := &Config{Out: &buf}

	fmt.Fprintf(a.out(), "first\n")
	fmt.Fprintf(b.out(), "second\n")
	fmt.Fprintf(a.out(), "third\n")

	want := "first\nsecond\nthird\n"
	if buf.String() != want {
		t.Errorf("buffer = %q, want %q", buf.String(), want)
	}
}

// TestOutOrStdout_NilYieldsStdoutOtherwiseIdentity verifies the package's nil
// normalizer: nil yields os.Stdout, anything else is returned unchanged. This
// is the reachable assertion for InstallRsync's guard, because the function
// itself shells out to brew or apt and cannot be driven deterministically in
// a unit test.
func TestOutOrStdout_NilYieldsStdoutOtherwiseIdentity(t *testing.T) {
	if got := outOrStdout(nil); got != os.Stdout {
		t.Errorf("outOrStdout(nil) = %v, want os.Stdout", got)
	}

	var buf bytes.Buffer
	if got := outOrStdout(&buf); got != &buf {
		t.Errorf("outOrStdout(&buf) = %v, want the same writer", got)
	}
}
