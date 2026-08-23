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
	"github.com/entelecheia/dotfiles-v2/internal/secrets"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

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

// renderSecretsEvent turns one engine step outcome into the line the report
// shows. Every string a secrets flow prints is written here, so the engine
// stays free of presentation and the three call sites (secrets, the one-stop
// backup, the one-stop restore) cannot drift apart.
func renderSecretsEvent(p *Printer) func(secrets.Event) {
	return func(ev secrets.Event) {
		switch ev.Kind {
		case secrets.EventVerificationSkipped:
			p.Warn("  decrypt verification skipped: %s — restore from this archive is unverified", ev.Reason)
		case secrets.EventScaffoldPreview:
			p.Line("  [dry-run] Would scaffold: %s (0600)", ev.Path)
		case secrets.EventScaffolded:
			p.Line("  Scaffolded: %s (0600)", ev.Path)
		case secrets.EventPlaintextMissing:
			p.Line("  %s not found, skipping: %s", ev.Label, ev.Path)
		case secrets.EventEncrypted:
			p.Line("  Encrypted: %s -> %s", ev.Path, ev.Dest)
		case secrets.EventArchiveCopied:
			p.Line("  Copied: %s", ev.Name)
		case secrets.EventArchiveMissing:
			p.Line("  %s archive not found, skipping: %s", ev.Label, ev.Path)
		case secrets.EventRestored:
			p.Line("  Restored: %s", ev.Path)
		case secrets.EventRestoreBackup:
			p.Line("  Backup:   %s", ev.Path)
		case secrets.EventRestoreUnchanged:
			p.Line("  Unchanged: %s", ev.Path)
		case secrets.EventRestoreSkipped:
			p.Warn("  Skipped (declined overwrite): %s", ev.Path)
		case secrets.EventUnmatchedArchives:
			p.Warn("  %d archive(s) in %s matched no known secret and were NOT restored: %s",
				len(ev.Names), ev.Path, strings.Join(ev.Names, ", "))
			p.Warn("  (an SSH key from another host? set ssh.key_name accordingly and re-run restore)")
		}
	}
}

// confirmSecrets answers the engine's yes/no questions. The wording and the
// unattended (--yes) policy live here, never in the engine (D-09).
func confirmSecrets(unattended bool) func(secrets.ConfirmKind, string) (bool, error) {
	return func(kind secrets.ConfirmKind, dest string) (bool, error) {
		if kind != secrets.ConfirmOverwriteRestore {
			return false, fmt.Errorf("unknown secrets confirmation %d", kind)
		}
		return ui.Confirm(fmt.Sprintf(
			"%s exists and differs — overwrite? (a timestamped .bak copy will be saved)", dest), unattended)
	}
}

// secretsSession is the home, state and store directory one `dot secrets`
// invocation operates on. Resolving them in one place is what keeps --home
// from reaching some subcommands and not others (BUG-08).
type secretsSession struct {
	// Home is the resolved target home: the override when one was given,
	// the process home otherwise.
	Home string
	// override is the raw --home / $DOTFILES_HOME value, empty for the
	// process home. It selects the state loader and saver, so a run under
	// the flag reads and records in the target's state file.
	override string
	State    *config.UserState
	// StoreDir is the encrypted store, joined from the store-relative
	// constant against Home. That is the same spelling the one-stop backup
	// and restore steps use (onestop.go:162), which is why they honored the
	// flag while these subcommands did not.
	StoreDir string
}

// secretsSessionFrom resolves the session for one command run: the flag, then
// $DOTFILES_HOME, then the process home, with state loaded from whichever it
// picked. The override outranks XDG_CONFIG_HOME, following
// config.StatePathForHome and the apply/check/diff precedent.
func secretsSessionFrom(cmd *cobra.Command) (*secretsSession, error) {
	s := &secretsSession{override: homeOverrideFrom(cmd)}
	s.Home = s.override
	if s.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		s.Home = home
	}
	state, err := s.loadState()
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	s.State = state
	s.StoreDir = filepath.Join(s.Home, secrets.StoreDirRel)
	return s, nil
}

// loadState reads the target home's state file. Backup re-reads after copying
// so a concurrent edit is not clobbered by the last-backup record.
func (s *secretsSession) loadState() (*config.UserState, error) {
	if s.override != "" {
		return config.LoadStateForHome(s.override)
	}
	return config.LoadState()
}

// saveState writes state back to the target home.
func (s *secretsSession) saveState(state *config.UserState) error {
	if s.override != "" {
		return config.SaveStateForHome(s.override, state)
	}
	return config.SaveState(state)
}

// newSecretsInitCmd encrypts local secrets files with age.
func newSecretsInitCmd() *cobra.Command {
	var scaffold bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Encrypt SSH key and shell secrets with age",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ses, err := secretsSessionFrom(cmd)
			if err != nil {
				return err
			}

			// Checked before any path lookup: a state with no recipients
			// cannot produce a decryptable archive under any of them.
			if len(ses.State.Secrets.AgeRecipients) == 0 {
				return fmt.Errorf("no age recipients configured; set secrets.age_recipients in state")
			}

			p := printerFrom(cmd)
			if err := secrets.Init(context.Background(), secrets.InitOptions{
				Runner:   secretsRunner(cmd),
				State:    ses.State,
				Home:     ses.Home,
				StoreDir: ses.StoreDir,
				Scaffold: scaffold,
				Progress: renderSecretsEvent(p),
			}); err != nil {
				return err
			}

			p.Line("Done. Run 'dot secrets list' to verify.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&scaffold, "scaffold", false, "Create empty ~/.config/shell/90-secrets.sh template (0600) if missing")
	return cmd
}

// newSecretsRestoreCmd decrypts secrets from a source directory.
func newSecretsRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <source>",
		Short: "Decrypt secrets from a source directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ses, err := secretsSessionFrom(cmd)
			if err != nil {
				return err
			}

			p := printerFrom(cmd)
			yes, _ := cmd.Flags().GetBool("yes")

			if _, err := secrets.Restore(context.Background(), secrets.RestoreOptions{
				Runner:   secretsRunner(cmd),
				State:    ses.State,
				Home:     ses.Home,
				Src:      args[0],
				Confirm:  confirmSecrets(yes),
				Progress: renderSecretsEvent(p),
			}); err != nil {
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
			ses, err := secretsSessionFrom(cmd)
			if err != nil {
				return err
			}

			res, err := secrets.Status(secrets.StatusOptions{
				Runner:   secretsRunner(cmd),
				State:    ses.State,
				Home:     ses.Home,
				StoreDir: ses.StoreDir,
			})
			if err != nil {
				return err
			}

			p := printerFrom(cmd)
			checkFile := func(f secrets.FileStatus) {
				mark := "missing"
				if f.Present {
					mark = "present"
				}
				p.Line("  %-30s  %s", f.Label, mark)
			}

			p.Line("Plaintext files:")
			for _, f := range res.Plaintext {
				checkFile(f)
			}

			p.Line("")
			p.Line("Encrypted files:")
			for _, f := range res.Encrypted {
				checkFile(f)
			}

			p.Line("")
			p.Line("  Age identity: %s", res.AgeIdentity)
			if len(ses.State.Secrets.AgeRecipients) > 0 {
				p.Line("  Age recipients:")
				for _, r := range ses.State.Secrets.AgeRecipients {
					p.Line("    %s", r)
				}
			} else {
				p.Line("  Age recipients: (none configured)")
			}

			p.Line("")
			if lb := ses.State.Secrets.LastBackup; lb != nil && lb.Path != "" {
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
			ses, err := secretsSessionFrom(cmd)
			if err != nil {
				return err
			}

			res, err := secrets.List(secrets.ListOptions{StoreDir: ses.StoreDir})
			p := printerFrom(cmd)
			if err != nil {
				return err
			}
			if res.Missing {
				p.Line("No secrets store found. Run 'dot secrets init' first.")
				return nil
			}

			p.Line("Secrets store: %s\n", ses.StoreDir)
			for _, e := range res.Entries {
				p.Line("  %-30s  %d bytes", e.Name, e.Size)
			}
			if len(res.Entries) == 0 {
				p.Line("  (empty)")
			}
			return nil
		},
	}
}
