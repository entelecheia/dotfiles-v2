package cli

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

const secretsDir = ".local/share/dotfiles-secrets"

const shellSecretsTemplate = `# Shell secrets — sourced by zsh at login via zshrc.
# Add environment exports for API keys, tokens, and other secrets.
# This file is encrypted by 'dot secrets init' into
#   ~/.local/share/dotfiles-secrets/90-secrets.sh.age
# Never commit the plaintext version to git or sync it to Drive.
#
# export OPENAI_API_KEY=sk-...
# export ANTHROPIC_API_KEY=sk-...
# export GITHUB_TOKEN=ghp_...
`

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage encrypted secrets",
		Long:  "Encrypt, backup, restore, and inspect dot secrets using age.",
	}

	cmd.AddCommand(newSecretsInitCmd())
	cmd.AddCommand(newSecretsBackupCmd())
	cmd.AddCommand(newSecretsRestoreCmd())
	cmd.AddCommand(newSecretsStatusCmd())
	cmd.AddCommand(newSecretsListCmd())

	return cmd
}

// secretsRunner returns a runner suitable for secrets operations.
// Honors the global --dry-run flag: when dry-run, commands are printed but not executed.
func secretsRunner(cmd *cobra.Command) *exec.Runner {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return exec.NewRunner(dryRun, logger)
}

func secretsStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, secretsDir), nil
}

// sshKeyName returns the configured SSH key name, rejecting values that
// would escape ~/.ssh or the secrets store when interpolated into paths.
func sshKeyName(state *config.UserState) (string, error) {
	keyName := state.SSH.KeyName
	if keyName == "" {
		return "id_ed25519", nil
	}
	if strings.ContainsAny(keyName, "/\\") || keyName == "." || keyName == ".." {
		return "", fmt.Errorf("invalid ssh.key_name %q: must be a bare file name", keyName)
	}
	return keyName, nil
}

// resolveAgeIdentity returns the age identity path (default
// ~/.ssh/id_ed25519) with a leading ~ expanded.
func resolveAgeIdentity(state *config.UserState, home string) string {
	identity := state.Secrets.AgeIdentity
	if identity == "" {
		return filepath.Join(home, ".ssh", "id_ed25519")
	}
	if strings.HasPrefix(identity, "~/") {
		return filepath.Join(home, identity[2:])
	}
	if identity == "~" {
		return home
	}
	return identity
}

// secretEntry maps one encrypted archive name to its plaintext location.
type secretEntry struct {
	Label   string      // human-readable name for reports
	AgeName string      // file name inside the store / backup dir
	Plain   string      // plaintext path (encrypt source, restore dest)
	DirPerm os.FileMode // permission for the plaintext parent dir
}

// secretEntries is the single source of truth for which files `dot
// secrets` manages — init, restore, and status all derive from it.
func secretEntries(state *config.UserState, home string) ([]secretEntry, error) {
	keyName, err := sshKeyName(state)
	if err != nil {
		return nil, err
	}
	return []secretEntry{
		{
			Label:   "SSH key",
			AgeName: keyName + ".age",
			Plain:   filepath.Join(home, ".ssh", keyName),
			DirPerm: 0o700,
		},
		{
			Label:   "Shell secrets",
			AgeName: "90-secrets.sh.age",
			Plain:   filepath.Join(home, ".config", "shell", "90-secrets.sh"),
			DirPerm: 0o755,
		},
	}, nil
}

// encryptSecretFile encrypts src to dest without ever truncating an
// existing dest on failure: age writes into a 0600 temp file in the store
// directory, the result is optionally round-trip verified with verify, and
// only then renamed over dest. The dry-run guard comes first — otherwise
// the temp/rename dance would clobber a good archive with an empty file
// while runner.Run no-ops.
func encryptSecretFile(
	ctx context.Context,
	runner *exec.Runner,
	recipientArgs []string,
	src, dest string,
	verify func(agePath string) error,
) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: would encrypt", "src", src, "dest", dest)
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".enc-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	defer os.Remove(tmpPath) // no-op after a successful rename

	args := append([]string{"-e"}, recipientArgs...)
	args = append(args, "-o", tmpPath, src)
	if _, err := runner.Run(ctx, "age", args...); err != nil {
		return fmt.Errorf("encrypting %s: %w (existing %s untouched)", src, err, dest)
	}
	if verify != nil {
		if err := verify(tmpPath); err != nil {
			return fmt.Errorf("round-trip verification of %s failed: %w (existing %s untouched)", src, err, dest)
		}
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("replacing %s: %w", dest, err)
	}
	return nil
}

// secretsVerifier builds the round-trip check used at encrypt time: it
// proves the configured identity can decrypt what the configured
// recipients produced, so undecryptable archives are caught while the
// plaintext still exists — not at restore time. Returns (nil, reason) when
// verification must be skipped (missing or passphrase-protected identity,
// which age would prompt for on /dev/tty and appear to hang).
func secretsVerifier(ctx context.Context, runner *exec.Runner, identity string) (func(string) error, string) {
	if runner.DryRun {
		return nil, ""
	}
	if !runner.FileExists(identity) {
		return nil, fmt.Sprintf("age identity %s not found", identity)
	}
	if head, err := os.ReadFile(identity); err == nil && !bytes.Contains(head, []byte("AGE-SECRET-KEY-")) {
		// SSH identity: detect passphrase protection without prompting.
		if runner.CommandExists("ssh-keygen") {
			if _, err := runner.RunQuery(ctx, "ssh-keygen", "-y", "-P", "", "-f", identity); err != nil {
				return nil, fmt.Sprintf("identity %s appears passphrase-protected", identity)
			}
		}
	}
	return func(agePath string) error {
		out, err := os.CreateTemp(filepath.Dir(agePath), ".verify-*")
		if err != nil {
			return err
		}
		outPath := out.Name()
		if err := out.Close(); err != nil {
			return err
		}
		defer os.Remove(outPath)
		_, err = runner.Run(ctx, "age", "-d", "-i", identity, "-o", outPath, agePath)
		return err
	}, ""
}

// newSecretsInitCmd encrypts local secrets files with age.
func newSecretsInitCmd() *cobra.Command {
	var scaffold bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Encrypt SSH key and shell secrets with age",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()

			state, err := config.LoadState()
			if err != nil {
				return fmt.Errorf("loading state: %w", err)
			}

			if len(state.Secrets.AgeRecipients) == 0 {
				return fmt.Errorf("no age recipients configured; set secrets.age_recipients in state")
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)

			storeDir, err := secretsStorePath()
			if err != nil {
				return err
			}
			if runner.DryRun {
				runner.Logger.Info("dry-run: would create secrets dir", "path", storeDir)
			} else if err := os.MkdirAll(storeDir, 0700); err != nil {
				return fmt.Errorf("creating secrets dir: %w", err)
			}

			if !runner.CommandExists("age") {
				return fmt.Errorf("age is not installed — run 'dot apply' to install it")
			}

			// Build common recipient args.
			recipientArgs := make([]string, 0, len(state.Secrets.AgeRecipients)*2)
			for _, r := range state.Secrets.AgeRecipients {
				recipientArgs = append(recipientArgs, "-r", r)
			}

			// Round-trip verification: a typo'd recipient produces archives
			// that encrypt fine and fail only at restore time, when the
			// plaintext may already be gone.
			verify, skipReason := secretsVerifier(ctx, runner, resolveAgeIdentity(state, home))
			if skipReason != "" {
				p.Warn("  decrypt verification skipped: %s — restore from this archive is unverified", skipReason)
			}

			entries, err := secretEntries(state, home)
			if err != nil {
				return err
			}

			// Optionally scaffold the shell secrets template first so the
			// entry loop below picks it up.
			shellSecrets := filepath.Join(home, ".config", "shell", "90-secrets.sh")
			if scaffold && !runner.FileExists(shellSecrets) {
				if runner.DryRun {
					p.Line("  [dry-run] Would scaffold: %s (0600)", shellSecrets)
				} else {
					if err := os.MkdirAll(filepath.Dir(shellSecrets), 0755); err != nil {
						return fmt.Errorf("creating shell config dir: %w", err)
					}
					if err := os.WriteFile(shellSecrets, []byte(shellSecretsTemplate), 0600); err != nil {
						return fmt.Errorf("scaffolding shell secrets: %w", err)
					}
					p.Line("  Scaffolded: %s (0600)", shellSecrets)
				}
			}

			for _, entry := range entries {
				if !runner.FileExists(entry.Plain) {
					p.Line("  %s not found, skipping: %s", entry.Label, entry.Plain)
					continue
				}
				dest := filepath.Join(storeDir, entry.AgeName)
				if err := encryptSecretFile(ctx, runner, recipientArgs, entry.Plain, dest, verify); err != nil {
					return fmt.Errorf("encrypting %s: %w", entry.Label, err)
				}
				p.Line("  Encrypted: %s -> %s", entry.Plain, dest)
			}

			p.Line("Done. Run 'dot secrets list' to verify.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&scaffold, "scaffold", false, "Create empty ~/.config/shell/90-secrets.sh template (0600) if missing")
	return cmd
}

// copySecretArchive copies one .age file atomically: read + 0600 temp +
// rename, so an interrupted copy can never truncate the previous (and
// possibly only) backup copy, and backup copies never inherit a loose
// mode from the store.
func copySecretArchive(runner *exec.Runner, src, dst string) error {
	if runner.DryRun {
		runner.Logger.Info("dry-run: would copy", "src", src, "dst", dst)
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".copy-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// secretsBackupFiles copies every *.age in the store to dest. Returns the
// number of files copied; (0, nil) when the store doesn't exist yet.
func secretsBackupFiles(runner *exec.Runner, p *Printer, storeDir, dest string) (int, error) {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading secrets dir: %w", err)
	}

	if runner.DryRun {
		runner.Logger.Info("dry-run: would create destination", "path", dest)
	} else if err := os.MkdirAll(dest, 0700); err != nil {
		return 0, fmt.Errorf("creating destination: %w", err)
	}

	copied := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".age" {
			continue
		}
		src := filepath.Join(storeDir, e.Name())
		dst := filepath.Join(dest, e.Name())
		if err := copySecretArchive(runner, src, dst); err != nil {
			return copied, fmt.Errorf("copying %s: %w", e.Name(), err)
		}
		p.Line("  Copied: %s", e.Name())
		copied++
	}
	return copied, nil
}

// newSecretsBackupCmd copies *.age files to a destination directory.
func newSecretsBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup <destination>",
		Short: "Copy encrypted secrets to a destination directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dest := args[0]
			storeDir, err := secretsStorePath()
			if err != nil {
				return err
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)

			if _, err := os.Stat(storeDir); os.IsNotExist(err) {
				p.Line("No secrets store found. Run 'dot secrets init' first.")
				return nil
			}

			copied, err := secretsBackupFiles(runner, p, storeDir, dest)
			if err != nil {
				return err
			}
			if copied == 0 {
				p.Line("No .age files found to backup.")
				return nil
			}
			p.Line("Backup complete: %d file(s) -> %s", copied, dest)

			// Record last-backup location (skip in dry-run — nothing was copied).
			if runner.DryRun {
				return nil
			}
			absDest, err := filepath.Abs(dest)
			if err != nil {
				absDest = dest
			}
			state, err := config.LoadState()
			if err != nil {
				p.Warn("warning: could not load state to record backup: %v", err)
				return nil
			}
			state.Secrets.LastBackup = &config.BackupRecord{
				Path:  absDest,
				Time:  time.Now(),
				Files: copied,
			}
			if err := config.SaveState(state); err != nil {
				p.Warn("warning: could not save last-backup record: %v", err)
			}
			return nil
		},
	}
}

// newSecretsRestoreCmd decrypts secrets from a source directory.
// restoreStatus reports what restoreSecretFile did.
type restoreStatus int

const (
	restoreWritten   restoreStatus = iota // dest created or replaced
	restoreUnchanged                      // decrypted content == existing dest
	restoreSkipped                        // user declined overwrite
)

// backupTimestamp returns a filesystem-safe RFC3339 timestamp (UTC, ':'→'-').
func backupTimestamp() string {
	return strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "-")
}

// restoreSecretFile decrypts srcAge to destPath without ever truncating an
// existing destPath on failure: it decrypts into a 0600 temp file in the
// destination directory and atomically renames it over destPath. When an
// existing, different destPath would be replaced, confirm is consulted once;
// on acceptance the old content is saved to destPath+".bak-<timestamp>".
func restoreSecretFile(
	ctx context.Context,
	runner *exec.Runner,
	identity, srcAge, destPath string,
	dirPerm os.FileMode,
	confirm func(dest string) (bool, error),
) (status restoreStatus, backupPath string, err error) {
	// Identity check before the dry-run short-circuit, so a dry-run preview
	// fails the same way the real restore would.
	if !runner.FileExists(identity) {
		return 0, "", fmt.Errorf("age identity not found: %s", identity)
	}

	if runner.DryRun {
		runner.Logger.Info("dry-run: would restore", "src", srcAge, "dest", destPath)
		return restoreWritten, "", nil
	}

	if err := os.MkdirAll(filepath.Dir(destPath), dirPerm); err != nil {
		return 0, "", fmt.Errorf("creating %s: %w", filepath.Dir(destPath), err)
	}

	// CreateTemp creates the file 0600, so the plaintext secret is never
	// world-readable, even transiently. age reopens it by path with
	// O_TRUNC, which preserves those permissions.
	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".restore-*")
	if err != nil {
		return 0, "", fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return 0, "", fmt.Errorf("closing temp file: %w", err)
	}
	defer os.Remove(tmpPath) // no-op after a successful rename

	if _, err := runner.Run(ctx, "age", "-d", "-i", identity, "-o", tmpPath, srcAge); err != nil {
		return 0, "", fmt.Errorf("decrypting %s: %w (existing %s untouched)", srcAge, err, destPath)
	}

	newData, err := os.ReadFile(tmpPath)
	if err != nil {
		return 0, "", fmt.Errorf("reading decrypted output: %w", err)
	}

	if oldData, err := os.ReadFile(destPath); err == nil {
		if bytes.Equal(oldData, newData) {
			// Still heal drifted permissions — ssh refuses
			// group/world-readable keys.
			if err := os.Chmod(destPath, 0600); err != nil {
				return 0, "", fmt.Errorf("restoring permissions on %s: %w", destPath, err)
			}
			return restoreUnchanged, "", nil
		}
		ok, err := confirm(destPath)
		if err != nil {
			return 0, "", err
		}
		if !ok {
			return restoreSkipped, "", nil
		}
		backupPath = destPath + ".bak-" + backupTimestamp()
		if err := os.WriteFile(backupPath, oldData, 0600); err != nil {
			return 0, "", fmt.Errorf("backing up %s: %w", destPath, err)
		}
	} else if !os.IsNotExist(err) {
		return 0, "", fmt.Errorf("reading existing %s: %w", destPath, err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, backupPath, fmt.Errorf("replacing %s: %w", destPath, err)
	}
	return restoreWritten, backupPath, nil
}

// secretsRestoreResult summarizes a secretsRestoreFiles run.
type secretsRestoreResult struct {
	Restored  int
	Unchanged int
	Skipped   int      // user declined overwrite
	Unmatched []string // *.age files in src that map to no known entry
}

// secretsRestoreFiles decrypts every known archive in src back to its
// plaintext location, then reports archives that matched no entry — those
// were backed up (backup copies all *.age) but cannot be restored without
// a matching entry, e.g. an SSH key from a host with a different
// ssh.key_name.
func secretsRestoreFiles(
	ctx context.Context,
	runner *exec.Runner,
	p *Printer,
	state *config.UserState,
	home, src string,
	unattended bool,
) (*secretsRestoreResult, error) {
	if !runner.CommandExists("age") {
		return nil, fmt.Errorf("age is not installed — run 'dot apply' to install it")
	}
	entries, err := secretEntries(state, home)
	if err != nil {
		return nil, err
	}
	identity := resolveAgeIdentity(state, home)

	confirm := func(dest string) (bool, error) {
		return ui.Confirm(fmt.Sprintf("%s exists and differs — overwrite? (a timestamped .bak copy will be saved)", dest), unattended)
	}

	result := &secretsRestoreResult{}
	known := make(map[string]bool, len(entries))
	for _, entry := range entries {
		known[entry.AgeName] = true
		ageSrc := filepath.Join(src, entry.AgeName)
		if !runner.FileExists(ageSrc) {
			p.Line("  %s archive not found, skipping: %s", entry.Label, ageSrc)
			continue
		}
		status, backup, err := restoreSecretFile(ctx, runner, identity, ageSrc, entry.Plain, entry.DirPerm, confirm)
		if err != nil {
			return result, fmt.Errorf("restoring %s: %w", entry.Label, err)
		}
		switch status {
		case restoreWritten:
			result.Restored++
			p.Line("  Restored: %s", entry.Plain)
			if backup != "" {
				p.Line("  Backup:   %s", backup)
			}
		case restoreUnchanged:
			result.Unchanged++
			p.Line("  Unchanged: %s", entry.Plain)
		case restoreSkipped:
			result.Skipped++
			p.Warn("  Skipped (declined overwrite): %s", entry.Plain)
		}
	}

	if dirEntries, err := os.ReadDir(src); err == nil {
		for _, e := range dirEntries {
			name := e.Name()
			if e.IsDir() || filepath.Ext(name) != ".age" || known[name] {
				continue
			}
			result.Unmatched = append(result.Unmatched, name)
		}
	}
	if len(result.Unmatched) > 0 {
		p.Warn("  %d archive(s) in %s matched no known secret and were NOT restored: %s",
			len(result.Unmatched), src, strings.Join(result.Unmatched, ", "))
		p.Warn("  (an SSH key from another host? set ssh.key_name accordingly and re-run restore)")
	}
	return result, nil
}

func newSecretsRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <source>",
		Short: "Decrypt secrets from a source directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			state, err := config.LoadState()
			if err != nil {
				return fmt.Errorf("loading state: %w", err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)
			yes, _ := cmd.Flags().GetBool("yes")

			if _, err := secretsRestoreFiles(ctx, runner, p, state, home, args[0], yes); err != nil {
				return err
			}
			p.Line("Restore complete.")
			return nil
		},
	}
}

// newSecretsStatusCmd checks whether plaintext and encrypted secrets files exist.
func newSecretsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check status of secrets files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			state, err := config.LoadState()
			if err != nil {
				return fmt.Errorf("loading state: %w", err)
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			storeDir, err := secretsStorePath()
			if err != nil {
				return err
			}

			entries, err := secretEntries(state, home)
			if err != nil {
				return err
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)
			checkFile := func(label, path string) {
				exists := runner.FileExists(path)
				mark := "missing"
				if exists {
					mark = "present"
				}
				p.Line("  %-30s  %s", label, mark)
			}

			p.Line("Plaintext files:")
			for _, entry := range entries {
				checkFile(entry.Label, entry.Plain)
			}

			p.Line("")
			p.Line("Encrypted files:")
			for _, entry := range entries {
				checkFile(entry.AgeName, filepath.Join(storeDir, entry.AgeName))
			}

			p.Line("")
			p.Line("  Age identity: %s", resolveAgeIdentity(state, home))
			if len(state.Secrets.AgeRecipients) > 0 {
				p.Line("  Age recipients:")
				for _, r := range state.Secrets.AgeRecipients {
					p.Line("    %s", r)
				}
			} else {
				p.Line("  Age recipients: (none configured)")
			}

			p.Line("")
			if lb := state.Secrets.LastBackup; lb != nil && lb.Path != "" {
				p.Line("Last backup:")
				p.Line("  Path:  %s", lb.Path)
				p.Line("  When:  %s (%s ago)", lb.Time.Format(time.RFC3339), humanDuration(time.Since(lb.Time)))
				p.Line("  Files: %d", lb.Files)
			} else {
				p.Line("Last backup: (none recorded)")
			}

			return nil
		},
	}
}

// humanDuration formats a duration as a short human-readable string.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// newSecretsListCmd lists encrypted files in the secrets store.
func newSecretsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List encrypted secrets files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			storeDir, err := secretsStorePath()
			if err != nil {
				return err
			}

			entries, err := os.ReadDir(storeDir)
			p := printerFrom(cmd)
			if err != nil {
				if os.IsNotExist(err) {
					p.Line("No secrets store found. Run 'dot secrets init' first.")
					return nil
				}
				return fmt.Errorf("reading secrets dir: %w", err)
			}

			p.Line("Secrets store: %s\n", storeDir)
			found := 0
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				p.Line("  %-30s  %d bytes", e.Name(), info.Size())
				found++
			}
			if found == 0 {
				p.Line("  (empty)")
			}
			return nil
		},
	}
}
