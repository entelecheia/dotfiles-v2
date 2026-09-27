package aipolicy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// projectSettingsFiles conservatively retains legacy/ancestor settings, then
// includes Claude's main-checkout local file. Claude >=2.1.211 shares that local
// file with linked worktrees, except home-root/foreign-owner/Windows fallbacks.
// https://code.claude.com/docs/en/settings#where-claude-code-keeps-the-local-file-in-a-git-repository
func projectSettingsFiles(agent, cwd string) ([]string, error) {
	files := []string{}
	if cwd == "" {
		return files, nil
	}
	if !filepath.IsAbs(cwd) {
		return nil, fmt.Errorf("request cwd must be absolute")
	}
	if agent != "claude" && agent != "codex" {
		return files, nil
	}
	dir := filepath.Clean(cwd)
	repo := ""
	for depth := 0; depth < 64; depth++ {
		if agent == "claude" {
			files = append(files, filepath.Join(dir, ".claude", "settings.json"), filepath.Join(dir, ".claude", "settings.local.json"))
		} else {
			files = append(files, filepath.Join(dir, ".codex", "config.toml"))
		}
		if repo == "" {
			_, err := os.Lstat(filepath.Join(dir, ".git"))
			if err == nil {
				repo = dir
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("cannot inspect project git metadata")
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if depth == 63 {
			return nil, fmt.Errorf("project settings ancestry exceeds inspection limit")
		}
		dir = parent
	}
	if agent != "claude" || repo == "" || runtime.GOOS == "windows" {
		return files, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if filepath.Clean(repo) == filepath.Clean(home) {
		return files, nil
	}
	owned, err := ownedSettingsRoot(repo)
	if err != nil {
		return nil, err
	}
	if !owned {
		return files, nil
	}
	main, err := mainCheckout(repo)
	if err != nil {
		return nil, err
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err == nil {
		home = canonicalHome
	}
	if filepath.Clean(main) == filepath.Clean(home) {
		return files, nil
	}
	owned, err = ownedSettingsRoot(main)
	if err != nil {
		return nil, err
	}
	if !owned {
		return files, nil
	}

	local := filepath.Join(main, ".claude", "settings.local.json")
	for _, file := range files {
		if file == local {
			return files, nil
		}
	}
	return append(files, local), nil
}

// mainCheckout uses at most two bounded read-only git commands. core.worktree
// must precede common-dir parent inference: absorbed submodules keep their git
// directory under the superproject, not under the main checkout.
func mainCheckout(repo string) (string, error) {
	result, err := settingsGit(repo, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("cannot resolve native main-checkout settings from git metadata")
	}
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 2 || !filepath.IsAbs(lines[0]) || !filepath.IsAbs(lines[1]) {
		return "", fmt.Errorf("invalid native project git metadata")
	}
	common := filepath.Clean(lines[1])
	worktree, err := settingsGit(repo, "config", "--no-includes", "--file", filepath.Join(common, "config"), "--get", "core.worktree")
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 1 {
			return "", fmt.Errorf("cannot resolve native main-checkout core.worktree")
		}
		worktree = ""
	}
	main := ""
	if worktree = strings.TrimSpace(worktree); worktree != "" {
		if strings.ContainsAny(worktree, "\r\n") {
			return "", fmt.Errorf("invalid main-checkout path")
		}
		main = worktree
		if !filepath.IsAbs(main) {
			main = filepath.Join(common, main)
		}
	} else if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	} else {
		return "", fmt.Errorf("main checkout is unverified; nonstandard git metadata has no core.worktree")
	}
	resolved, err := filepath.EvalSymlinks(main)
	if err != nil {
		return "", fmt.Errorf("main checkout is unreachable; native settings cannot be verified")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("main checkout is unavailable; native settings cannot be verified")
	}
	return resolved, nil
}
func ownedSettingsRoot(root string) (bool, error) {
	for _, entry := range []string{root, filepath.Join(root, ".git"), filepath.Join(root, ".claude")} {
		info, err := os.Stat(entry)
		if os.IsNotExist(err) && entry == filepath.Join(root, ".claude") {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("cannot verify main-checkout settings location")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return false, fmt.Errorf("cannot verify main-checkout ownership")
		}
		if stat.Uid != uint32(os.Getuid()) {
			return false, nil
		}
	}
	return true, nil
}
func settingsGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output := &boundedProbeOutput{}
	cmd.Stdout = output
	err := cmd.Run()
	return output.String(), err
}
