package config

import (
	"os"
	"path/filepath"
	"testing"
)

// BUG-20, second site: detectVaultDir expanded a ~-form workspace path with
// fileutil.ExpandHome, which reads the process environment, so `dot apply
// --home /tmp/sandbox` decided where the sandbox's vault lives by stat'ing the
// INVOKING user's tree. What lands in the rendered shell config then names a
// directory the target home may not have.
//
// Every row seeds a DIFFERENT layout in each home, so a wrong-home
// implementation fails on a wrong answer rather than on an absent directory,
// and the two layouts are mirrored across rows so it fails in both directions.
func seedVaultHomes(t *testing.T, invokingRel, targetRel string) (invoking, target string) {
	t.Helper()
	invoking, target = t.TempDir(), t.TempDir()
	for home, rel := range map[string]string{invoking: invokingRel, target: targetRel} {
		if rel == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Join(home, "workspace", rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The process is pointed at the invoking home for the whole test: that is
	// the value the defect read.
	t.Setenv("HOME", invoking)
	return invoking, target
}

func TestResolveVaultPath_DetectsInTheTargetHome(t *testing.T) {
	tests := []struct {
		name        string
		invokingRel string
		targetRel   string
		wantPath    string
		wantClone   string
	}{
		{
			name:        "target has work/vault, invoker has vault",
			invokingRel: "vault",
			targetRel:   "work/vault",
			wantPath:    "~/workspace/work/vault",
			wantClone:   "~/workspace/work/vault",
		},
		{
			// The mirror image. Without it a run that always answered
			// work/vault would pass the row above for the wrong reason.
			name:        "target has vault, invoker has work/vault",
			invokingRel: "work/vault",
			targetRel:   "vault",
			wantPath:    "~/workspace/vault",
			wantClone:   "~/workspace/vault",
		},
		{
			// Nothing on disk in the target: ResolveVaultPath invents the
			// fresh default, ResolveVaultCloneTarget deliberately does not.
			// Reading the invoker's tree here answers "work/vault exists"
			// and turns the legacy fallthrough into a redirect.
			name:        "target has no vault, invoker has work/vault",
			invokingRel: "work/vault",
			targetRel:   "",
			wantPath:    "~/workspace/work/vault",
			wantClone:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, target := seedVaultHomes(t, tt.invokingRel, tt.targetRel)
			cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}

			if got := cfg.VaultPath(target); got != tt.wantPath {
				t.Errorf("VaultPath(target) = %q, want %q (detection read the invoking user's tree)", got, tt.wantPath)
			}
			if got := cfg.VaultCloneTarget(target); got != tt.wantClone {
				t.Errorf("VaultCloneTarget(target) = %q, want %q (detection read the invoking user's tree)", got, tt.wantClone)
			}
		})
	}
}

// Non-vacuity: passing the process home must still read the process home.
// Without this row a detectVaultDir that answered "" for everything would
// satisfy the mirrored rows above.
func TestResolveVaultPath_ProcessHomeStillDetected(t *testing.T) {
	invoking, _ := seedVaultHomes(t, "vault", "work/vault")
	cfg := &Config{Modules: ModulesConfig{Workspace: WorkConfig{Path: "~/workspace"}}}

	if got, want := cfg.VaultPath(invoking), "~/workspace/vault"; got != want {
		t.Errorf("VaultPath(invoking home) = %q, want %q", got, want)
	}
}

// Converse rule: before the home was a parameter, detectVaultDir resolved it
// itself, so "" was only reachable when os.UserHomeDir failed. Now any caller
// can supply it, and filepath.Join("", "workspace") is "workspace" — a probe
// of the process WORKING DIRECTORY. Detection must find nothing instead of
// answering from wherever the binary happened to be started.
func TestResolveVaultPath_EmptyHomeDetectsNothing(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "workspace", "vault"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	// The fresh default, i.e. nothing was detected. Seeded as <ws>/vault so a
	// cwd probe answers differently from the default rather than by accident.
	if got, want := ResolveVaultPath("", "~/workspace", ""), "~/workspace/work/vault"; got != want {
		t.Errorf("ResolveVaultPath with no home = %q, want %q (it probed the working directory)", got, want)
	}
	if got := ResolveVaultCloneTarget("", "~/workspace", ""); got != "" {
		t.Errorf("ResolveVaultCloneTarget with no home = %q, want empty (it probed the working directory)", got)
	}
}
