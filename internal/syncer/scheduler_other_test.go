//go:build !darwin

package syncer

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

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
