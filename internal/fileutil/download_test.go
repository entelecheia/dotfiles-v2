package fileutil

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

type archiveTestEntry struct {
	name     string
	content  string
	typeflag byte
	linkname string
	mode     int64
}

func makeTarGzEntries(t *testing.T, entries []archiveTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := entry.mode
		if mode == 0 {
			mode = 0755
		}
		header := &tar.Header{Name: entry.name, Mode: mode, Typeflag: typeflag, Linkname: entry.linkname}
		if typeflag == tar.TypeReg || typeflag == tar.TypeRegA {
			header.Size = int64(len(entry.content))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
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

func makeZip(t *testing.T, entries []archiveTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		mode := os.FileMode(entry.mode)
		if mode == 0 {
			mode = 0755
		}
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(mode)
		if entry.typeflag == tar.TypeSymlink {
			header.SetMode(os.ModeSymlink | 0755)
			entry.content = entry.linkname
		}
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

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

func TestExtractTarGzSecurityMatrix(t *testing.T) {
	t.Run("stripped siblings stay rooted", func(t *testing.T) {
		dest := t.TempDir()
		archive := makeTarGzEntries(t, []archiveTestEntry{
			{name: "prefix/dir/file", content: "file"},
			{name: "prefix/sibling", content: "sibling"},
		})
		if err := ExtractTarGz(bytes.NewReader(archive), dest, 1); err != nil {
			t.Fatalf("ExtractTarGz: %v", err)
		}
		for _, path := range []string{"dir/file", "sibling"} {
			if _, err := os.Stat(filepath.Join(dest, path)); err != nil {
				t.Fatalf("expected rooted %s: %v", path, err)
			}
		}
	})

	for _, tc := range []struct {
		name  string
		entry archiveTestEntry
		strip int
	}{
		{name: "upward", entry: archiveTestEntry{name: "../escape", content: "no"}},
		{name: "absolute", entry: archiveTestEntry{name: "/escape", content: "no"}},
		{name: "post strip upward", entry: archiveTestEntry{name: "prefix/../escape", content: "no"}, strip: 1},
		{name: "hard link", entry: archiveTestEntry{name: "hard", typeflag: tar.TypeLink, linkname: "target"}},
		{name: "fifo", entry: archiveTestEntry{name: "fifo", typeflag: tar.TypeFifo}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			outside := filepath.Join(filepath.Dir(dest), "escape")
			if err := ExtractTarGz(bytes.NewReader(makeTarGzEntries(t, []archiveTestEntry{tc.entry})), dest, tc.strip); err == nil {
				t.Fatal("ExtractTarGz succeeded for unsafe entry")
			}
			if _, err := os.Lstat(outside); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("outside target was touched: %v", err)
			}
		})
	}

	t.Run("relative link is resolved from final parent", func(t *testing.T) {
		dest := t.TempDir()
		archive := makeTarGzEntries(t, []archiveTestEntry{
			{name: "prefix/lib/target", content: "ok"},
			{name: "prefix/bin/tool", typeflag: tar.TypeSymlink, linkname: "../lib/target"},
		})
		if err := ExtractTarGz(bytes.NewReader(archive), dest, 1); err != nil {
			t.Fatalf("ExtractTarGz: %v", err)
		}
		if target, err := os.Readlink(filepath.Join(dest, "bin", "tool")); err != nil || target != "../lib/target" {
			t.Fatalf("link = %q, %v", target, err)
		}
	})
}

func TestExtractZipSecurityMatrix(t *testing.T) {
	t.Run("regular content remains rooted", func(t *testing.T) {
		dest := t.TempDir()
		archive := makeZip(t, []archiveTestEntry{{name: "dir/file", content: "ok"}})
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatal(err)
		}
		if err := ExtractZip(reader, dest); err != nil {
			t.Fatalf("ExtractZip: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(dest, "dir", "file")); err != nil || string(data) != "ok" {
			t.Fatalf("rooted zip output = %q, %v", data, err)
		}
	})

	for _, name := range []string{"../escape", "/escape", "C:/escape"} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			archive := makeZip(t, []archiveTestEntry{{name: name, content: "no"}})
			reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
			if err != nil {
				t.Fatal(err)
			}
			if err := ExtractZip(reader, dest); err == nil {
				t.Fatal("ExtractZip succeeded for unsafe name")
			}
		})
	}

	t.Run("safe and escaping symlinks", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			link string
			want bool
		}{
			{name: "safe", link: "../lib/target", want: true},
			{name: "escape", link: "../../escape"},
			{name: "absolute", link: "/escape"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dest := t.TempDir()
				archive := makeZip(t, []archiveTestEntry{{name: "lib/target", content: "ok"}, {name: "bin/tool", typeflag: tar.TypeSymlink, linkname: tc.link}})
				reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
				if err != nil {
					t.Fatal(err)
				}
				err = ExtractZip(reader, dest)
				if tc.want && err != nil {
					t.Fatalf("ExtractZip: %v", err)
				}
				if !tc.want && err == nil {
					t.Fatal("ExtractZip accepted escaping symlink")
				}
			})
		}
	})
}

func TestExtractTarGz_ArchiveLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  int64
		max  int64
		want bool
	}{
		{"entry exact", 8388608, DefaultArchiveLimits.MaxEntryBytes, true},
		{"entry boundary plus one", 8388609, DefaultArchiveLimits.MaxEntryBytes, false},
		{"total exact", 402653184, DefaultArchiveLimits.MaxTotalExtractedBytes, true},
		{"total boundary plus one", 402653185, DefaultArchiveLimits.MaxTotalExtractedBytes, false},
		{"ratio exact", 8, DefaultArchiveLimits.MaxExpansionRatio, true},
		{"ratio boundary plus one", 9, DefaultArchiveLimits.MaxExpansionRatio, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinArchiveLimit(tc.got, tc.max); got != tc.want {
				t.Fatalf("withinArchiveLimit(%d, %d) = %v, want %v", tc.got, tc.max, got, tc.want)
			}
		})
	}

	limits := DefaultArchiveLimits
	limits.MaxEntryBytes = 4
	limits.MaxTotalExtractedBytes = 6
	limits.MaxEntries = 1
	limits.MaxExpansionRatio = 8
	limits.MaxCompressedBytes = 1024
	for _, tc := range []struct {
		name    string
		entries []archiveTestEntry
	}{
		{name: "entry", entries: []archiveTestEntry{{name: "large", content: "12345"}}},
		{name: "total", entries: []archiveTestEntry{{name: "one", content: "1234"}, {name: "two", content: "1234"}}},
		{name: "count", entries: []archiveTestEntry{{name: "one", content: "1"}, {name: "two", content: "1"}}},
	} {
		t.Run("wired "+tc.name, func(t *testing.T) {
			if err := extractTarGzWithLimits(bytes.NewReader(makeTarGzEntries(t, tc.entries)), t.TempDir(), 0, limits); err == nil {
				t.Fatal("expected archive limit error")
			}
		})
	}
}

func TestExtractZip_ArchiveLimits(t *testing.T) {
	if DefaultArchiveLimits.MaxEntries != 4096 || DefaultArchiveLimits.MaxCompressedBytes != 201326592 {
		t.Fatalf("unexpected production limits: %+v", DefaultArchiveLimits)
	}
	limits := DefaultArchiveLimits
	limits.MaxEntryBytes = 4
	limits.MaxTotalExtractedBytes = 4
	limits.MaxEntries = 1
	limits.MaxExpansionRatio = 8
	limits.MaxCompressedBytes = 1024
	archive := makeZip(t, []archiveTestEntry{{name: "large", content: "12345"}})
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if err := extractZipWithLimits(reader, t.TempDir(), limits); err == nil {
		t.Fatal("expected zip archive limit error")
	}
}

func TestDownloadArchive_CompressedLimit(t *testing.T) {
	served := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.Header().Set("Content-Length", "201326593")
		_, _ = w.Write([]byte("too large"))
	}))
	defer srv.Close()
	if err := DownloadAndExtractTarGz(context.Background(), exec.NewRunner(false, quietLogger()), srv.URL, t.TempDir(), 0); err == nil {
		t.Fatal("expected compressed input refusal")
	}
	if !served {
		t.Fatal("server was not reached")
	}
}

func TestArchiveLimits_MeasuredSupportedArtifacts(t *testing.T) {
	artifacts := map[string]struct{ compressed, extracted, entries int64 }{
		"FiraCode":                 {compressed: 28602426, extracted: 164000000, entries: 500},
		"JetBrainsMono":            {compressed: 133975870, extracted: 243185440, entries: 98},
		"Hack":                     {compressed: 18694868, extracted: 145000000, entries: 700},
		"dot-darwin-amd64-v2.69.2": {compressed: 5766495, extracted: 14817700, entries: 3},
		"dot-darwin-arm64-v2.69.2": {compressed: 5766495, extracted: 14817700, entries: 3},
		"dot-linux-amd64-v2.69.2":  {compressed: 5766495, extracted: 14817700, entries: 3},
		"dot-linux-arm64-v2.69.2":  {compressed: 5766495, extracted: 14817700, entries: 3},
		"oh-my-zsh-146461f":        {compressed: 3340957, extracted: 7126554, entries: 1492},
	}
	for name, artifact := range artifacts {
		t.Run(name, func(t *testing.T) {
			if err := DefaultArchiveLimits.ValidateMeasured(artifact.compressed, artifact.extracted, artifact.entries); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExtractRealOhMyZsh(t *testing.T) {
	const fixture = "testdata/ohmyzsh-146461f.tar.gz"
	const fixtureSHA256 = "23fd754895813e0f81293983c7600b7ac87faf69c0734514f6bc42f2f64b7e99"

	archive, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != fixtureSHA256 {
		t.Fatalf("fixture sha256 = %s, want %s", got, fixtureSHA256)
	}
	dest := t.TempDir()
	if err := ExtractTarGz(bytes.NewReader(archive), dest, 1); err != nil {
		t.Fatalf("ExtractTarGz fixture: %v", err)
	}
	for _, required := range []string{"oh-my-zsh.sh", "lib", "plugins", "themes", "custom"} {
		if _, err := os.Stat(filepath.Join(dest, required)); err != nil {
			t.Fatalf("fixture is missing %s: %v", required, err)
		}
	}
}
