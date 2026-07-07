package module

import (
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	dotexec "github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/template"
)

func gpgTestContext(t *testing.T, signing bool) *RunContext {
	t.Helper()
	return &RunContext{
		Config: &config.Config{Modules: config.ModulesConfig{
			Git: config.GitConfig{Signing: signing},
		}},
		Runner:   dotexec.NewRunner(false, slog.Default()),
		Template: template.NewEngine(),
		HomeDir:  t.TempDir(),
	}
}

func gpgHasChange(changes []Change, desc string) bool {
	for _, c := range changes {
		if c.Description == desc {
			return true
		}
	}
	return false
}

func seedGitGlobal(t *testing.T, kvs ...[2]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	for _, kv := range kvs {
		if out, err := exec.Command("git", "config", "--global", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("seed %s=%s: %v (%s)", kv[0], kv[1], err, out)
		}
	}
}

// When commit.gpgsign is already true but gpg.format is unset, Check must still
// report the pending gpg.format change (Apply would set it) — otherwise
// check/diff under-reports a change Apply performs.
func TestGPGModule_Check_ReportsMissingGPGFormat(t *testing.T) {
	seedGitGlobal(t, [2]string{"commit.gpgsign", "true"})

	res, err := (&GPGModule{}).Check(context.Background(), gpgTestContext(t, true))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !gpgHasChange(res.Changes, "configure git gpg format") {
		t.Errorf("Check must report a pending gpg.format change; got %+v", res.Changes)
	}
	if gpgHasChange(res.Changes, "configure git commit signing") {
		t.Errorf("commit.gpgsign already true — must not report a commit signing change; got %+v", res.Changes)
	}
	if res.Satisfied {
		t.Error("Check must not be satisfied while gpg.format is unset")
	}
}

// With both signing keys already set, Check must report no signing-related
// changes (template file changes are unrelated and allowed).
func TestGPGModule_Check_NoSigningChangesWhenBothSet(t *testing.T) {
	seedGitGlobal(t,
		[2]string{"commit.gpgsign", "true"},
		[2]string{"gpg.format", "openpgp"},
	)

	res, err := (&GPGModule{}).Check(context.Background(), gpgTestContext(t, true))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if gpgHasChange(res.Changes, "configure git gpg format") ||
		gpgHasChange(res.Changes, "configure git commit signing") {
		t.Errorf("both signing keys set — no signing changes expected; got %+v", res.Changes)
	}
}
