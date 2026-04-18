package cli

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/rclone"
	"github.com/entelecheia/dotfiles-v2/internal/rsync"
)

func TestClonePreflight_RclonMissingReportsViaPrinter(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &out}

	// rclone may or may not be on PATH in CI; either way the filter file path below
	// is guaranteed missing, so at least one of preflight's two gates fires.
	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	cfg := &rclone.Config{FilterFile: "/definitely/does/not/exist"}
	ok := clonePreflight(p, cfg, runner)

	// Whether CommandExists("rclone") returns true or false on the host, the filter file
	// path is guaranteed missing, so at least one of the two user-visible messages fires.
	if ok {
		t.Fatalf("preflight unexpectedly succeeded: out=%q", out.String())
	}
	got := out.String()
	switch {
	case strings.Contains(got, "rclone is not installed"):
	case strings.Contains(got, "Filter file not found"):
	default:
		t.Errorf("expected rclone/filter warning, got %q", got)
	}
}

func TestSyncPreflight_RemoteHostEmptyReportsViaPrinter(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &out}

	runner := exec.NewRunner(false, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	cfg := &rsync.Config{RemoteHost: "", ExtensionsFile: "/definitely/does/not/exist"}

	if ok := syncPreflight(p, cfg, runner); ok {
		t.Fatalf("preflight unexpectedly succeeded: out=%q", out.String())
	}
	got := out.String()
	switch {
	case strings.Contains(got, "rsync is not installed"):
	case strings.Contains(got, "Remote host not configured"):
	case strings.Contains(got, "Extensions file not found"):
	default:
		t.Errorf("expected rsync/host/extensions warning, got %q", got)
	}
}
