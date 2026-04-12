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
				fmt.Printf("  Encrypted: %s -> %s\n", sshKeyPath, dest)
			} else {
				fmt.Printf("  SSH key not found, skipping: %s\n", sshKeyPath)
			}

			// Encrypt shell secrets if they exist.
			shellSecrets := filepath.Join(home, ".config", "shell", "90-secrets.sh")
			if scaffold && !runner.FileExists(shellSecrets) {
				dryRun, _ := cmd.Flags().GetBool("dry-run")
				if dryRun {
					fmt.Printf("  [dry-run] Would scaffold: %s (0600)\n", shellSecrets)
				} else {
					if err := os.MkdirAll(filepath.Dir(shellSecrets), 0755); err != nil {
						return fmt.Errorf("creating shell config dir: %w", err)
					}
					if err := os.WriteFile(shellSecrets, []byte(shellSecretsTemplate), 0600); err != nil {
						return fmt.Errorf("scaffolding shell secrets: %w", err)
					}
					fmt.Printf("  Scaffolded: %s (0600)\n", shellSecrets)
				}
			}
			if runner.FileExists(shellSecrets) {
				dest := filepath.Join(storeDir, "90-secrets.sh.age")
				args := append([]string{"-e"}, recipientArgs...)
				args = append(args, "-o", dest, shellSecrets)
				if _, err := runner.Run(ctx, "age", args...); err != nil {
					return fmt.Errorf("encrypting shell secrets: %w", err)
				}
				fmt.Printf("  Encrypted: %s -> %s\n", shellSecrets, dest)
			} else {
				fmt.Printf("  Shell secrets not found, skipping: %s\n", shellSecrets)
			}

			fmt.Println("Done. Run 'dotfiles secrets list' to verify.")
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

			entries, err := os.ReadDir(storeDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No secrets store found. Run 'dotfiles secrets init' first.")
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
				fmt.Printf("  Copied: %s\n", e.Name())
				copied++
			}

			if copied == 0 {
				fmt.Println("No .age files found to backup.")
				return nil
			}
			fmt.Printf("Backup complete: %d file(s) -> %s\n", copied, dest)

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
				fmt.Fprintf(os.Stderr, "warning: could not load state to record backup: %v\n", err)
				return nil
			}
			state.Secrets.LastBackup = &config.BackupRecord{
				Path:  absDest,
				Time:  time.Now(),
				Files: copied,
			}
			if err := config.SaveState(state); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save last-backup record: %v\n", err)
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
				fmt.Printf("  Restored: %s\n", sshDest)
			} else {
				fmt.Printf("  SSH key archive not found, skipping: %s\n", sshAgeSrc)
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
				fmt.Printf("  Restored: %s\n", shellDest)
			} else {
				fmt.Printf("  Shell secrets archive not found, skipping: %s\n", shellAgeSrc)
			}

			fmt.Println("Restore complete.")
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
			checkFile := func(label, path string) {
				exists := runner.FileExists(path)
				mark := "missing"
				if exists {
					mark = "present"
				}
				fmt.Printf("  %-30s  %s\n", label, mark)
			}

			fmt.Println("Plaintext files:")
			checkFile("SSH key (~/.ssh/"+keyName+")", filepath.Join(home, ".ssh", keyName))
			checkFile("Shell secrets", filepath.Join(home, ".config", "shell", "90-secrets.sh"))

			fmt.Println()
			fmt.Println("Encrypted files:")
			checkFile(keyName+".age", filepath.Join(storeDir, keyName+".age"))
			checkFile("90-secrets.sh.age", filepath.Join(storeDir, "90-secrets.sh.age"))

			fmt.Println()
			identity := state.Secrets.AgeIdentity
			if identity == "" {
				identity = filepath.Join(home, ".ssh", "id_ed25519")
			}
			fmt.Printf("  Age identity: %s\n", identity)
			if len(state.Secrets.AgeRecipients) > 0 {
				fmt.Println("  Age recipients:")
				for _, r := range state.Secrets.AgeRecipients {
					fmt.Printf("    %s\n", r)
				}
			} else {
				fmt.Println("  Age recipients: (none configured)")
			}

			fmt.Println()
			if lb := state.Secrets.LastBackup; lb != nil && lb.Path != "" {
				fmt.Println("Last backup:")
				fmt.Printf("  Path:  %s\n", lb.Path)
				fmt.Printf("  When:  %s (%s ago)\n", lb.Time.Format(time.RFC3339), humanDuration(time.Since(lb.Time)))
				fmt.Printf("  Files: %d\n", lb.Files)
			} else {
				fmt.Println("Last backup: (none recorded)")
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
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No secrets store found. Run 'dotfiles secrets init' first.")
					return nil
				}
				return fmt.Errorf("reading secrets dir: %w", err)
			}

			fmt.Printf("Secrets store: %s\n\n", storeDir)
			found := 0
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				fmt.Printf("  %-30s  %d bytes\n", e.Name(), info.Size())
				found++
			}
			if found == 0 {
				fmt.Println("  (empty)")
			}
			return nil
		},
	}
}
