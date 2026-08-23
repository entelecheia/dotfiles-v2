package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

// BUG-07: `dot status` resolved its state, config path, secrets store, sync
// paths and workspace registry from the process home, so `--home <other>`
// printed the invoking user's name, email, GitHub user, config path and live
// sync timestamps instead of the target's.
//
// Every row seeds TWO homes and asserts the target's distinctive values are
// PRESENT as well as the invoker's being absent. An absence-only assertion
// passes when a section prints nothing at all, which is not the property
// under test.

type statusHomeSeed struct {
	home     string
	name     string
	email    string
	github   string
	profile  string
	project  string
	ageFiles int
	sshKey   bool // seed ~/.ssh/<key>, so "SSH key" reads present
	shellSec bool // seed ~/.config/shell/90-secrets.sh
}

// seedStatusHome writes everything the five home-derived sections of
// `dot status` read: the state file, the workspace registry, the encrypted
// store, and the two plaintext secrets the summary probes for.
func seedStatusHome(t *testing.T, s statusHomeSeed) {
	t.Helper()
	writeCLITestFile(t, config.StatePathForHome(s.home),
		"name: "+s.name+"\nemail: "+s.email+"\ngithub_user: "+s.github+"\nprofile: "+s.profile+"\n")
	writeCLITestFile(t, filepath.Join(s.home, ".config", "dot", "workspace.yaml"),
		"projects:\n  - name: "+s.project+"\n    path: "+filepath.Join(s.home, "proj")+"\n")
	for i := 0; i < s.ageFiles; i++ {
		writeCLITestFile(t, filepath.Join(s.home, ".local", "share", "dotfiles-secrets", fmt.Sprintf("f%d.age", i)), "x")
	}
	if s.sshKey {
		writeCLITestFile(t, filepath.Join(s.home, ".ssh", "id_ed25519"), "key")
	}
	if s.shellSec {
		writeCLITestFile(t, filepath.Join(s.home, ".config", "shell", "90-secrets.sh"), "# secrets")
	}
}

// newStatusHomeFixture seeds an invoking home (which it also installs as the
// process HOME, with XDG_CONFIG_HOME pointing inside it) and a separate target
// home, and returns both paths.
//
// XDG_CONFIG_HOME is deliberately pointed at the INVOKER's tree: with it set,
// a `--home <target>` run that resolves the state path through
// config.StateDir() lands in the invoker's config, so these rows also pin the
// precedence decision (the flag outranks XDG_CONFIG_HOME).
func newStatusHomeFixture(t *testing.T) (invoker, target string) {
	t.Helper()
	invoker = t.TempDir()
	target = t.TempDir()

	t.Setenv("HOME", invoker)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(invoker, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(invoker, ".cache"))
	t.Setenv("DOTFILES_HOME", "")
	t.Setenv("DOTFILES_PROFILE", "")
	t.Setenv("PATH", t.TempDir())

	seedStatusHome(t, statusHomeSeed{
		home: invoker, name: "Invoking User", email: "invoker@example.invalid",
		github: "invokeruser", profile: "full", project: "invoker-project",
		ageFiles: 5, shellSec: true,
	})
	seedStatusHome(t, statusHomeSeed{
		home: target, name: "Target Admin", email: "target@example.invalid",
		github: "targetuser", profile: "minimal", project: "target-project",
		ageFiles: 2, sshKey: true,
	})
	return invoker, target
}

func TestStatusHonorsHomeFlag(t *testing.T) {
	invoker, target := newStatusHomeFixture(t)

	out, errOut, err := runDotForTest("--home", target, "status")
	if err != nil {
		t.Fatalf("status --home: %v\nstderr=%s", err, errOut)
	}

	// User section: the target's identity, and none of the invoker's.
	for _, want := range []string{"Target Admin", "target@example.invalid", "targetuser", "minimal"} {
		if !strings.Contains(out, want) {
			t.Errorf("status --home did not report the target home's %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"Invoking User", "invoker@example.invalid", "invokeruser"} {
		if strings.Contains(out, leaked) {
			t.Errorf("status --home leaked the invoking user's %q:\n%s", leaked, out)
		}
	}

	// Config path: inside the target home, not the invoker's XDG config dir.
	if !strings.Contains(out, config.StatePathForHome(target)) {
		t.Errorf("status --home printed a config path outside the target home (want %s):\n%s",
			config.StatePathForHome(target), out)
	}
	if strings.Contains(out, config.StatePathForHome(invoker)) {
		t.Errorf("status --home printed the invoking user's config path:\n%s", out)
	}

	// Secrets: the target's store is counted, the target's SSH key is seen,
	// and the invoker's shell secrets file is not.
	if !strings.Contains(out, "2 file(s)") {
		t.Errorf("status --home did not count the target home's encrypted store (want 2 file(s)):\n%s", out)
	}
	if strings.Contains(out, "5 file(s)") {
		t.Errorf("status --home counted the invoking user's encrypted store:\n%s", out)
	}
	assertKV(t, out, "SSH key", "present")
	assertKV(t, out, "Shell secrets", "missing")

	// Sync: the target home's workspace and mirror trees.
	if !strings.Contains(out, filepath.Join(target, "workspace", "work")) {
		t.Errorf("status --home resolved sync paths outside the target home:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(invoker, "workspace", "work")) {
		t.Errorf("status --home resolved the invoking user's sync paths:\n%s", out)
	}

	// Workspace: the target home's project registry.
	if !strings.Contains(out, "target-project") {
		t.Errorf("status --home read a workspace registry outside the target home:\n%s", out)
	}
	if strings.Contains(out, "invoker-project") {
		t.Errorf("status --home read the invoking user's workspace registry:\n%s", out)
	}
}

// TestStatusWithoutHomeFlagUsesProcessHome is the non-vacuity row: without the
// flag every section must resolve exactly as it does today. Without it the fix
// could hardcode the target sandbox and still pass the row above.
func TestStatusWithoutHomeFlagUsesProcessHome(t *testing.T) {
	invoker, target := newStatusHomeFixture(t)

	out, errOut, err := runDotForTest("status")
	if err != nil {
		t.Fatalf("status: %v\nstderr=%s", err, errOut)
	}

	for _, want := range []string{"Invoking User", "invoker@example.invalid", "invokeruser", "invoker-project", "5 file(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status without --home did not report the process home's %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"Target Admin", "target@example.invalid", "targetuser", "target-project", "2 file(s)"} {
		if strings.Contains(out, leaked) {
			t.Errorf("status without --home reported the unrelated home's %q:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, filepath.Join(invoker, "workspace", "work")) {
		t.Errorf("status without --home resolved sync paths outside the process home:\n%s", out)
	}
	if strings.Contains(out, filepath.Join(target, "workspace", "work")) {
		t.Errorf("status without --home resolved the unrelated home's sync paths:\n%s", out)
	}
	assertKV(t, out, "SSH key", "missing")
	assertKV(t, out, "Shell secrets", "present")
}

// TestStatusHomePrecedence pins the three-step order check.go established:
// the flag wins, $DOTFILES_HOME is the fallback, the process home is last.
func TestStatusHomePrecedence(t *testing.T) {
	t.Run("env override alone", func(t *testing.T) {
		_, target := newStatusHomeFixture(t)
		t.Setenv("DOTFILES_HOME", target)

		out, errOut, err := runDotForTest("status")
		if err != nil {
			t.Fatalf("status: %v\nstderr=%s", err, errOut)
		}
		if !strings.Contains(out, "Target Admin") {
			t.Errorf("status ignored $DOTFILES_HOME:\n%s", out)
		}
		if strings.Contains(out, "Invoking User") {
			t.Errorf("status read the process home despite $DOTFILES_HOME:\n%s", out)
		}
	})

	t.Run("flag outranks env", func(t *testing.T) {
		invoker, target := newStatusHomeFixture(t)
		t.Setenv("DOTFILES_HOME", invoker)

		out, errOut, err := runDotForTest("--home", target, "status")
		if err != nil {
			t.Fatalf("status --home: %v\nstderr=%s", err, errOut)
		}
		if !strings.Contains(out, "Target Admin") {
			t.Errorf("$DOTFILES_HOME outranked the --home flag:\n%s", out)
		}
		if strings.Contains(out, "Invoking User") {
			t.Errorf("$DOTFILES_HOME outranked the --home flag:\n%s", out)
		}
	})
}

// assertKV asserts that the line carrying `key` also carries `want`. Matching
// per line rather than on the whole document keeps "present"/"missing" rows
// from being satisfied by a different section's value.
func assertKV(t *testing.T, out, key, want string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, key) {
			if !strings.Contains(line, want) {
				t.Errorf("%q row reads %q, want %q\nline: %s", key, strings.TrimSpace(line), want, line)
			}
			return
		}
	}
	t.Errorf("no %q row in status output:\n%s", key, out)
}

// TestJSONStatusSurfacesHonorHomeFlag is BUG-07's other half: `dot sync status
// --json` and `dot peer status --json` resolved workspace paths through
// syncer.Bootstrap, which loaded state and resolved the profile from the
// process home no matter what the flag said.
func TestJSONStatusSurfacesHonorHomeFlag(t *testing.T) {
	for _, surface := range [][]string{
		{"sync", "status", "--json"},
		{"peer", "status", "--json"},
	} {
		t.Run(strings.Join(surface, " "), func(t *testing.T) {
			invoker, target := newStatusHomeFixture(t)

			out, errOut, err := runDotForTest(append([]string{"--home", target}, surface...)...)
			if err != nil {
				t.Fatalf("%v --home: %v\nstderr=%s", surface, err, errOut)
			}
			if !strings.Contains(out, filepath.Join(target, "workspace", "work")) {
				t.Errorf("--home did not move the workspace path into the target home:\n%s", out)
			}
			if strings.Contains(out, invoker) {
				t.Errorf("--home run still reports paths under the invoking user's home:\n%s", out)
			}

			// Non-vacuity: without the flag the same document resolves through
			// the process home exactly as it does today.
			out, errOut, err = runDotForTest(surface...)
			if err != nil {
				t.Fatalf("%v: %v\nstderr=%s", surface, err, errOut)
			}
			if !strings.Contains(out, filepath.Join(invoker, "workspace", "work")) {
				t.Errorf("without --home the workspace path left the process home:\n%s", out)
			}
			if strings.Contains(out, target) {
				t.Errorf("without --home the document reports an unrelated home:\n%s", out)
			}
		})
	}
}
