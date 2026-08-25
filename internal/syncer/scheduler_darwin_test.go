//go:build darwin

package syncer

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	if len(plist.ProgramArguments) == 0 || plist.ProgramArguments[0] != filepath.Join(binDir, "dot") {
		t.Fatalf("ProgramArguments[0] = %q, want %q", plist.ProgramArguments, filepath.Join(binDir, "dot"))
	}
	if got := matchingHomeArguments(plist.ProgramArguments); len(got) != 1 || got[0] != "--home="+root {
		t.Fatalf("home arguments = %q, want %q", got, "--home="+root)
	}
	paths := plistStandardPaths(t, body)
	for _, key := range []string{"StandardOutPath", "StandardErrorPath"} {
		if paths[key] != logFile {
			t.Errorf("%s = %q, want %q", key, paths[key], logFile)
		}
	}
}

func TestSchedulerInstallKind_RejectsUnrepresentablePlistPathBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		field      string
		value      string
		executable bool
	}{
		{name: "invalid executable UTF-8", field: "executable", value: string([]byte("/tmp/dot-\xff")), executable: true},
		{name: "invalid executable control", field: "executable", value: "/tmp/dot-\x01", executable: true},
		{name: "invalid log UTF-8", field: "log file", value: string([]byte("/tmp/log-\xff"))},
		{name: "invalid log control", field: "log file", value: "/tmp/log-\x01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			plistPath := filepath.Join(root, "Library", "LaunchAgents", "com.dotfiles.sync.plist")
			if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
				t.Fatal(err)
			}
			const seeded = "seeded shared plist"
			if err := os.WriteFile(plistPath, []byte(seeded), 0o644); err != nil {
				t.Fatal(err)
			}
			var actions bytes.Buffer
			runner := exec.NewRunner(true, slog.New(slog.NewTextHandler(&actions, &slog.HandlerOptions{Level: slog.LevelDebug})))
			scheduler := NewScheduler(runner, &Paths{LaunchdPlist: plistPath}, &Config{
				Home: root, LocalPath: filepath.Join(root, "workspace"), LogFile: filepath.Join(root, "sync.log"), Interval: 60,
			}, template.NewEngine())
			if tc.executable {
				previous := schedulerLookPath
				schedulerLookPath = func(string) (string, error) { return tc.value, nil }
				t.Cleanup(func() { schedulerLookPath = previous })
			} else {
				scheduler.Config.LogFile = tc.value
				previous := schedulerLookPath
				schedulerLookPath = func(string) (string, error) { return "/tmp/dot", nil }
				t.Cleanup(func() { schedulerLookPath = previous })
			}

			err := scheduler.InstallKind(context.Background(), SchedulerKindPush)
			if err == nil {
				t.Fatal("unrepresentable plist path unexpectedly reached mutation")
			}
			for _, want := range []string{tc.field, fmt.Sprintf("%q", tc.value), plistPath, "XML 1.0", "left untouched", "dot sync setup"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			if got, readErr := os.ReadFile(plistPath); readErr != nil || string(got) != seeded {
				t.Fatalf("rejection changed seeded plist: %q, %v", got, readErr)
			}
			if temps, globErr := filepath.Glob(filepath.Join(filepath.Dir(plistPath), ".dot-write-*")); globErr != nil || len(temps) != 0 {
				t.Fatalf("rejection left temporary plist files: %v, %v", temps, globErr)
			}
			if actions.Len() != 0 {
				t.Fatalf("rejection performed runner or launchctl action:\n%s", actions.String())
			}
		})
	}
}

func plistStandardPaths(t *testing.T, body []byte) map[string]string {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	values := map[string]string{}
	var key, text string
	var inKey, inString bool
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return values
		}
		if err != nil {
			t.Fatalf("decode plist: %v", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			text = ""
			inKey = token.Name.Local == "key"
			inString = token.Name.Local == "string"
		case xml.CharData:
			if inKey || inString {
				text += string(token)
			}
		case xml.EndElement:
			switch token.Name.Local {
			case "key":
				key, inKey = text, false
			case "string":
				if key == "StandardOutPath" || key == "StandardErrorPath" {
					values[key] = text
				}
				inString = false
			}
		}
	}
}
