package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// Printer routes CLI output to writable sinks so tests can capture output.
// Obtain one via printerFrom(cmd); commands that don't have a cobra context
// can construct one manually with Printer{Out: ..., Err: ...}.
type Printer struct {
	Out io.Writer
	Err io.Writer
}

// printerFrom returns a Printer wired to the command's output streams.
// Falls back to os.Stdout/os.Stderr when cmd is nil (defensive — cobra
// always populates these in practice).
func printerFrom(cmd *cobra.Command) *Printer {
	if cmd == nil {
		return &Printer{Out: os.Stdout, Err: os.Stderr}
	}
	return &Printer{Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
}

// Line prints a formatted line to Out with a trailing newline.
func (p *Printer) Line(format string, a ...any) {
	fmt.Fprintf(p.Out, format+"\n", a...)
}

// Warn prints a formatted line to Err with a trailing newline.
func (p *Printer) Warn(format string, a ...any) {
	fmt.Fprintf(p.Err, format+"\n", a...)
}

// Raw prints to Out without adding a newline (for progress dots, prompts).
func (p *Printer) Raw(format string, a ...any) {
	fmt.Fprintf(p.Out, format, a...)
}

// KV renders a styled key/value line. Empty values render as "(unset)" in
// the hint style so the column alignment stays consistent across calls.
func (p *Printer) KV(key, value string) {
	if value == "" {
		value = ui.StyleHint.Render("(unset)")
	} else {
		value = ui.StyleValue.Render(value)
	}
	fmt.Fprintf(p.Out, "  %s  %s\n", ui.StyleKey.Render(key+":"), value)
}
