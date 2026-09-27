package aitooling

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Verify first adoption against the exact official npm distribution, including
// its registry SHA-512 integrity. Nothing is extracted or executed.
func (e *Engine) ponyCacheClean(ctx context.Context, path, version string) bool {
	if prior, ok := e.receipts["ponytail/opencode"]; ok && prior.Integrity != "" {
		digest, err := pluginDigest(path)
		return err == nil && digest == prior.Integrity
	}
	if !stableVersion.MatchString(version) {
		return false
	}
	data, err := e.get(ctx, "https://registry.npmjs.org/@dietrichgebert/ponytail/"+version)
	if err != nil {
		return false
	}
	var metadata struct {
		Dist struct {
			Integrity string `json:"integrity"`
		}
	}
	if json.Unmarshal(data, &metadata) != nil || !strings.HasPrefix(metadata.Dist.Integrity, "sha512-") {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(metadata.Dist.Integrity, "sha512-"))
	if err != nil {
		return false
	}
	archive, err := e.get(ctx, "https://registry.npmjs.org/@dietrichgebert/ponytail/-/ponytail-"+version+".tgz")
	if err != nil {
		return false
	}
	sum := sha512.Sum512(archive)
	if !bytes.Equal(sum[:], expected) {
		return false
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return false
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(io.LimitReader(gz, 32<<20))
	files := 0
	expectedFiles := map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return false
		}
		if !strings.HasPrefix(header.Name, "package/") {
			return false
		}
		rel := strings.TrimPrefix(header.Name, "package/")
		if filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "../") {
			return false
		}
		b, err := io.ReadAll(io.LimitReader(tr, 4<<20))
		if err != nil || int64(len(b)) != header.Size {
			return false
		}
		expectedFiles[rel] = true
		if st, err := os.Lstat(filepath.Join(path, rel)); err != nil || !st.Mode().IsRegular() {
			return false
		}
		local, err := os.ReadFile(filepath.Join(path, rel))
		if err != nil || !bytes.Equal(local, b) {
			return false
		}
		files++
	}
	if files == 0 {
		return false
	}
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
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
		if !expectedFiles[rel] {
			return errors.New("extra native package file")
		}
		return nil
	}) == nil
}
