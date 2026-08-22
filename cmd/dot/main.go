package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/entelecheia/dotfiles-v2/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := cli.Execute(version, commit); err != nil {
		// The unknown-command gate has already written its own guidance;
		// printing "Error: unknown command" behind it would add a seventh
		// line. Every other error keeps the existing format.
		if !errors.Is(err, cli.ErrUnknownCommand) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
