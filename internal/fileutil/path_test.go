package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExists(t *testing.T) {
	tmp := t.TempDir()
	if !Exists(tmp) {
		t.Errorf("Exists(%q) = false, want true", tmp)
	}
	if Exists(filepath.Join(tmp, "missing")) {
		t.Error("Exists on missing path returned true")
	}
}

func TestIsDir(t *testing.T) {
	tmp := t.TempDir()
	if !IsDir(tmp) {
		t.Errorf("IsDir(%q) = false, want true", tmp)
	}

	f := filepath.Join(tmp, "file")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsDir(f) {
		t.Errorf("IsDir(%q) = true on regular file", f)
	}
	if IsDir(filepath.Join(tmp, "nope")) {
		t.Error("IsDir on missing path returned true")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	tests := []struct {
		in, want string
	}{
		{"~/foo", filepath.Join(home, "foo")},
		{"~/", home},
		{"/absolute", "/absolute"},
		{"relative/path", "relative/path"},
		{"~nouser/x", "~nouser/x"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ExpandHome(tt.in); got != tt.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
