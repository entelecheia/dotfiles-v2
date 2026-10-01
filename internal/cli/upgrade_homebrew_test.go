package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/resourceguard"
)

type upgradeReleaseTransport struct{ requests int }

func (tr *upgradeReleaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.requests++
	if req.URL.Host != "api.github.com" || !strings.HasSuffix(req.URL.Path, "/releases/latest") {
		return nil, fmt.Errorf("unexpected download attempt: %s", req.URL.Path)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v99.0.0"}`)), Request: req}, nil
}

func formulaJSON(stable string, pinned bool) string {
	return fmt.Sprintf(`{"formulae":[{"full_name":"entelecheia/tap/dotfiles","pinned":%v,"versions":{"stable":%q}}]}`, pinned, stable)
}

// fakeUpgrade scripts upgradeRun and upgradeAcquire and records each call as
// "<base name> <args>".
type fakeUpgrade struct {
	calls    []string
	infos    []string // successive brew info outputs; the last one repeats
	version  string   // dot --version output
	acquired int
	deferErr error
	launchd  func(args []string) (string, error)
}

func (f *fakeUpgrade) install(t *testing.T) {
	t.Helper()
	run, acquire, poll := upgradeRun, upgradeAcquire, upgradePoll
	t.Cleanup(func() { upgradeRun, upgradeAcquire, upgradePoll = run, acquire, poll })
	upgradePoll = time.Millisecond
	upgradeAcquire = func(context.Context, resourceguard.Options) (func(), error) {
		if f.deferErr != nil {
			return nil, f.deferErr
		}
		f.acquired++
		return func() {}, nil
	}
	upgradeRun = func(_ context.Context, _ bool, name string, args ...string) (string, error) {
		f.calls = append(f.calls, strings.TrimSpace(filepath.Base(name)+" "+strings.Join(args, " ")))
		switch {
		case filepath.Base(name) == "launchctl":
			if f.launchd == nil {
				return "", errors.New("not loaded")
			}
			return f.launchd(args)
		case len(args) > 0 && args[0] == "info":
			out := f.infos[0]
			if len(f.infos) > 1 {
				f.infos = f.infos[1:]
			}
			return out, nil
		case len(args) > 0 && args[0] == "--version":
			return f.version, nil
		}
		return "", nil
	}
}

func (f *fakeUpgrade) brewCalls() []string {
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "launchctl ") {
			out = append(out, c)
		}
	}
	return out
}

// Run the real command from a temporary Cellar binary through a symlink, with
// brew scripted: it upgrades through brew, never downloads or writes the
// Cellar binary, and runs no brew command when already current (#235).
func TestRunUpgradeHomebrew(t *testing.T) {
	if mode := os.Getenv("DOTFILES_TEST_UPGRADE_CHILD"); mode != "" {
		transport := &upgradeReleaseTransport{}
		http.DefaultTransport = transport
		f := &fakeUpgrade{infos: []string{formulaJSON("99.0.0", false)}, version: "dot version 99.0.0 (abc)\n"}
		f.install(t)
		current := "1.0.0"
		if mode == "current" {
			current = "99.0.0"
		}
		cmd := newUpgradeCmd(current)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if mode == "check" {
			cmd.SetArgs([]string{"--check"})
		}
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v, output %q", mode, err, out.String())
		}
		var want []string
		if mode == "update" {
			want = []string{"brew info --json=v2 --formula dotfiles", "brew upgrade entelecheia/tap/dotfiles", "dot --version"}
			if !strings.Contains(out.String(), "Upgraded: 1.0.0 → 99.0.0 (Homebrew)") {
				t.Fatalf("output %q", out.String())
			}
		}
		if got := f.brewCalls(); !slices.Equal(got, want) {
			t.Fatalf("%s: brew calls %q, want %q", mode, got, want)
		}
		if mode == "update" && f.acquired != 1 {
			t.Fatalf("maintenance slot acquired %d times", f.acquired)
		}
		if transport.requests != 1 {
			t.Fatalf("made %d HTTP requests, want 1", transport.requests)
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
	for _, mode := range []string{"update", "current", "check"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := osexec.CommandContext(ctx, link, "-test.run=^TestRunUpgradeHomebrew$", "-test.count=1")
			child.Env = append(os.Environ(), "DOTFILES_TEST_UPGRADE_CHILD="+mode, "HOME="+t.TempDir())
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

func TestUpgradeHomebrewBranches(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the Homebrew path refuses root")
	}
	t.Setenv("HOME", t.TempDir()) // no LaunchAgents to reload
	h := homebrewDot{prefix: "/opt/homebrew", formula: "dotfiles"}
	const done = "dot version 2.0.0 (abc)\n"
	for _, tc := range []struct {
		name     string
		f        fakeUpgrade
		dryRun   bool
		wantErr  string
		want     []string
		wantLine string
	}{
		{
			name: "tap current",
			f:    fakeUpgrade{infos: []string{formulaJSON("2.0.0", false)}, version: done},
			want: []string{"brew info --json=v2 --formula dotfiles", "brew upgrade entelecheia/tap/dotfiles", "dot --version"},
		},
		{
			name: "tap behind, brew update catches up",
			f:    fakeUpgrade{infos: []string{formulaJSON("1.0.0", false), formulaJSON("2.0.0", false)}, version: done},
			want: []string{"brew info --json=v2 --formula dotfiles", "brew update", "brew info --json=v2 --formula dotfiles", "brew upgrade entelecheia/tap/dotfiles", "dot --version"},
		},
		{
			name:    "tap still behind after brew update",
			f:       fakeUpgrade{infos: []string{formulaJSON("1.0.0", false)}, version: done},
			wantErr: "not 2.0.0 yet",
			want:    []string{"brew info --json=v2 --formula dotfiles", "brew update", "brew info --json=v2 --formula dotfiles"},
		},
		{
			name:    "pinned",
			f:       fakeUpgrade{infos: []string{formulaJSON("2.0.0", true)}, version: done},
			wantErr: "brew unpin dotfiles",
			want:    []string{"brew info --json=v2 --formula dotfiles"},
		},
		{
			name:    "slot busy",
			f:       fakeUpgrade{infos: []string{formulaJSON("2.0.0", false)}, deferErr: &resourceguard.DeferredError{Reason: "another heavyweight job owns scope maintenance"}},
			wantErr: "maintenance slot",
		},
		{
			name:    "version not reached",
			f:       fakeUpgrade{infos: []string{formulaJSON("2.0.0", false)}, version: "dot version 1.0.0 (abc)\n"},
			wantErr: "not 2.0.0",
			want:    []string{"brew info --json=v2 --formula dotfiles", "brew upgrade entelecheia/tap/dotfiles", "dot --version"},
		},
		{
			name:     "dry run",
			f:        fakeUpgrade{infos: []string{formulaJSON("1.0.0", false)}},
			dryRun:   true,
			want:     []string{"brew info --json=v2 --formula dotfiles"},
			wantLine: "[dry-run] would run: brew update\n[dry-run] would run: brew upgrade entelecheia/tap/dotfiles\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			f.install(t)
			var out bytes.Buffer
			err := upgradeHomebrew(context.Background(), &Printer{Out: &out, Err: &out}, h, "1.0.0", "2.0.0", tc.dryRun)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if got := f.brewCalls(); !slices.Equal(got, tc.want) {
				t.Fatalf("calls %q, want %q", got, tc.want)
			}
			if tc.dryRun && f.acquired != 0 {
				t.Fatal("dry run took the maintenance slot")
			}
			if !strings.Contains(out.String(), tc.wantLine) {
				t.Fatalf("output %q, want %q", out.String(), tc.wantLine)
			}
		})
	}
}

// After the upgrade, a loaded job running the Cellar binary is booted out and
// bootstrapped; an unloaded job (a paused sync) and a job running another
// binary are left alone (#235).
func TestReloadDotLaunchAgents(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd only")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := homebrewDot{prefix: filepath.Join(root, "brew"), formula: "dotfiles"}
	kegDot := filepath.Join(h.prefix, "Cellar", "dotfiles", "2.0.0", "bin", "dot")
	linked := filepath.Join(h.prefix, "bin", "dot")
	other := filepath.Join(root, "other", "dot")
	for _, f := range []string{kegDot, other} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kegDot, linked); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"com.dotfiles.sync", "com.dotfiles.peer", "com.dotfiles.other", "com.dotfiles.broken"} {
		if err := os.WriteFile(filepath.Join(agents, label+".plist"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	domain := guiTarget()

	for _, tc := range []struct {
		name          string
		bootstrapFail bool
		wantErr       string
	}{
		{name: "reloads"},
		{name: "bootstrap fails", bootstrapFail: true, wantErr: "could not reload com.dotfiles.broken, com.dotfiles.sync"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded := map[string]string{
				"com.dotfiles.sync":   linked,
				"com.dotfiles.broken": kegDot,
				"com.dotfiles.other":  other,
				// com.dotfiles.peer is not loaded, like a paused job
			}
			f := &fakeUpgrade{launchd: func(args []string) (string, error) {
				label := strings.TrimPrefix(args[len(args)-1], domain+"/")
				switch args[0] {
				case "print":
					if program, ok := loaded[label]; ok {
						return "com.dotfiles.x = {\n\tactive count = 0\n\tprogram = " + program + "\n\tinherited environment = {\n\t\tprogram = /nested\n\t}\n}\n", nil
					}
					return "", errors.New("Could not find service")
				case "bootout":
					delete(loaded, label)
					return "", nil
				case "bootstrap":
					if tc.bootstrapFail {
						return "", errors.New("Bootstrap failed: 5: Input/output error")
					}
					return "", nil
				}
				return "", fmt.Errorf("unexpected launchctl %v", args)
			}}
			f.install(t)
			var out bytes.Buffer
			err := reloadDotLaunchAgents(context.Background(), &Printer{Out: &out, Err: &out}, h, false)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.wantErr, out.String())
			}
			var mutations []string
			for _, c := range f.calls {
				if strings.HasPrefix(c, "launchctl bootout") || strings.HasPrefix(c, "launchctl bootstrap") {
					mutations = append(mutations, c)
				}
			}
			attempts := 1
			if tc.bootstrapFail {
				attempts = 3
			}
			var want []string
			for _, label := range []string{"com.dotfiles.broken", "com.dotfiles.sync"} {
				want = append(want, "launchctl bootout "+domain+"/"+label)
				for range attempts {
					want = append(want, "launchctl bootstrap "+domain+" "+filepath.Join(agents, label+".plist"))
				}
			}
			if !slices.Equal(mutations, want) {
				t.Fatalf("launchctl mutations %q, want %q", mutations, want)
			}
			if tc.wantErr == "" && !strings.Contains(out.String(), "Reloaded com.dotfiles.sync") {
				t.Fatalf("output %q", out.String())
			}
		})
	}
}

func TestLaunchdProgram(t *testing.T) {
	dump := "gui/501/com.dotfiles.sync = {\n\tactive count = 0\n\tpath = /x.plist\n\tprogram = /opt/homebrew/bin/dot\n\tspawn = {\n\t\tprogram = /nested\n\t}\n}\n"
	if got := launchdProgram(dump); got != "/opt/homebrew/bin/dot" {
		t.Fatalf("launchdProgram = %q", got)
	}
	if got := launchdProgram("\t\tprogram = /nested\n"); got != "" {
		t.Fatalf("nested program matched: %q", got)
	}
}
