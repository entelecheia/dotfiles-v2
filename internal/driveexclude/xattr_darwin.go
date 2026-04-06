//go:build darwin

package driveexclude

import (
	"os/exec"
	"strings"
)

func setIgnoreContent(path string) error {
	return exec.Command("xattr", "-w", "com.google.drivefs.ignorecontent", "1", path).Run()
}

func hasIgnoreContent(path string) (bool, error) {
	out, err := exec.Command("xattr", "-p", "com.google.drivefs.ignorecontent", path).Output()
	if err != nil {
		// xattr returns exit 1 when attribute not found — not a real error
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "1", nil
}

func removeIgnoreContent(path string) error {
	return exec.Command("xattr", "-d", "com.google.drivefs.ignorecontent", path).Run()
}
