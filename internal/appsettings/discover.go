package appsettings

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DiscoverApp searches for an installed macOS application matching name and
// synthesizes a backup entry for it. Useful when the user types an app by its
// display name (e.g. "Moom Classic") instead of a manifest token: as long as
// the .app bundle is on disk we can read its bundle identifier and probe the
// standard Library locations to build a meaningful backup set.
//
// Returns nil when the app bundle can't be found or no standard path exists.
// The returned AppEntry uses name as its Token so the archive groups under a
// predictable directory.
func DiscoverApp(home, name string) *AppEntry {
	appPath := locateAppBundle(home, name)
	if appPath == "" {
		return nil
	}
	bundleID := readBundleID(appPath)
	if bundleID == "" {
		return nil
	}

	library := filepath.Join(home, "Library")
	appBase := strings.TrimSuffix(filepath.Base(appPath), ".app")

	// Probe a small list of conventional locations. Only keep those that
	// actually exist so the summary line for this app isn't dominated by
	// "missing" counts.
	candidates := []PathEntry{
		{Type: "pref", Path: filepath.Join("Preferences", bundleID+".plist")},
		{Type: "support", Path: filepath.Join("Application Support", appBase)},
		{Type: "support", Path: filepath.Join("Application Support", bundleID)},
		{Type: "container", Path: filepath.Join("Containers", bundleID)},
	}
	var paths []PathEntry
	seen := make(map[string]bool)
	for _, c := range candidates {
		if seen[c.Path] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(library, c.Path)); err != nil {
			continue
		}
		seen[c.Path] = true
		paths = append(paths, c)
	}

	// Group Containers are prefixed by the developer's team identifier, so
	// glob for any directory ending with ".<bundleID>" or equal to bundleID.
	if dents, err := os.ReadDir(filepath.Join(library, "Group Containers")); err == nil {
		for _, d := range dents {
			dn := d.Name()
			if dn == bundleID || strings.HasSuffix(dn, "."+bundleID) {
				rel := filepath.Join("Group Containers", dn)
				if seen[rel] {
					continue
				}
				seen[rel] = true
				paths = append(paths, PathEntry{Type: "group", Path: rel})
			}
		}
	}

	if len(paths) == 0 {
		return nil
	}
	return &AppEntry{Token: name, Paths: paths}
}

// locateAppBundle returns the first existing .app bundle that matches name,
// or the empty string if none is found. Accepts either "Moom Classic" or
// "Moom Classic.app".
func locateAppBundle(home, name string) string {
	candidates := appBundleCandidates(home, name)
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return ""
}

func appBundleCandidates(home, name string) []string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return nil
	}
	withExt := clean
	if !strings.HasSuffix(strings.ToLower(withExt), ".app") {
		withExt = clean + ".app"
	}
	roots := []string{
		"/Applications",
		"/Applications/Utilities",
		filepath.Join(home, "Applications"),
	}
	var out []string
	for _, r := range roots {
		out = append(out, filepath.Join(r, withExt))
	}
	return out
}

// readBundleID returns the CFBundleIdentifier from an .app bundle, or the
// empty string on any failure. Uses /usr/bin/defaults so it works on every
// macOS install without pulling in a plist parser dependency.
func readBundleID(appPath string) string {
	plistBase := filepath.Join(appPath, "Contents", "Info")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := osexec.CommandContext(ctx, "/usr/bin/defaults", "read", plistBase, "CFBundleIdentifier").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
