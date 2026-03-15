package main

import (
	"os"

	"github.com/entelecheia/dotfiles-v2/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := cli.Execute(version, commit); err != nil {
		os.Exit(1)
	}
}
