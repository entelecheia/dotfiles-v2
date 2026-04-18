package cli

import (
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
)

const secretsDir = ".local/share/dotfiles-secrets"

const shellSecretsTemplate = `# Shell secrets — sourced by zsh at login via zshrc.
# Add environment exports for API keys, tokens, and other secrets.
# This file is encrypted by 'dotfiles secrets init' into
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
		Long:  "Encrypt, backup, restore, and inspect dotfiles secrets using age.",
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

			storeDir, err := secretsStorePath()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(storeDir, 0700); err != nil {
				return fmt.Errorf("creating secrets dir: %w", err)
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)

			if !runner.CommandExists("age") {
				return fmt.Errorf("age is not installed — run 'dotfiles apply' to install it")
			}

			// Build common recipient args.
			recipientArgs := make([]string, 0, len(state.Secrets.AgeRecipients)*2)
			for _, r := range state.Secrets.AgeRecipients {
				recipientArgs = append(recipientArgs, "-r", r)
			}

			// Encrypt SSH private key.
			keyName := state.SSH.KeyName
			if keyName == "" {
				keyName = "id_ed25519"
			}
			sshKeyPath := filepath.Join(home, ".ssh", keyName)
			if runner.FileExists(sshKeyPath) {
				dest := filepath.Join(storeDir, keyName+".age")
				args := append([]string{"-e"}, recipientArgs...)
				args = append(args, "-o", dest, sshKeyPath)
				if _, err := runner.Run(ctx, "age", args...); err != nil {
					return fmt.Errorf("encrypting SSH key: %w", err)
				}
				p.Line("  Encrypted: %s -> %s", sshKeyPath, dest)
			} else {
				p.Line("  SSH key not found, skipping: %s", sshKeyPath)
			}

			// Encrypt shell secrets if they exist.
			shellSecrets := filepath.Join(home, ".config", "shell", "90-secrets.sh")
			if scaffold && !runner.FileExists(shellSecrets) {
				dryRun, _ := cmd.Flags().GetBool("dry-run")
				if dryRun {
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
			if runner.FileExists(shellSecrets) {
				dest := filepath.Join(storeDir, "90-secrets.sh.age")
				args := append([]string{"-e"}, recipientArgs...)
				args = append(args, "-o", dest, shellSecrets)
				if _, err := runner.Run(ctx, "age", args...); err != nil {
					return fmt.Errorf("encrypting shell secrets: %w", err)
				}
				p.Line("  Encrypted: %s -> %s", shellSecrets, dest)
			} else {
				p.Line("  Shell secrets not found, skipping: %s", shellSecrets)
			}

			p.Line("Done. Run 'dotfiles secrets list' to verify.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&scaffold, "scaffold", false, "Create empty ~/.config/shell/90-secrets.sh template (0600) if missing")
	return cmd
}

// newSecretsBackupCmd copies *.age files to a destination directory.
func newSecretsBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup <destination>",
		Short: "Copy encrypted secrets to a destination directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			dest := args[0]
			if err := os.MkdirAll(dest, 0700); err != nil {
				return fmt.Errorf("creating destination: %w", err)
			}

			storeDir, err := secretsStorePath()
			if err != nil {
				return err
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)

			entries, err := os.ReadDir(storeDir)
			if err != nil {
				if os.IsNotExist(err) {
					p.Line("No secrets store found. Run 'dotfiles secrets init' first.")
					return nil
				}
				return fmt.Errorf("reading secrets dir: %w", err)
			}

			copied := 0
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if filepath.Ext(e.Name()) != ".age" {
					continue
				}
				src := filepath.Join(storeDir, e.Name())
				dst := filepath.Join(dest, e.Name())
				if _, err := runner.Run(ctx, "cp", src, dst); err != nil {
					return fmt.Errorf("copying %s: %w", e.Name(), err)
				}
				p.Line("  Copied: %s", e.Name())
				copied++
			}

			if copied == 0 {
				p.Line("No .age files found to backup.")
				return nil
			}
			p.Line("Backup complete: %d file(s) -> %s", copied, dest)

			// Record last-backup location (skip in dry-run — runner.Run didn't copy).
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if dryRun {
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
func newSecretsRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <source>",
		Short: "Decrypt secrets from a source directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			src := args[0]

			state, err := config.LoadState()
			if err != nil {
				return fmt.Errorf("loading state: %w", err)
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}

			identity := state.Secrets.AgeIdentity
			if identity == "" {
				identity = filepath.Join(home, ".ssh", "id_ed25519")
			}
			// Expand tilde in identity path.
			if strings.HasPrefix(identity, "~/") {
				identity = filepath.Join(home, identity[2:])
			} else if identity == "~" {
				identity = home
			}

			runner := secretsRunner(cmd)
			p := printerFrom(cmd)

			if !runner.CommandExists("age") {
				return fmt.Errorf("age is not installed — run 'dotfiles apply' to install it")
			}

			keyName := state.SSH.KeyName
			if keyName == "" {
				keyName = "id_ed25519"
			}

			// Restore SSH private key.
			sshAgeSrc := filepath.Join(src, keyName+".age")
			if runner.FileExists(sshAgeSrc) {
				sshDest := filepath.Join(home, ".ssh", keyName)
				if err := os.MkdirAll(filepath.Dir(sshDest), 0700); err != nil {
					return fmt.Errorf("creating .ssh dir: %w", err)
				}
				if _, err := runner.Run(ctx, "age", "-d", "-i", identity, "-o", sshDest, sshAgeSrc); err != nil {
					return fmt.Errorf("decrypting SSH key: %w", err)
				}
				if err := os.Chmod(sshDest, 0600); err != nil {
					return fmt.Errorf("setting SSH key permissions: %w", err)
				}
				p.Line("  Restored: %s", sshDest)
			} else {
				p.Line("  SSH key archive not found, skipping: %s", sshAgeSrc)
			}

			// Restore shell secrets.
			shellAgeSrc := filepath.Join(src, "90-secrets.sh.age")
			if runner.FileExists(shellAgeSrc) {
				shellDest := filepath.Join(home, ".config", "shell", "90-secrets.sh")
				if err := os.MkdirAll(filepath.Dir(shellDest), 0755); err != nil {
					return fmt.Errorf("creating shell config dir: %w", err)
				}
				if _, err := runner.Run(ctx, "age", "-d", "-i", identity, "-o", shellDest, shellAgeSrc); err != nil {
					return fmt.Errorf("decrypting shell secrets: %w", err)
				}
				if err := os.Chmod(shellDest, 0600); err != nil {
					return fmt.Errorf("setting shell secrets permissions: %w", err)
				}
				p.Line("  Restored: %s", shellDest)
			} else {
				p.Line("  Shell secrets archive not found, skipping: %s", shellAgeSrc)
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

			keyName := state.SSH.KeyName
			if keyName == "" {
				keyName = "id_ed25519"
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
			checkFile("SSH key (~/.ssh/"+keyName+")", filepath.Join(home, ".ssh", keyName))
			checkFile("Shell secrets", filepath.Join(home, ".config", "shell", "90-secrets.sh"))

			p.Line("")
			p.Line("Encrypted files:")
			checkFile(keyName+".age", filepath.Join(storeDir, keyName+".age"))
			checkFile("90-secrets.sh.age", filepath.Join(storeDir, "90-secrets.sh.age"))

			p.Line("")
			identity := state.Secrets.AgeIdentity
			if identity == "" {
				identity = filepath.Join(home, ".ssh", "id_ed25519")
			}
			p.Line("  Age identity: %s", identity)
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
					p.Line("No secrets store found. Run 'dotfiles secrets init' first.")
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
