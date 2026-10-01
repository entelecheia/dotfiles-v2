package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type upgradeReleaseTransport struct{ requests int }

func (tr *upgradeReleaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.requests++
	if req.URL.Host != "api.github.com" || !strings.HasSuffix(req.URL.Path, "/releases/latest") {
		return nil, fmt.Errorf("unexpected download attempt: %s", req.URL.Path)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v99.0.0"}`)), Request: req}, nil
}

// Run the real command from a temporary Cellar binary through a symlink.
// Metadata is served in-process, so any attempted download is a test failure.
func TestRunUpgradeHomebrew(t *testing.T) {
	if mode := os.Getenv("DOTFILES_TEST_UPGRADE_CHILD"); mode != "" {
		transport := &upgradeReleaseTransport{}
		http.DefaultTransport = transport
		cmd := newUpgradeCmd("1.0.0")
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if mode == "check" {
			cmd.SetArgs([]string{"--check"})
		}
		err := cmd.Execute()
		if mode == "check" {
			if err != nil || !strings.Contains(out.String(), "99.0.0") {
				t.Fatalf("check: %v, output %q", err, out.String())
			}
		} else if err == nil || !strings.Contains(err.Error(), "brew upgrade dotfiles") {
			t.Fatalf("update: %v, want a package-manager refusal", err)
		}
		wantRequests := 0
		if mode == "check" {
			wantRequests = 1
		}
		if transport.requests != wantRequests {
			t.Fatalf("made %d HTTP requests, want %d", transport.requests, wantRequests)
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "Cellar", "dotfiles", "1.0.0", "bin", "dot")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "dot")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(data)
	for _, mode := range []string{"update", "check"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := osexec.CommandContext(ctx, link, "-test.run=^TestRunUpgradeHomebrew$", "-test.count=1")
			child.Env = append(os.Environ(), "DOTFILES_TEST_UPGRADE_CHILD="+mode)
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("child: %v\n%s", err, output)
			}
			after, err := os.ReadFile(binary)
			if err != nil || sha256.Sum256(after) != before {
				t.Fatalf("binary changed: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(binary))
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging files left behind: %v, %v", entries, err)
			}
		})
	}
}
