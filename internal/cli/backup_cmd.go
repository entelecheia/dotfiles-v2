package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/appsettings"
	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/profilesnap"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// newBackupCmd returns the top-level `dot backup` one-stop wizard: an
// interactive Q&A that backs up everything dot manages — profile state,
// macOS app settings, AI/Maru settings, and encrypted secrets — into the
// shared backup root in one run.
func newBackupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "backup",
		Short: "One-stop interactive backup (profile, apps, AI, secrets)",
		Long: `Back up everything dot manages in one interactive run.

Domains:
  profile  config.yaml + install/backup lists (+ optional age keys)
           → <root>/profiles/<host>/<version>/
  apps     macOS app settings (plists, Application Support, containers)
           → <root>/app-settings/<host>/<token>/
  ai       AI CLI/agent settings + Maru settings
           → <root>/ai-config/<host>/<version>/
  secrets  encrypted .age archives from the local secrets store
           → <root>/secrets-age/<host>/

The wizard confirms the backup root, lets you pick domains, asks the
per-domain questions (age keys, AI auth tokens, tag), then runs every
selected domain and prints a ✓/✗ summary. Profile and AI snapshots share
one tag so they can be correlated later. Use --yes with --scope for
unattended runs.`,
		Args: cobra.NoArgs,
		RunE: runOnestopBackup,
	}
	c.Flags().String("to", "", "Backup root (overrides configured BackupRoot)")
	c.Flags().String("tag", "", "Shared label stored in profile/AI snapshot metadata")
	c.Flags().String("scope", "", "Comma-separated domains to back up (profile,apps,ai,secrets)")
	c.Flags().Bool("include-secrets", false, "Include ~/.ssh/age_key* in the profile snapshot")
	c.Flags().Bool("include-auth", false, "Include AI auth tokens in the AI snapshot")
	return c
}

func runOnestopBackup(cmd *cobra.Command, _ []string) error {
	o, err := newOnestopCtx(cmd)
	if err != nil {
		return err
	}
	p := o.p
	p.Header("One-stop Backup")

	// 1. Backup root.
	if err := o.confirmRoot(true); err != nil {
		return err
	}

	// 2. Availability scan → scope selection.
	var available []string
	labels := map[string]string{}
	if _, err := os.Stat(o.statePath()); err == nil {
		available = append(available, "profile")
		labels["profile"] = "profile — config.yaml + app lists (+ age keys)"
	}
	if runtime.GOOS == "darwin" {
		available = append(available, "apps")
		labels["apps"] = "apps — macOS app settings (plists, Application Support)"
	}
	available = append(available, "ai")
	labels["ai"] = "ai — AI CLI/agent settings + Maru settings"
	storeDir := o.secretsStoreDir()
	if hasAgeArchives(storeDir) {
		available = append(available, "secrets")
		labels["secrets"] = "secrets — encrypted .age archives"
	}

	scopes := available
	if raw, _ := cmd.Flags().GetString("scope"); raw != "" {
		scopes, err = parseScope(raw, available)
		if err != nil {
			return err
		}
	} else if !o.yes {
		opts := make([]ui.SelectOption, 0, len(available))
		for _, s := range available {
			opts = append(opts, ui.SelectOption{Label: labels[s], Value: s})
		}
		scopes, err = ui.MultiSelectLabeled("Select what to back up", opts, available, false)
		if err != nil {
			return err
		}
		if len(scopes) == 0 {
			p.Line("aborted (nothing selected)")
			return nil
		}
	}
	selected := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		selected[s] = true
	}

	// 3. Per-domain follow-ups.
	includeSecrets, _ := cmd.Flags().GetBool("include-secrets")
	if selected["profile"] && !o.yes {
		includeSecrets, err = ui.ConfirmBool("Include age keys (~/.ssh/age_key*) in the profile snapshot?", true, false)
		if err != nil {
			return err
		}
	}
	includeAuth, _ := cmd.Flags().GetBool("include-auth")
	if selected["ai"] {
		if isCloudPath(o.root) && (includeAuth || !o.yes) {
			p.Warn("  Backup root looks cloud-synced — AI auth tokens stored there sync to the cloud provider.")
		}
		if !o.yes {
			includeAuth, err = ui.ConfirmBool("Include AI auth tokens (OAuth/API credentials)?", includeAuth, false)
			if err != nil {
				return err
			}
		}
	}
	tag, _ := cmd.Flags().GetString("tag")
	if !o.yes {
		tag, err = ui.Input("Tag (label stored in snapshot metadata, empty = auto)", tag, false)
		if err != nil {
			return err
		}
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		tag = "onestop-" + time.Now().UTC().Format("20060102T150405Z")
	}

	// 4. Plan summary + confirmation.
	p.Section("Plan")
	p.KV("Root", o.root)
	p.KV("Host", o.host)
	p.KV("Scope", strings.Join(scopes, ", "))
	p.KV("Tag", tag)
	if selected["profile"] {
		p.KV("Age keys", fmt.Sprintf("%v", includeSecrets))
	}
	if selected["ai"] {
		p.KV("AI auth", fmt.Sprintf("%v", includeAuth))
	}
	if o.dryRun {
		p.KV("Mode", "dry-run")
	}
	ok, err := ui.Confirm("Proceed with backup?", o.yes)
	if err != nil {
		return err
	}
	if !ok {
		p.Line("aborted")
		return nil
	}

	// 5. Execute — steps are independent; a failure is recorded and the
	// remaining domains still run.
	var steps []onestopStep
	if selected["profile"] {
		steps = append(steps, o.backupProfileStep(tag, includeSecrets))
	}
	if selected["apps"] {
		steps = append(steps, o.backupAppsStep(tag))
	}
	if selected["ai"] {
		steps = append(steps, o.backupAIStep(tag, includeAuth))
	}
	if selected["secrets"] {
		steps = append(steps, o.backupSecretsStep(storeDir))
	}

	// 6. Outcome.
	if failed := printOnestopSummary(p, steps); failed > 0 {
		return fmt.Errorf("%d backup step(s) failed", failed)
	}
	p.Success("one-stop backup complete")
	return nil
}

// hasAgeArchives reports whether dir contains at least one *.age file.
func hasAgeArchives(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".age" {
			return true
		}
	}
	return false
}

func (o *onestopCtx) backupProfileStep(tag string, includeSecrets bool) onestopStep {
	eng := o.profileEngine()
	snap, err := eng.Backup(profilesnap.BackupOptions{Tag: tag, IncludeSecrets: includeSecrets})
	if err != nil {
		return onestopStep{Name: "profile", Err: err}
	}
	if includeSecrets && snap.SecretsCopied == 0 {
		o.p.Warn("  profile: age keys requested but no ~/.ssh/age_key* found — snapshot contains no secrets")
	}
	return onestopStep{Name: "profile", Detail: fmt.Sprintf("version %s → %s", snap.Version, snap.Path)}
}

func (o *onestopCtx) backupAppsStep(tag string) onestopStep {
	eng, err := o.appsEngine()
	if err != nil {
		return onestopStep{Name: "apps", Err: err}
	}
	// Mirror `dot apps backup --yes`: re-resolve saved display-name tokens
	// before the manifest intersection drops them.
	for _, t := range o.state.Modules.MacApps.BackupApps {
		if eng.Manifest.App(t) != nil {
			continue
		}
		if discovered := appsettings.DiscoverApp(eng.HomeDir, t); discovered != nil {
			eng.Manifest.Apps = append(eng.Manifest.Apps, *discovered)
		}
	}
	eng.AdoptArchivedApps()
	tokens := resolveBackupTokens(o.cmd, eng)
	if len(tokens) == 0 {
		return onestopStep{Name: "apps", Err: fmt.Errorf("nothing to back up")}
	}
	sum, err := eng.Backup(context.Background(), tokens)
	if err != nil {
		return onestopStep{Name: "apps", Err: err}
	}
	if sum.Failed > 0 {
		return onestopStep{Name: "apps", Err: fmt.Errorf("%d path(s) failed — their previous archive copies were kept (other paths refreshed)", sum.Failed)}
	}
	if !o.dryRun {
		o.state.Modules.MacApps.LastBackup = &config.BackupRecord{
			Path:  eng.HostRoot(),
			Time:  time.Now(),
			Files: sum.Files,
		}
		if err := persistUserState(o.cmd, o.state); err != nil {
			o.runner.Logger.Warn("record last backup", "err", err)
		}
		if err := eng.WriteLastBackupStamp(appsettings.BackupStamp{
			Tag:       tag,
			CreatedAt: time.Now().UTC(),
			Tokens:    tokens,
			Files:     sum.Files,
		}); err != nil {
			o.runner.Logger.Warn("write last-backup stamp", "err", err)
		}
	}
	return onestopStep{Name: "apps", Detail: fmt.Sprintf("%d app(s), %d file(s) → %s", len(sum.Apps), sum.Files, eng.HostRoot())}
}

func (o *onestopCtx) backupAIStep(tag string, includeAuth bool) onestopStep {
	eng := o.aiEngine()
	sum, err := eng.Backup(aisettings.BackupOptions{Tag: tag, IncludeAuth: includeAuth})
	if err != nil {
		return onestopStep{Name: "ai", Err: err}
	}
	auditAIEventBestEffort(o.cmd, "ai.backup", aiSummaryPayload(sum))
	return onestopStep{Name: "ai", Detail: fmt.Sprintf("version %s (%d files) → %s", sum.Version, sum.Files, sum.Path)}
}

func (o *onestopCtx) backupSecretsStep(storeDir string) onestopStep {
	dest := o.secretsArchiveDir()
	copied, err := secretsBackupFiles(o.runner, o.p, storeDir, dest)
	if err != nil {
		return onestopStep{Name: "secrets", Err: err}
	}
	if !o.dryRun {
		o.state.Secrets.LastBackup = &config.BackupRecord{
			Path:  dest,
			Time:  time.Now(),
			Files: copied,
		}
		if err := persistUserState(o.cmd, o.state); err != nil {
			o.runner.Logger.Warn("record secrets last backup", "err", err)
		}
	}
	return onestopStep{Name: "secrets", Detail: fmt.Sprintf("%d archive(s) → %s", copied, dest)}
}
