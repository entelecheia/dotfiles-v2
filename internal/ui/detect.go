package ui

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// detectRunner returns a non-dry-run runner used by the detect* helpers below.
// Detection commands are read-only; they always run regardless of caller intent.
func detectRunner() *exec.Runner { return exec.NewRunner(false, slog.Default()) }

// detectGitConfig reads a value from git config (global).
func detectGitConfig(key string) string {
	res, err := detectRunner().RunQuery(context.Background(), "git", "config", "--global", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// detectGithubUser attempts to get the current user from gh CLI.
func detectGithubUser() string {
	if _, err := osexec.LookPath("gh"); err != nil {
		return ""
	}
	res, err := detectRunner().RunQuery(context.Background(), "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// detectTimezone reads the system timezone.
func detectTimezone() string {
	// Try /etc/localtime symlink (Linux + macOS)
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		// e.g., /var/db/timezone/zoneinfo/Asia/Seoul or /usr/share/zoneinfo/Asia/Seoul
		for _, prefix := range []string{"/var/db/timezone/zoneinfo/", "/usr/share/zoneinfo/"} {
			if strings.HasPrefix(target, prefix) {
				return strings.TrimPrefix(target, prefix)
			}
		}
		// Fallback: take last 2 segments
		parts := strings.Split(target, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
	}
	// Try $TZ env
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	// Try /etc/timezone (Debian/Ubuntu)
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

// detectWorkspacePath checks common workspace locations.
func detectWorkspacePath() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "workspace"),
		filepath.Join(home, "Workspace"),
		filepath.Join(home, "work"),
	}
	for _, c := range candidates {
		if fi, err := os.Lstat(c); err == nil && fi.IsDir() || fi != nil && fi.Mode()&os.ModeSymlink != 0 {
			// Return with ~ prefix for portability
			return "~/" + filepath.Base(c)
		}
	}
	return ""
}

// detectGoogleDrivePath finds a Google Drive mount on macOS.
func detectGoogleDrivePath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, _ := os.UserHomeDir()
	// Check common Drive paths
	if entries, err := os.ReadDir(home); err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, "My Drive") || strings.Contains(name, "GoogleDrive") {
				return filepath.Join(home, name)
			}
		}
	}
	// Check /Volumes for mounted Drives
	if entries, err := os.ReadDir("/Volumes"); err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "GoogleDrive") || strings.Contains(e.Name(), "Google Drive") {
				return filepath.Join("/Volumes", e.Name())
			}
		}
	}
	return ""
}

// detectSSHKeys finds existing SSH key names in ~/.ssh/.
func detectSSHKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	var keys []string
	seen := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".pub") || strings.HasSuffix(name, ".age") {
			continue
		}
		if name == "config" || name == "known_hosts" || name == "authorized_keys" ||
			name == "authorized_age_keys" || name == "environment" || name == "agent" ||
			strings.HasPrefix(name, "config.") || strings.HasPrefix(name, "known_hosts.") ||
			strings.HasPrefix(name, "age_key") {
			continue
		}
		if !fileExists(filepath.Join(sshDir, name+".pub")) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			keys = append(keys, name)
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		iEd := strings.Contains(keys[i], "ed25519")
		jEd := strings.Contains(keys[j], "ed25519")
		if iEd != jEd {
			return iEd
		}
		return keys[i] < keys[j]
	})

	return keys
}

// detectAgeKeys finds existing age identity files in ~/.ssh/.
func detectAgeKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	var keys []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".pub") {
			continue
		}
		if strings.HasPrefix(name, "age_key") && !strings.HasSuffix(name, ".pub") {
			keys = append(keys, filepath.Join("~/.ssh", name))
		}
	}
	sort.Strings(keys)
	return keys
}

// readAgePublicKey attempts to read the .pub file corresponding to an age identity.
func readAgePublicKey(identityPath string) string {
	expanded := expandHome(identityPath)
	pubPath := expanded + ".pub"
	f, err := os.Open(pubPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "age1") {
			return line
		}
	}
	return ""
}

// readSymlinkTarget returns the target of a symlink, or "" if not a symlink.
func readSymlinkTarget(path string) string {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}
