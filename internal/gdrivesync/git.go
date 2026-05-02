package gdrivesync

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitTrackedRelPaths returns relpaths tracked by Git under root. It is a
// best-effort guard: non-Git workspaces, missing git, or submodule traversal
// failures all return an empty set so gdrive-sync remains usable outside Git.
func gitTrackedRelPaths(root string) map[string]bool {
	root = strings.TrimRight(root, "/")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--recurse-submodules").Output()
	if err != nil {
		out, err = exec.Command("git", "-C", root, "ls-files", "-z").Output()
		if err != nil {
			return map[string]bool{}
		}
	}
	return parseGitLsFiles(out)
}

func parseGitLsFiles(out []byte) map[string]bool {
	paths := map[string]bool{}
	for _, raw := range bytes.Split(out, []byte{0}) {
		rel := normalizeRel(string(raw))
		if rel == "" {
			continue
		}
		paths[filepath.ToSlash(rel)] = true
	}
	return paths
}
