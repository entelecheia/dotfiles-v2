//go:build !darwin

package syncer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

func TestSchedulerLifecycle_ProfiledUnits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		kind    SchedulerKind
		action  string
		want    string
	}{
		{name: "default push install", profile: DefaultProfile, kind: SchedulerKindPush, action: "install", want: "--user enable --now dotfiles-sync.timer"},
		{name: "default intake status", profile: DefaultProfile, kind: SchedulerKindIntake, action: "status", want: "--user is-active dotfiles-sync-intake.timer"},
		{name: "custom push pause", profile: "research", kind: SchedulerKindPush, action: "pause", want: "--user stop dotfiles-research.timer"},
		{name: "custom push resume", profile: "research", kind: SchedulerKindPush, action: "resume", want: "--user start dotfiles-research.timer"},
		{name: "custom push uninstall", profile: "research", kind: SchedulerKindPush, action: "uninstall", want: "--user disable --now dotfiles-research.timer"},
		{name: "custom intake install", profile: "research", kind: SchedulerKindIntake, action: "install", want: "--user enable --now dotfiles-research-intake.timer"},
		{name: "custom intake pause", profile: "research", kind: SchedulerKindIntake, action: "pause", want: "--user stop dotfiles-research-intake.timer"},
		{name: "custom intake resume", profile: "research", kind: SchedulerKindIntake, action: "resume", want: "--user start dotfiles-research-intake.timer"},
		{name: "custom intake uninstall", profile: "research", kind: SchedulerKindIntake, action: "uninstall", want: "--user disable --now dotfiles-research-intake.timer"},
		{name: "custom intake status", profile: "research", kind: SchedulerKindIntake, action: "status", want: "--user is-active dotfiles-research-intake.timer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binDir := filepath.Join(root, "bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(root, "systemctl-args")
			writeStub(t, filepath.Join(binDir, "dot"), "#!/bin/sh\nexit 0\n")
			writeStub(t, filepath.Join(binDir, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$DOTFILES_TEST_SYSTEMCTL_ARGS\"\necho active\nexit 0\n")
			t.Setenv("PATH", binDir)
			t.Setenv("DOTFILES_TEST_SYSTEMCTL_ARGS", record)

			paths := withProfile(pathsFor(root, filepath.Join(root, "cache")), tc.profile)
			timer := paths.SystemdTimerFor(tc.kind)
			if err := os.MkdirAll(filepath.Dir(timer), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.action != "install" {
				if err := os.WriteFile(timer, []byte("persisted timer"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			scheduler := NewScheduler(
				exec.NewRunner(false, slog.New(slog.NewTextHandler(io.Discard, nil))),
				paths,
				&Config{LocalPath: filepath.Join(root, "workspace"), LogFile: filepath.Join(root, "sync.log"), Interval: 60},
				template.NewEngine(),
			)

			var err error
			switch tc.action {
			case "install":
				err = scheduler.InstallKind(context.Background(), tc.kind)
			case "uninstall":
				err = scheduler.UninstallKind(context.Background(), tc.kind)
			case "pause":
				err = scheduler.PauseKind(context.Background(), tc.kind)
			case "resume":
				err = scheduler.ResumeKind(context.Background(), tc.kind)
			case "status":
				if got := scheduler.StateKind(context.Background(), tc.kind); got != SchedulerRunning {
					t.Fatalf("StateKind = %s, want running", got)
				}
			default:
				t.Fatalf("unknown action %q", tc.action)
			}
			if err != nil {
				t.Fatalf("%s: %v", tc.action, err)
			}
			body, err := os.ReadFile(record)
			if err != nil {
				t.Fatalf("read systemctl arguments: %v", err)
			}
			if !containsCommandLine(string(body), tc.want) {
				t.Fatalf("systemctl calls = %q, missing %q", string(body), tc.want)
			}
		})
	}
}

func containsCommandLine(output, want string) bool {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func TestSchedulerMutators_RejectInvalidHomeBeforeMutation(t *testing.T) {
	for _, home := range []string{string([]byte{'/', 't', 'm', 'p', '/', 0xff}), "/tmp/control\x01home"} {
		t.Run("invalid home", func(t *testing.T) {
			s := NewScheduler(exec.NewRunner(false, slog.New(slog.NewTextHandler(io.Discard, nil))), &Paths{}, &Config{Home: home}, template.NewEngine())
			for name, mutate := range map[string]func(context.Context) error{
				"install kind":   func(ctx context.Context) error { return s.InstallKind(ctx, SchedulerKindPush) },
				"uninstall kind": func(ctx context.Context) error { return s.UninstallKind(ctx, SchedulerKindPush) },
				"pause kind":     func(ctx context.Context) error { return s.PauseKind(ctx, SchedulerKindPush) },
				"resume kind":    func(ctx context.Context) error { return s.ResumeKind(ctx, SchedulerKindPush) },
				"legacy cleanup": func(ctx context.Context) error { return s.CleanupLegacyUnits(ctx) },
			} {
				t.Run(name, func(t *testing.T) {
					if err := mutate(context.Background()); err == nil {
						t.Fatal("invalid home reached a scheduler mutator")
					}
				})
			}
		})
	}
}
