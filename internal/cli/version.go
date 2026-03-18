package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd(version, commit string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("dotfiles %s (%s)\n", version, commit)
			fmt.Printf("  go:   %s\n", runtime.Version())
			fmt.Printf("  os:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
