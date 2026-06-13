package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// stubAge installs an executable "age" stub and prepends its dir to PATH.
// Args arrive as: -d -i <identity> -o <out> <src>, so $5 is the output path
// and $6 the source. In ok mode "decryption" copies the source file.
func stubAge(t *testing.T, fail bool) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncat \"$6\" > \"$5\"\n"
	if fail {
		script = "#!/bin/sh\nexit 1\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "age"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type restoreFixture struct {
	runner   *exec.Runner
	identity string
	srcAge   string
	dest     string
}

// newRestoreFixture seeds an identity file and a fake .age source whose
// stub-decrypted content is plaintext.
func newRestoreFixture(t *testing.T, plaintext string) *restoreFixture {
	t.Helper()
	dir := t.TempDir()
	identity := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(identity, []byte("identity"), 0600); err != nil {
		t.Fatal(err)
	}
	srcAge := filepath.Join(dir, "secret.age")
	if err := os.WriteFile(srcAge, []byte(plaintext), 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return &restoreFixture{
		runner:   exec.NewRunner(false, logger),
		identity: identity,
		srcAge:   srcAge,
		dest:     filepath.Join(dir, "restored", "secret"),
	}
}

func confirmAlways(bool) func(string) (bool, error) {
	return func(string) (bool, error) { return true, nil }
}

func confirmNever() func(string) (bool, error) {
	return func(string) (bool, error) { return false, nil }
}

func confirmFatal(t *testing.T) func(string) (bool, error) {
	return func(string) (bool, error) {
		t.Fatal("confirm must not be called")
		return false, nil
	}
}

func globBackups(t *testing.T, dest string) []string {
	t.Helper()
	matches, err := filepath.Glob(dest + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func globTempLitter(t *testing.T, dest string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".restore-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestRestoreSecretFile_NewDest(t *testing.T) {
	stubAge(t, false)
	f := newRestoreFixture(t, "secret-key-bytes")

	status, backup, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err != nil {
		t.Fatalf("restoreSecretFile: %v", err)
	}
	if status != restoreWritten {
		t.Errorf("status = %d, want restoreWritten", status)
	}
	if backup != "" {
		t.Errorf("backup = %q, want empty for new dest", backup)
	}

	data, err := os.ReadFile(f.dest)
	if err != nil {
		t.Fatalf("reading restored file: %v", err)
	}
	if string(data) != "secret-key-bytes" {
		t.Errorf("content = %q", string(data))
	}
	info, _ := os.Stat(f.dest)
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if got := globBackups(t, f.dest); len(got) != 0 {
		t.Errorf("unexpected backups: %v", got)
	}
	if got := globTempLitter(t, f.dest); len(got) != 0 {
		t.Errorf("temp litter left behind: %v", got)
	}
}

func TestRestoreSecretFile_BackupOnOverwrite(t *testing.T) {
	stubAge(t, false)
	f := newRestoreFixture(t, "new-content")
	if err := os.MkdirAll(filepath.Dir(f.dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.dest, []byte("old-content"), 0600); err != nil {
		t.Fatal(err)
	}

	status, backup, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmAlways(true))
	if err != nil {
		t.Fatalf("restoreSecretFile: %v", err)
	}
	if status != restoreWritten {
		t.Errorf("status = %d, want restoreWritten", status)
	}

	data, _ := os.ReadFile(f.dest)
	if string(data) != "new-content" {
		t.Errorf("dest = %q, want new content", string(data))
	}
	backups := globBackups(t, f.dest)
	if len(backups) != 1 || backups[0] != backup {
		t.Fatalf("backups = %v, returned %q", backups, backup)
	}
	old, _ := os.ReadFile(backup)
	if string(old) != "old-content" {
		t.Errorf("backup content = %q, want old content", string(old))
	}
	info, _ := os.Stat(backup)
	if info.Mode().Perm() != 0600 {
		t.Errorf("backup mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestRestoreSecretFile_DeclinedOverwrite(t *testing.T) {
	stubAge(t, false)
	f := newRestoreFixture(t, "new-content")
	if err := os.MkdirAll(filepath.Dir(f.dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.dest, []byte("old-content"), 0600); err != nil {
		t.Fatal(err)
	}

	status, backup, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmNever())
	if err != nil {
		t.Fatalf("restoreSecretFile: %v", err)
	}
	if status != restoreSkipped {
		t.Errorf("status = %d, want restoreSkipped", status)
	}
	if backup != "" {
		t.Errorf("backup = %q, want empty", backup)
	}
	data, _ := os.ReadFile(f.dest)
	if string(data) != "old-content" {
		t.Errorf("dest changed despite declined overwrite: %q", string(data))
	}
}

func TestRestoreSecretFile_FailedDecryptLeavesDestIntact(t *testing.T) {
	stubAge(t, true)
	f := newRestoreFixture(t, "new-content")
	if err := os.MkdirAll(filepath.Dir(f.dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.dest, []byte("old-content"), 0600); err != nil {
		t.Fatal(err)
	}

	_, _, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err == nil {
		t.Fatal("expected error from failed decrypt")
	}
	if !strings.Contains(err.Error(), "untouched") {
		t.Errorf("error should state dest was untouched: %v", err)
	}
	data, _ := os.ReadFile(f.dest)
	if string(data) != "old-content" {
		t.Errorf("dest corrupted by failed decrypt: %q", string(data))
	}
	if got := globBackups(t, f.dest); len(got) != 0 {
		t.Errorf("unexpected backups: %v", got)
	}
	if got := globTempLitter(t, f.dest); len(got) != 0 {
		t.Errorf("temp litter left behind: %v", got)
	}
}

func TestRestoreSecretFile_UnchangedSkipsBackup(t *testing.T) {
	stubAge(t, false)
	f := newRestoreFixture(t, "same-content")
	if err := os.MkdirAll(filepath.Dir(f.dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.dest, []byte("same-content"), 0600); err != nil {
		t.Fatal(err)
	}

	status, backup, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err != nil {
		t.Fatalf("restoreSecretFile: %v", err)
	}
	if status != restoreUnchanged {
		t.Errorf("status = %d, want restoreUnchanged", status)
	}
	if backup != "" || len(globBackups(t, f.dest)) != 0 {
		t.Error("identical content must not create a backup")
	}
}

func TestRestoreSecretFile_EmptyPlaintextRestores(t *testing.T) {
	// A genuinely empty secret (e.g. an empty 90-secrets.sh that was
	// backed up) must restore faithfully — real decrypt failures exit
	// non-zero and are caught before this point.
	stubAge(t, false)
	f := newRestoreFixture(t, "") // stub copies the empty source → empty output

	status, _, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err != nil {
		t.Fatalf("restoreSecretFile: %v", err)
	}
	if status != restoreWritten {
		t.Errorf("status = %d, want restoreWritten", status)
	}
	info, err := os.Stat(f.dest)
	if err != nil {
		t.Fatalf("empty secret should be restored: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
}

func TestRestoreSecretFile_UnchangedHealsPermissions(t *testing.T) {
	stubAge(t, false)
	f := newRestoreFixture(t, "same-content")
	if err := os.MkdirAll(filepath.Dir(f.dest), 0700); err != nil {
		t.Fatal(err)
	}
	// Identical content, but permissions drifted to group/world-readable.
	if err := os.WriteFile(f.dest, []byte("same-content"), 0644); err != nil {
		t.Fatal(err)
	}

	status, _, err := restoreSecretFile(context.Background(), f.runner,
		f.identity, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err != nil {
		t.Fatalf("restoreSecretFile: %v", err)
	}
	if status != restoreUnchanged {
		t.Errorf("status = %d, want restoreUnchanged", status)
	}
	info, _ := os.Stat(f.dest)
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want drifted permissions healed to 0600", info.Mode().Perm())
	}
}

func TestRestoreSecretFile_DryRun(t *testing.T) {
	f := newRestoreFixture(t, "content")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dryRunner := exec.NewRunner(true, logger)

	status, _, err := restoreSecretFile(context.Background(), dryRunner,
		f.identity, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if status != restoreWritten {
		t.Errorf("status = %d, want restoreWritten", status)
	}
	if _, statErr := os.Stat(f.dest); !os.IsNotExist(statErr) {
		t.Error("dry-run must not touch the filesystem")
	}
}

func TestRestoreSecretFile_MissingIdentity(t *testing.T) {
	stubAge(t, false)
	f := newRestoreFixture(t, "content")
	missing := f.identity + ".missing"

	_, _, err := restoreSecretFile(context.Background(), f.runner,
		missing, f.srcAge, f.dest, 0700, confirmFatal(t))
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err = %v, want missing-identity error naming the path", err)
	}
}

func TestBackupTimestamp_FilesystemSafe(t *testing.T) {
	if ts := backupTimestamp(); strings.ContainsRune(ts, ':') {
		t.Errorf("timestamp %q contains ':'", ts)
	}
}

// stubAgeEncryptOnly installs an age stub where encrypt copies the source
// but decrypt always fails — the shape of a typo'd recipient: archives are
// produced fine and only the round-trip check can catch them.
func stubAgeEncryptOnly(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"-e\" ]; then cat \"$6\" > \"$5\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "age"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestEncryptSecretFile_FailureLeavesDestUntouched(t *testing.T) {
	stubAge(t, true) // every age call fails
	dir := t.TempDir()
	src := filepath.Join(dir, "plain")
	dest := filepath.Join(dir, "store", "plain.age")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("previous-good-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(false, logger)

	err := encryptSecretFile(context.Background(), runner, []string{"-r", "age1x"}, src, dest, nil)
	if err == nil {
		t.Fatal("expected encryption failure")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "previous-good-archive" {
		t.Errorf("previous archive corrupted: %q", got)
	}
	litter, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".*enc-*"))
	if len(litter) != 0 {
		t.Errorf("temp litter left behind: %v", litter)
	}
}

func TestEncryptSecretFile_DryRunTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plain")
	dest := filepath.Join(dir, "store", "plain.age")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("previous-good-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dryRunner := exec.NewRunner(true, logger)

	if err := encryptSecretFile(context.Background(), dryRunner, []string{"-r", "age1x"}, src, dest, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "previous-good-archive" {
		t.Errorf("dry-run replaced the archive: %q", got)
	}
}

func TestCopySecretArchive_TightensPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.age")
	dst := filepath.Join(dir, "out", "a.age")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := copySecretArchive(exec.NewRunner(false, logger), src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup copy mode = %v, want 0600", info.Mode().Perm())
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "payload" {
		t.Errorf("content = %q", got)
	}
}

func TestSecretsRestoreFiles_ReportsUnmatchedArchives(t *testing.T) {
	stubAge(t, false)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	for _, name := range []string{"id_ed25519.age", "90-secrets.sh.age", "id_rsa.age"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("payload-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(false, logger)
	p := &Printer{Out: os.Stderr, Err: os.Stderr}

	result, err := secretsRestoreFiles(context.Background(), runner, p, &config.UserState{}, home, src, true)
	if err != nil {
		t.Fatal(err)
	}
	// id_ed25519.age restores onto the identical identity file content?
	// No — stub writes "payload-id_ed25519.age", differing → overwrite (unattended=true).
	if result.Restored != 2 {
		t.Errorf("Restored = %d, want 2", result.Restored)
	}
	if len(result.Unmatched) != 1 || result.Unmatched[0] != "id_rsa.age" {
		t.Errorf("Unmatched = %v, want [id_rsa.age]", result.Unmatched)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "shell", "90-secrets.sh")); err != nil {
		t.Errorf("shell secrets not restored: %v", err)
	}
}

func TestSSHKeyNameRejectsPathSeparators(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "..", "."} {
		state := &config.UserState{}
		state.SSH.KeyName = bad
		if _, err := sshKeyName(state); err == nil {
			t.Errorf("sshKeyName(%q) should fail", bad)
		}
	}
	state := &config.UserState{}
	state.SSH.KeyName = "id_rsa"
	if name, err := sshKeyName(state); err != nil || name != "id_rsa" {
		t.Errorf("sshKeyName(id_rsa) = %q, %v", name, err)
	}
}

func TestSecretsBackupCLI_DryRunCreatesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	store := filepath.Join(home, ".local", "share", "dotfiles-secrets")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "x.age"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, "backup-dest")

	out, errOut, err := runDotForTest("secrets", "backup", dest, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run backup: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dry-run created the destination dir: %v", err)
	}
}

func TestSecretsInitCLI_VerificationFailureLeavesStoreClean(t *testing.T) {
	stubAgeEncryptOnly(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// State with a (typo'd) recipient; identity is a native age key so the
	// round-trip check runs without ssh-keygen passphrase probing.
	writeCLITestFile(t, filepath.Join(home, ".config", "dotfiles", "config.yaml"),
		"secrets:\n  age_recipients: [\"age1typo\"]\n  age_identity: ~/.ssh/age_key\n")
	writeCLITestFile(t, filepath.Join(home, ".ssh", "age_key"), "AGE-SECRET-KEY-1TEST")
	writeCLITestFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "ssh-key-material")

	out, errOut, err := runDotForTest("secrets", "init")
	if err == nil {
		t.Fatalf("expected round-trip verification failure\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "verification") {
		t.Errorf("error should mention verification: %v", err)
	}
	store := filepath.Join(home, ".local", "share", "dotfiles-secrets")
	entries, _ := os.ReadDir(store)
	for _, e := range entries {
		t.Errorf("store should be clean after failed verification, found %s", e.Name())
	}
}

func TestSecretsInitCLI_RoundtripVerifiedSuccess(t *testing.T) {
	stubAge(t, false) // encrypt and decrypt both "work"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	writeCLITestFile(t, filepath.Join(home, ".config", "dotfiles", "config.yaml"),
		"secrets:\n  age_recipients: [\"age1good\"]\n  age_identity: ~/.ssh/age_key\n")
	writeCLITestFile(t, filepath.Join(home, ".ssh", "age_key"), "AGE-SECRET-KEY-1TEST")
	writeCLITestFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "ssh-key-material")

	out, errOut, err := runDotForTest("secrets", "init")
	if err != nil {
		t.Fatalf("init: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	archive := filepath.Join(home, ".local", "share", "dotfiles-secrets", "id_ed25519.age")
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	if string(got) != "ssh-key-material" {
		t.Errorf("stub-encrypted archive content = %q", got)
	}
	litter, _ := filepath.Glob(filepath.Join(home, ".local", "share", "dotfiles-secrets", ".*"))
	if len(litter) != 0 {
		t.Errorf("temp litter in store: %v", litter)
	}
}

func TestSecretsInitCLI_MissingIdentitySkipsVerification(t *testing.T) {
	stubAge(t, false)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Identity points at a path that doesn't exist yet (fresh machine):
	// init must warn, skip verification, and still encrypt.
	writeCLITestFile(t, filepath.Join(home, ".config", "dotfiles", "config.yaml"),
		"secrets:\n  age_recipients: [\"age1good\"]\n  age_identity: ~/.ssh/missing_age_key\n")
	writeCLITestFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "ssh-key-material")

	out, errOut, err := runDotForTest("secrets", "init")
	if err != nil {
		t.Fatalf("init must succeed with verification skipped: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "verification skipped") || !strings.Contains(errOut, "not found") {
		t.Errorf("missing skip warning on stderr:\nstdout=%s\nstderr=%s", out, errOut)
	}
	archive := filepath.Join(home, ".local", "share", "dotfiles-secrets", "id_ed25519.age")
	got, err := os.ReadFile(archive)
	if err != nil || string(got) != "ssh-key-material" {
		t.Errorf("archive not written despite skipped verification: %q err=%v", got, err)
	}
}

// stubSSHKeygen installs an ssh-keygen stub that exits with the given code,
// simulating a passphrase-protected (exit 1) or unprotected (exit 0) key.
func stubSSHKeygen(t *testing.T, exitCode int) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nexit %d\n", exitCode)
	if err := os.WriteFile(filepath.Join(bin, "ssh-keygen"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSecretsVerifier_PassphraseProtectedIdentitySkips(t *testing.T) {
	stubSSHKeygen(t, 1) // ssh-keygen -y -P "" fails → passphrase-protected
	dir := t.TempDir()
	identity := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(identity, []byte("-----BEGIN OPENSSH PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(false, logger)

	verify, reason := secretsVerifier(context.Background(), runner, identity)
	if verify != nil || !strings.Contains(reason, "passphrase-protected") {
		t.Errorf("verify=%v reason=%q, want skip with passphrase reason", verify != nil, reason)
	}
}

func TestSecretsVerifier_UnprotectedSSHIdentityVerifies(t *testing.T) {
	stubSSHKeygen(t, 0)
	dir := t.TempDir()
	identity := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(identity, []byte("-----BEGIN OPENSSH PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := exec.NewRunner(false, logger)

	verify, reason := secretsVerifier(context.Background(), runner, identity)
	if verify == nil || reason != "" {
		t.Errorf("verify=%v reason=%q, want a usable verifier", verify != nil, reason)
	}
}
