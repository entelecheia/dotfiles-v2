package aitooling

import (
	"context"
	"crypto/sha1" // Git object IDs, not a security signature.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// pluginDigest records code and configuration, including symlink targets.
// Native dependency caches are owned separately and are not overwritten here.
func pluginDigest(root string) (string, error) {
	var names []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel != "." && (d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, rel := range names {
		p := filepath.Join(root, rel)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		var b []byte
		if st.Mode()&os.ModeSymlink != 0 {
			v, err := os.Readlink(p)
			if err != nil {
				return "", err
			}
			b = []byte(v)
		} else {
			b, err = os.ReadFile(p)
			if err != nil {
				return "", err
			}
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", rel, st.Mode())
		_, _ = h.Write(b)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (e *Engine) pluginIntegrity(ctx context.Context, key, agent, selector, path string) bool {
	if path == "" {
		return false
	}
	digest, err := pluginDigest(path)
	if err != nil {
		return false
	}
	if prior, ok := e.receipts[key]; ok && prior.Integrity != "" {
		return digest == prior.Integrity
	}
	// First adoption is verified against the exact native installation commit,
	// never against a moving HEAD or only the plugin's version string.
	if agent != "claude" {
		return false
	}
	var installed struct {
		Plugins map[string][]struct{ Scope, InstallPath, GitCommitSHA string }
	}
	b, err := os.ReadFile(filepath.Join(e.profile(agent), "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(b, &installed) != nil {
		return false
	}
	commit := ""
	for _, r := range installed.Plugins[selector] {
		if r.Scope == "user" && r.InstallPath == path {
			commit = r.GitCommitSHA
		}
	}
	if len(commit) != 40 {
		return false
	}
	if _, err = hex.DecodeString(commit); err != nil {
		return false
	}
	pieces := strings.Split(selector, "@")
	if len(pieces) != 2 {
		return false
	}
	market := filepath.Join(e.profile(agent), "plugins", "marketplaces", pieces[1])
	var manifest struct {
		Plugins []struct {
			Name   string
			Source json.RawMessage
		}
	}
	b, err = os.ReadFile(filepath.Join(market, ".claude-plugin", "marketplace.json"))
	if err != nil || json.Unmarshal(b, &manifest) != nil {
		return false
	}
	source := ""
	for _, p := range manifest.Plugins {
		if p.Name == pieces[0] {
			_ = json.Unmarshal(p.Source, &source)
		}
	}
	if source == "" || filepath.IsAbs(source) {
		return false
	}
	source = filepath.Clean(source)
	if source == ".." || strings.HasPrefix(source, "../") {
		return false
	}
	git := e.find("git")
	if git == "" {
		return false
	}
	out, err := e.exec(ctx, command{Path: git, Args: []string{"-C", market, "ls-tree", "-r", "-z", commit, "--", source}, Env: e.environment()})
	if err != nil {
		return false
	}
	files := 0
	expected := map[string]bool{}
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			continue
		}
		head, name, ok := strings.Cut(line, "\t")
		if !ok {
			return false
		}
		fields := strings.Fields(head)
		if len(fields) != 3 || fields[1] != "blob" {
			return false
		}
		rel := name
		if source != "." {
			rel = strings.TrimPrefix(name, source+"/")
			if rel == name {
				return false
			}
		}
		expected[rel] = true
		p := filepath.Join(path, rel)
		var content []byte
		if fields[0] == "120000" {
			v, err := os.Readlink(p)
			if err != nil {
				return false
			}
			content = []byte(v)
		} else {
			content, err = os.ReadFile(p)
			if err != nil {
				return false
			}
		}
		h := sha1.New()
		_, _ = fmt.Fprintf(h, "blob %d%c", len(content), 0)
		_, _ = h.Write(content)
		if hex.EncodeToString(h.Sum(nil)) != fields[2] {
			return false
		}
		files++
	}
	if files == 0 {
		return false
	}
	// Unknown extra source files are user changes too; dependencies and native
	// Git metadata alone are exempted from this exact-source comparison.
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != path && (d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(path, p)
		if !expected[rel] {
			return fmt.Errorf("untracked plugin file")
		}
		return nil
	})
	if err != nil {
		return false
	}
	e.receipts[key] = receipt{Provider: "native-plugin", Path: path, Integrity: digest}
	return true
}
