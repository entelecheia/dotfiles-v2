package main

import (
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
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
