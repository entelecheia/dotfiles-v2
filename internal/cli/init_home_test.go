package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func writeAgeIdentity(t *testing.T, home, name, recipient string) {
	t.Helper()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, name), []byte("AGE-SECRET-KEY-1TEST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, name+".pub"), []byte(recipient+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitHomeOverrideUsesTargetAgeKey(t *testing.T) {
	invoker := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", invoker)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(invoker, ".config"))
	writeAgeIdentity(t, invoker, "age_key_invoker", "age1invoker")
	writeAgeIdentity(t, target, "age_key_target", "age1target")

	_, errOut, err := runDotForTest("--home", target, "--yes", "init")
	if err != nil {
		t.Fatalf("dot --home init --yes: %v\nstderr=%s", err, errOut)
	}

	state, err := config.LoadStateForHome(target)
	if err != nil {
		t.Fatalf("load target state: %v", err)
	}
	if got, want := state.Secrets.AgeIdentity, "~/.ssh/age_key_target"; got != want {
		t.Fatalf("target age identity = %q, want %q", got, want)
	}
}

func TestInitHomeOverrideDoesNotRecordInvokerAgeKey(t *testing.T) {
	invoker := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", invoker)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(invoker, ".config"))
	writeAgeIdentity(t, invoker, "age_key_invoker", "age1invoker")

	_, errOut, err := runDotForTest("--home", target, "--yes", "init")
	if err != nil {
		t.Fatalf("dot --home init --yes: %v\nstderr=%s", err, errOut)
	}

	state, err := config.LoadStateForHome(target)
	if err != nil {
		t.Fatalf("load target state: %v", err)
	}
	if got := state.Secrets.AgeIdentity; got != "" {
		t.Fatalf("target state recorded invoking age identity %q", got)
	}
}
