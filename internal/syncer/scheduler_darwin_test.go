//go:build darwin

package syncer

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

func TestLaunchdPrintTarget(t *testing.T) {
	got := launchdPrintTarget(501, SchedulerKindPush.LaunchdLabel())
	want := "gui/501/com.dotfiles.sync"
	if got != want {
		t.Fatalf("launchdPrintTarget = %q, want %q", got, want)
	}
}

func TestLaunchdStateFromPrintStatus(t *testing.T) {
	cases := []struct {
		name        string
		plistExists bool
		printOK     bool
		want        SchedulerState
	}{
		{"missing plist", false, true, SchedulerNotInstalled},
		{"loaded", true, true, SchedulerRunning},
		{"not loaded", true, false, SchedulerStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := launchdStateFromPrintStatus(tc.plistExists, tc.printOK)
			if got != tc.want {
				t.Fatalf("state = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSchedulerInstallKind_PlistPathFieldsRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "paths & <and>")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStub(t, filepath.Join(binDir, "dot"), "#!/bin/sh\nexit 0\n")
	writeStub(t, filepath.Join(binDir, "launchctl"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir)

	localPath := filepath.Join(root, "workspace")
	logFile := filepath.Join(localPath, "logs", "sync.log")
	plistPath := filepath.Join(root, "Library", "LaunchAgents", "com.dotfiles.sync.plist")
	scheduler := NewScheduler(
		exec.NewRunner(false, slog.New(slog.NewTextHandler(io.Discard, nil))),
		&Paths{LaunchdPlist: plistPath},
		&Config{Home: root, LocalPath: localPath, LogFile: logFile, Interval: 60},
		template.NewEngine(),
	)

	if err := scheduler.InstallKind(context.Background(), SchedulerKindPush); err != nil {
		t.Fatalf("InstallKind: %v", err)
	}
	body, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read persisted plist: %v", err)
	}
	var plist plistProgramArguments
	if err := xml.Unmarshal(body, &plist); err != nil {
		t.Fatalf("persisted plist must parse: %v\n%s", err, body)
	}
}
