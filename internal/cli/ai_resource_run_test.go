package cli

import (
	"bytes"
	"context"
	"github.com/entelecheia/dotfiles-v2/internal/resourceguard"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAIRunDryRunDoesNotExecute(t *testing.T) {
	c := newAIRunCmd()
	c.Flags().Bool("dry-run", true, "")
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"--", "definitely-not-a-real-command"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would acquire repository resource slot") {
		t.Fatal(out.String())
	}
}
func TestAIRunRejectsUnboundedWait(t *testing.T) {
	c := newAIRunCmd()
	c.Flags().Bool("dry-run", false, "")
	c.SetArgs([]string{"--wait", "24h", "--", "anything"})
	if err := c.Execute(); err == nil {
		t.Fatal("unbounded wait accepted")
	}
}

func TestAIRunProjectBindsAdmissionAndChildDirectory(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	want, err := resourceguard.ResolveScope(resourceguard.Options{ProjectDir: repo})
	if err != nil {
		t.Fatal(err)
	}
	released := false
	c := newAIRunCmdWithAdmission(func(_ context.Context, opts resourceguard.Options, _ time.Duration) (func(), error) {
		got, err := resourceguard.ResolveScope(opts)
		if err != nil {
			return nil, err
		}
		if got.Key != want.Key {
			t.Fatalf("scope=%+v want=%+v", got, want)
		}
		return func() { released = true }, nil
	})
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"--project", repo, "--", "/bin/pwd"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	actual, err := filepath.EvalSymlinks(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := filepath.EvalSymlinks(repo)
	if actual != expected || !released {
		t.Fatalf("child cwd=%s expected=%s released=%t", actual, expected, released)
	}
}
