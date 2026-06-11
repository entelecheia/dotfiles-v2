package fileutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// makeTarGz builds an in-memory .tar.gz with the given name → content
// entries, all regular files mode 0755.
func makeTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadFile_ReturnsSHA256(t *testing.T) {
	content := []byte("release archive bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "asset.tar.gz")
	runner := exec.NewRunner(false, quietLogger())

	got, err := DownloadFile(context.Background(), runner, srv.URL, dest)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}

	sum := sha256.Sum256(content)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("sha256 = %s, want %s", got, want)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Error("downloaded content differs from served content")
	}
}

func TestDownloadFile_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "asset.tar.gz")
	runner := exec.NewRunner(false, quietLogger())

	if _, err := DownloadFile(context.Background(), runner, srv.URL, dest); err == nil {
		t.Fatal("expected error on HTTP 404")
	}
}

func TestDownloadFile_DryRun(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "asset.tar.gz")
	runner := exec.NewRunner(true, quietLogger())

	sum, err := DownloadFile(context.Background(), runner, "http://invalid.invalid/x", dest)
	if err != nil {
		t.Fatalf("dry-run should not error: %v", err)
	}
	if sum != "" {
		t.Errorf("dry-run sum = %q, want empty", sum)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("dry-run must not create the destination file")
	}
}

func TestExtractTarGz(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"dot": "#!/bin/sh\necho dot\n"})
	dest := t.TempDir()

	if err := ExtractTarGz(bytes.NewReader(archive), dest, 0); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	target := filepath.Join(dest, "dot")
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
	data, _ := os.ReadFile(target)
	if string(data) != "#!/bin/sh\necho dot\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestExtractTarGz_StripComponents(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"prefix-1.0/bin/tool": "binary"})
	dest := t.TempDir()

	if err := ExtractTarGz(bytes.NewReader(archive), dest, 1); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "bin", "tool")); err != nil {
		t.Errorf("stripped path missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "prefix-1.0")); !os.IsNotExist(err) {
		t.Error("unstripped prefix directory should not exist")
	}
}
