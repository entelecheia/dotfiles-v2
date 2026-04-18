package cli

import (
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// ResolveVersion returns effective version and commit strings, falling back
// to Go's embedded VCS info when ldflags weren't set (e.g., plain `go build`).
func ResolveVersion(version, commit string) (string, string) {
	if version == "" {
		version = "dev"
	}
	if commit == "" || commit == "none" {
		if info, ok := debug.ReadBuildInfo(); ok {
			var vcsRev, vcsMod string
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					vcsRev = s.Value
				case "vcs.modified":
					vcsMod = s.Value
				}
			}
			if vcsRev != "" {
				if len(vcsRev) > 7 {
					vcsRev = vcsRev[:7]
				}
				if vcsMod == "true" {
					vcsRev += "-dirty"
				}
				commit = vcsRev
			}
		}
	}
	if commit == "" {
		commit = "none"
	}
	return version, commit
}

func newVersionCmd(version, commit string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			v, c := ResolveVersion(version, commit)
			p := printerFrom(cmd)
			p.Line("dotfiles %s (%s)", v, c)
			p.Line("  go:   %s", runtime.Version())
			p.Line("  os:   %s/%s", runtime.GOOS, runtime.GOARCH)
		},
	}
}
