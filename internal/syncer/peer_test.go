package syncer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileStoresAreIsolated(t *testing.T) {
	ws := t.TempDir()
	mirror := ResolveLocalPathsForProfile(ws, DefaultProfile)
	peer := ResolveLocalPathsForProfile(ws, "peer")

	if mirror.StoreDir == peer.StoreDir {
		t.Fatalf("profiles share a store dir: %s", mirror.StoreDir)
	}
	if filepath.Base(mirror.StoreDir) != "sync" {
		t.Errorf("default profile store = %s, want .../sync", mirror.StoreDir)
	}
	if filepath.Base(peer.StoreDir) != "peer" {
		t.Errorf("peer store = %s, want .../peer", peer.StoreDir)
	}
	// allow.txt separation is the whole point: the peer profile opts secrets IN,
	// and that must not reach the mirror profile, which must keep them OUT.
	if mirror.AllowFile == peer.AllowFile {
		t.Error("profiles share allow.txt; a peer secrets opt-in would leak to the cloud mirror")
	}
	if mirror.BaselineFile == peer.BaselineFile {
		t.Error("profiles share baseline.manifest")
	}
}

func TestPeerStoreIsNotGitTracked(t *testing.T) {
	// The managed .gitignore block ignores /.dotfiles/* and whitelists only
	// sync/. A shared, git-tracked peer baseline would produce merge conflicts
	// in the very file that coordinates the two machines.
	var whitelisted []string
	for _, e := range gitignoreEntries {
		if strings.HasPrefix(e, "!/") {
			whitelisted = append(whitelisted, e)
		}
	}
	for _, e := range whitelisted {
		if strings.Contains(e, "/peer/") {
			t.Fatalf("peer store is whitelisted in .gitignore (%s); it must stay machine-local", e)
		}
	}
	if !containsString(gitignoreEntries, "/.dotfiles/*") {
		t.Fatal("missing /.dotfiles/* catch-all; a new profile store would not be ignored")
	}
}

func TestValidateProfileRejectsPathEscapes(t *testing.T) {
	for _, bad := range []string{"..", ".", "a/b", "a\\b", "../etc"} {
		if err := ValidateProfile(bad); err == nil {
			t.Errorf("ValidateProfile(%q) = nil, want error", bad)
		}
	}
	for _, ok := range []string{"", "sync", "peer", "peer2"} {
		if err := ValidateProfile(ok); err != nil {
			t.Errorf("ValidateProfile(%q) = %v, want nil", ok, err)
		}
	}
	if NormalizeProfile("") != DefaultProfile {
		t.Error("empty profile should normalize to the default")
	}
}

func TestProfilePathsKeepDefaultUnchanged(t *testing.T) {
	home := t.TempDir()
	base, err := ResolvePathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	def, err := ResolvePathsForHomeProfile(home, DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if def.LockDir != base.LockDir || def.LaunchdPlist != base.LaunchdPlist {
		t.Errorf("default profile changed existing paths:\n lock %s vs %s\n plist %s vs %s",
			def.LockDir, base.LockDir, def.LaunchdPlist, base.LaunchdPlist)
	}
	peer, err := ResolvePathsForHomeProfile(home, "peer")
	if err != nil {
		t.Fatal(err)
	}
	if peer.LockDir == base.LockDir {
		t.Error("peer shares the mirror lock; a peer sync would block a mirror push for no reason")
	}
	if peer.LaunchdPlist == base.LaunchdPlist {
		t.Error("peer shares the launchd unit; installing one scheduler would uninstall the other")
	}
	if !strings.HasSuffix(peer.LaunchdPlist, "com.dotfiles.peer.plist") {
		t.Errorf("peer plist = %s", peer.LaunchdPlist)
	}
}

func TestCheckOwner(t *testing.T) {
	host, err := ShortHostname()
	if err != nil {
		t.Skip("hostname unavailable")
	}
	if err := CheckOwner(&Config{Profile: "sync"}); err != nil {
		t.Errorf("empty owner must stay unrestricted, got %v", err)
	}
	if err := CheckOwner(&Config{Profile: "sync", Owner: host}); err != nil {
		t.Errorf("owner == this host should pass, got %v", err)
	}
	// Domain suffixes must not defeat the comparison.
	if err := CheckOwner(&Config{Profile: "sync", Owner: host + ".local"}); err != nil {
		t.Errorf("owner with domain suffix should pass, got %v", err)
	}
	err = CheckOwner(&Config{Profile: "sync", Owner: "some-other-machine"})
	if err == nil {
		t.Fatal("foreign owner must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("error should say what it refuses: %v", err)
	}
}

func TestRemoteRsyncUsable(t *testing.T) {
	// The exact banners seen in the field.
	unusable := []string{
		"openrsync: protocol version 29\nrsync version 2.6.9 compatible",
		"rsync  version 2.6.9  protocol version 29",
	}
	for _, v := range unusable {
		if remoteRsyncUsable(v) {
			t.Errorf("remoteRsyncUsable(%q) = true, want false", firstLine(v))
		}
	}
	usable := []string{
		"rsync  version 3.4.4  protocol version 32",
		"rsync  version 3.2.7  protocol version 31",
	}
	for _, v := range usable {
		if !remoteRsyncUsable(v) {
			t.Errorf("remoteRsyncUsable(%q) = false, want true", firstLine(v))
		}
	}
}

func TestClassifyRsyncError(t *testing.T) {
	if got := classifyRsyncError(nil); got != nil {
		t.Errorf("nil in, %v out", got)
	}
	// 23 = partial transfer, 24 = vanished files. Both are routine on a live
	// tree and must not be treated as "stop everything".
	for _, code := range []int{23, 24} {
		err := exitErr(t, code)
		got := classifyRsyncError(err)
		if !IsPartialTransfer(got) {
			t.Errorf("exit %d should classify as partial, got %v", code, got)
		}
	}
	for _, code := range []int{1, 12, 30} {
		got := classifyRsyncError(exitErr(t, code))
		if IsPartialTransfer(got) {
			t.Errorf("exit %d must not be treated as partial", code)
		}
	}
}

func TestCommonArgsAddsDirWriteBit(t *testing.T) {
	cfg := &Config{FilterMode: FilterModeExclude}
	args := commonArgs(cfg, runtimeFilters{})
	if !containsString(args, "--chmod=Du+w") {
		t.Error("missing --chmod=Du+w; a read-only source dir would fail with exit 23")
	}
}

func TestTransportPassesRemoteRsyncPath(t *testing.T) {
	local := &Config{Target: Target{Kind: TargetLocal, Path: "/tmp/x/"}}
	if got := rsyncTransportArgs(local); got != nil {
		t.Errorf("local target should have no transport args, got %v", got)
	}
	ssh := &Config{Target: Target{Kind: TargetSSH, Host: "h", Path: "/p"}}
	got := rsyncTransportArgs(ssh)
	if !containsString(got, "-e") || !containsString(got, "ssh") {
		t.Errorf("ssh target needs -e ssh, got %v", got)
	}
	if containsString(got, "--rsync-path=") {
		t.Error("no --rsync-path expected when the remote default is usable")
	}
	ssh.RemoteRsyncPath = "/opt/homebrew/bin/rsync"
	got = rsyncTransportArgs(ssh)
	if !containsString(got, "--rsync-path=/opt/homebrew/bin/rsync") {
		t.Errorf("remote rsync override not passed: %v", got)
	}
}

// TestTrackedIncludesRecurseSubmodules is the regression test for the measured
// failure: tracked files inside a submodule were absent from the include layer,
// so an exclude pattern dropped them and the receiver saw deletions.
func TestTrackedIncludesRecurseSubmodules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "sites", "inner")
	mustMkdir(t, filepath.Join(sub, "__pycache__"))
	gitInit(t, sub)
	writeFile(t, filepath.Join(sub, "__pycache__", "a.pyc"), "x")
	gitAddCommit(t, sub, "seed")

	gitInit(t, root)
	writeFile(t, filepath.Join(root, "top.txt"), "y")
	gitAddCommit(t, root, "root")

	// The outer repo sees the submodule dir but not its contents.
	outer := gitTrackedForSync(root)
	if outer["sites/inner/__pycache__/a.pyc"] {
		t.Fatal("precondition failed: outer ls-files already lists submodule contents")
	}

	got := gitTrackedInSubmodules(root, []string{"sites/inner"})
	if !got["sites/inner/__pycache__/a.pyc"] {
		t.Errorf("submodule-tracked file missing from include layer; got %v", keys(got))
	}
}

// helpers

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want || (strings.HasSuffix(want, "=") && strings.HasPrefix(s, want)) {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func exitErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected a non-zero exit for code %d", code)
	}
	return err
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func gitAddCommit(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestPreferredMachineNameIsDNSSafeAndSpecific(t *testing.T) {
	got := PreferredMachineName()
	if got == "" {
		t.Skip("no machine name available")
	}
	if !dnsSafeName(got) {
		t.Errorf("PreferredMachineName() = %q, which is not DNS-safe; owner values get typed and diffed", got)
	}
	if got == "mac" && len(MachineNames()) > 1 {
		t.Errorf("picked the generic default %q despite better candidates %v", got, MachineNames())
	}
	// Whatever it picks must actually satisfy the guard on this machine.
	if err := CheckOwner(&Config{Profile: "sync", Owner: got}); err != nil {
		t.Errorf("PreferredMachineName() = %q but CheckOwner rejects it: %v", got, err)
	}
}

// TestFreshProfileHasEveryFilterFileRsyncReferences is the regression test for a
// real failure: commonArgs passes --exclude-from for exclude.txt and ignore.txt
// unconditionally, and rsync exits 11 when it cannot open one. A freshly created
// profile whose store lacked them failed every transfer.
func TestFreshProfileHasEveryFilterFileRsyncReferences(t *testing.T) {
	ws := t.TempDir()
	paths := ResolveLocalPathsForProfile(ws, "peer")
	if err := EnsureLocalLayout(paths); err != nil {
		t.Fatalf("EnsureLocalLayout: %v", err)
	}
	for _, f := range []struct{ name, path string }{
		{"exclude.txt", paths.ExcludeFile},
		{"ignore.txt", paths.IgnoreFile},
		{"include.txt", paths.IncludeFile},
		{"allow.txt", paths.AllowFile},
	} {
		if _, err := os.Stat(f.path); err != nil {
			t.Errorf("%s missing after layout creation: %v (rsync would exit 11)", f.name, err)
		}
	}
}
