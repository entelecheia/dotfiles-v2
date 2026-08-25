//go:build !darwin && !linux

package fileutil

import (
	"fmt"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func backup(_ *exec.Runner, _ string, path string) error {
	return fmt.Errorf("backing up %q is unsupported on this platform", path)
}
