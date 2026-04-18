package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestPrinter_LineAndWarnRouteToDistinctSinks(t *testing.T) {
	var out, errb bytes.Buffer
	p := &Printer{Out: &out, Err: &errb}

	p.Line("hello %s", "world")
	p.Warn("oops %d", 42)
	p.Raw("tail")

	if got := out.String(); got != "hello world\ntail" {
		t.Errorf("Out = %q, want %q", got, "hello world\ntail")
	}
	if got := errb.String(); got != "oops 42\n" {
		t.Errorf("Err = %q, want %q", got, "oops 42\n")
	}
}

func TestPrinterFrom_CapturesCobraOutputStreams(t *testing.T) {
	var out, errb bytes.Buffer
	cmd := &cobra.Command{Use: "dummy"}
	cmd.SetOut(&out)
	cmd.SetErr(&errb)

	p := printerFrom(cmd)
	p.Line("captured stdout")
	p.Warn("captured stderr")

	if !strings.Contains(out.String(), "captured stdout") {
		t.Errorf("stdout capture missing: %q", out.String())
	}
	if !strings.Contains(errb.String(), "captured stderr") {
		t.Errorf("stderr capture missing: %q", errb.String())
	}
}

func TestPrinterFrom_NilCmdFallsBackToOSStreams(t *testing.T) {
	p := printerFrom(nil)
	if p.Out == nil || p.Err == nil {
		t.Fatalf("nil sinks: Out=%v Err=%v", p.Out, p.Err)
	}
}
