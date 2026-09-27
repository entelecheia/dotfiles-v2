package cli

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/aisettings"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/syncer"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

// dot ai memory sync — cross-machine claude-mem replication over ssh.

func newAIMemorySyncCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "sync [push|pull|sync|sessions|status]",
		Short: "Replicate the claude-mem store with a peer over ssh",
		Long: `Replicate observations, session summaries, and their sdk_sessions rows
with a peer Mac. Incremental by default (receiver's MAX(created_at) minus a
7-day overlap); --full re-offers everything and dedupe absorbs it. The peer
needs only the dot binary: the transport is
'ssh <target> dot ai memory sync --serve <op>' with JSON over stdio.

sessions replicates all sessions both ways. status prints both sides'
counts plus the last scheduled run. With no action, sync runs both
directions. --peer defaults to the configured dot peer target.`,
		Args:         cobra.MaximumNArgs(1),
		RunE:         runAIMemorySync,
		SilenceUsage: true,
	}
	c.Flags().String("peer", "", "ssh target to sync with (default: the dot peer target)")
	c.Flags().Bool("full", false, "disable the incremental cutoff (re-offer all rows)")
	c.Flags().String("remote-db", "", "DB path on the peer (default: the peer's ~/.claude-mem/claude-mem.db)")
	c.Flags().String("serve", "", "run one serve op against the local DB (used by the ssh transport)")
	_ = c.Flags().MarkHidden("serve")
	return c
}

func runAIMemorySync(cmd *cobra.Command, args []string) error {
	if op, _ := cmd.Flags().GetString("serve"); op != "" {
		dbPath := memorySyncDBPath(cmd)
		return aisettings.ServeOp(cmd.Context(), dbPath, op, cmd.InOrStdin(), cmd.OutOrStdout())
	}

	action := "sync"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "push", "pull", "sync", "sessions", "status":
	default:
		return fmt.Errorf("unknown sync action %q (want push, pull, sync, sessions, or status)", action)
	}

	peer, err := memorySyncPeer(cmd)
	if err != nil {
		return err
	}
	home := homeFor(cmd)
	if action == "status" {
		return printMemorySyncStatus(printerFrom(cmd), cmd, home, peer)
	}

	scheduled := os.Getenv(scheduledRunEnv) == "1"
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	p := printerFrom(cmd)

	// An unreachable peer on a scheduled run is a skipped run, exit 0 — the
	// LaunchAgent pre-checks too; this covers direct scheduled invocations.
	probe := aisettingsProbeRunner()
	if err := syncer.CheckSSH(cmd.Context(), probe, peer.Target); err != nil {
		if scheduled {
			return nil
		}
		return fmt.Errorf("peer %s is unreachable: %w", peer.Target, err)
	}

	// The scheduled pass keeps both Macs on the latest claude-mem first, so
	// the pair converges on one plugin version (best-effort; a failed update
	// never blocks the sync itself).
	if scheduled {
		mgr, merr := newClaudeMemManagerFromCmd(cmd)
		if merr == nil {
			runner := memorySyncRunner(dryRun)
			if _, uerr := mgr.UpdateClaudeMemPlugin(cmd.Context(), runner, scheduled); uerr != nil {
				p.Warn("claude-mem update check failed (continuing with sync): %v", uerr)
			}
		}
	}

	db, err := aisettings.OpenSyncDB(aisettings.DefaultSyncDBPath(home))
	if err != nil {
		return err
	}
	defer db.Close()

	full, _ := cmd.Flags().GetBool("full")
	opts := aisettings.SyncOptions{Full: full, Direction: action, OnlySessions: action == "sessions"}
	if dryRun {
		return printMemorySyncDryRun(cmd, db, peer, opts)
	}
	report, err := aisettings.RunMemorySync(cmd.Context(), db, &aisettings.SSHTransport{}, peer, opts, time.Now())
	if err != nil {
		return err
	}

	// Backfill kick on this side when a pull added content rows (the serve
	// side kicks its own worker after a push).
	if report.Pulled != nil && report.Pulled.Obs+report.Pulled.Sums > 0 {
		_ = aisettings.KickWorkerRestart(cmd.Context(), home, nil)
	}
	if err := aisettings.SaveSyncState(aisettings.SyncStatePath(home), peer.Target, aisettings.SyncStateEntry{
		LastRun:    time.Now(),
		LastResult: report.Summarize(),
	}); err != nil {
		p.Warn("could not persist sync state: %v", err)
	}
	auditAIEventBestEffort(cmd, "ai.memory.sync", map[string]any{
		"peer": peer.Target, "direction": opts.Direction, "report": report,
	})

	p.Header("claude-mem sync")
	p.KV("Peer", peer.Target)
	p.KV("Action", action)
	p.Line("  %s", report.Summarize())
	printMemorySyncCounts(p, "Local", report.LocalCounts)
	printMemorySyncCounts(p, "Peer", report.PeerCounts)
	logSkippedColumns(p, report)
	p.Success("✓ sync complete")
	return nil
}

func printMemorySyncDryRun(cmd *cobra.Command, db *aisettings.SyncDB, peer aisettings.SyncPeer, opts aisettings.SyncOptions) error {
	p := printerFrom(cmd)
	t := &aisettings.SSHTransport{}
	maxes, err := t.Max(cmd.Context(), peer)
	if err != nil {
		return err
	}
	cutoffs, err := memorySyncCutoffsForPreview(maxes, opts)
	if err != nil {
		return err
	}
	bundle, err := db.Export(cutoffs, opts.OnlySessions)
	if err != nil {
		return err
	}
	p.Header("claude-mem sync (dry run)")
	p.KV("Peer", peer.Target)
	if cutoffs == nil {
		p.KV("Cutoff", "(full export)")
	} else {
		for _, table := range []string{"observations", "session_summaries"} {
			p.KV("Cutoff "+table, cutoffs[table])
		}
	}
	p.Line("  would offer: %d observations, %d summaries, %d sessions", len(bundle.Observations), len(bundle.Summaries), len(bundle.Sessions))
	return nil
}

func memorySyncCutoffsForPreview(maxes map[string]string, opts aisettings.SyncOptions) (map[string]string, error) {
	if opts.Full || opts.OnlySessions {
		return nil, nil
	}
	out := map[string]string{}
	for _, table := range []string{"observations", "session_summaries"} {
		cutoff, err := aisettings.CutoffFor(maxes[table], time.Now())
		if err != nil {
			return nil, err
		}
		out[table] = cutoff
	}
	return out, nil
}

// printMemorySyncStatus renders both the `sync status` action and the
// "peer sync" section of `dot ai memory status`.
func printMemorySyncStatus(p *Printer, cmd *cobra.Command, home string, peer aisettings.SyncPeer) error {
	p.Header("claude-mem peer sync")
	mgr, err := newClaudeMemManagerFromCmd(cmd)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(mgr.SyncLaunchdPlistPath()); statErr == nil {
		p.KV("Agent", mgr.SyncLaunchdPlistPath())
	} else {
		p.KV("Agent", "not installed — run: dot ai memory install --peer "+peer.Target)
	}
	p.KV("Peer", peer.Target)
	if peers, err := aisettings.LoadSyncState(aisettings.SyncStatePath(home)); err == nil {
		if entry, ok := peers[peer.Target]; ok {
			p.KV("Last run", entry.LastRun.Local().Format("2006-01-02 15:04:05"))
			p.KV("Last result", entry.LastResult)
		} else {
			p.KV("Last run", "(never)")
		}
	}
	db, err := aisettings.OpenSyncDB(aisettings.DefaultSyncDBPath(home))
	if err != nil {
		return err
	}
	defer db.Close()
	local, err := db.Counts()
	if err != nil {
		return err
	}
	printMemorySyncCounts(p, "Local", local)
	counts, err := (&aisettings.SSHTransport{}).Counts(cmd.Context(), peer)
	if err != nil {
		p.KV("Peer counts", "unreachable: "+err.Error())
		return nil
	}
	printMemorySyncCounts(p, "Peer", counts)
	return nil
}

// printMemorySyncStatusSection renders the compact "Peer sync" section of
// `dot ai memory status`: agent, peer, last run/result, both sides' counts.
func printMemorySyncStatusSection(p *Printer, cmd *cobra.Command, mgr *aisettings.ClaudeMemManager) {
	p.Section("Peer sync")
	hint := ui.StyleHint.Render(ui.MarkPartial)
	if _, err := os.Stat(mgr.SyncLaunchdPlistPath()); err != nil {
		p.Bullet(hint, "sync agent not installed — run: dot ai memory install --peer <target>")
	} else {
		p.Bullet(ui.StyleSuccess.Render(ui.MarkPresent), "sync agent installed (hourly)")
	}
	home := mgr.HomeDir
	target := ""
	if bs, err := syncer.Bootstrap(syncer.BootstrapOptions{Profile: PeerProfile, Home: homeOverrideFrom(cmd), ReadOnly: true}); err == nil && bs.Config.Target.IsSSH() {
		target = bs.Config.Target.Host
	}
	if target == "" {
		if recorded := memorySyncPeersFromState(home); len(recorded) > 0 {
			target = recorded[0]
		}
	}
	if target == "" {
		p.Bullet(hint, "no peer configured (dot peer init or --peer)")
		return
	}
	peers, _ := aisettings.LoadSyncState(aisettings.SyncStatePath(home))
	line := "peer " + target
	if entry, ok := peers[target]; ok {
		line += " · last run " + entry.LastRun.Local().Format("2006-01-02 15:04") + " · " + entry.LastResult
	} else {
		line += " · never synced"
	}
	p.Bullet(hint, line)
	db, err := aisettings.OpenSyncDB(aisettings.DefaultSyncDBPath(home))
	if err != nil {
		return
	}
	defer db.Close()
	if local, err := db.Counts(); err == nil {
		p.Bullet(hint, syncCountsLine("local", local))
	}
	counts, err := (&aisettings.SSHTransport{}).Counts(cmd.Context(), aisettings.SyncPeer{Target: target})
	if err != nil {
		p.Bullet(hint, "peer counts unreachable")
		return
	}
	p.Bullet(hint, syncCountsLine("peer", counts))
}

func syncCountsLine(label string, c *aisettings.TableCounts) string {
	return fmt.Sprintf("%s: obs %d · sums %d · sessions %d · obs-without-session %d",
		label, c.Obs, c.Sums, c.Sessions, c.ObsWithoutSession)
}

func printMemorySyncCounts(p *Printer, label string, c *aisettings.TableCounts) {
	if c == nil {
		return
	}
	p.KV(label, fmt.Sprintf("obs %d · sums %d · sessions %d · obs-without-session %d",
		c.Obs, c.Sums, c.Sessions, c.ObsWithoutSession))
}

func logSkippedColumns(p *Printer, report *aisettings.SyncReport) {
	seen := map[string]bool{}
	for _, res := range []*aisettings.ImportResult{report.Pushed, report.Pulled} {
		if res == nil {
			continue
		}
		for _, col := range res.Skipped {
			if !seen[col] {
				seen[col] = true
				p.Warn("skipped column %q (not present on receiver)", col)
			}
		}
	}
}

// memorySyncPeer resolves --peer, falling back to the configured dot peer
// target (read-only profile resolution).
func memorySyncPeer(cmd *cobra.Command) (aisettings.SyncPeer, error) {
	target, _ := cmd.Flags().GetString("peer")
	if target == "" {
		bs, err := syncer.Bootstrap(syncer.BootstrapOptions{Profile: PeerProfile, Home: homeOverrideFrom(cmd), ReadOnly: true})
		if err != nil {
			return aisettings.SyncPeer{}, err
		}
		if !bs.Config.Target.IsSSH() {
			return aisettings.SyncPeer{}, fmt.Errorf("no --peer given and dot peer is not configured; pass --peer <ssh-target> or run: dot peer init --host <user@host>")
		}
		target = bs.Config.Target.Host
	}
	remoteDB, _ := cmd.Flags().GetString("remote-db")
	return aisettings.SyncPeer{Target: target, RemoteDB: remoteDB}, nil
}

func memorySyncDBPath(cmd *cobra.Command) string {
	if remote, _ := cmd.Flags().GetString("remote-db"); remote != "" {
		return remote
	}
	return aisettings.DefaultSyncDBPath(homeFor(cmd))
}

// memorySyncPeersFromState lists recorded peers for the status section when
// no dot peer target is configured.
func memorySyncPeersFromState(home string) []string {
	peers, err := aisettings.LoadSyncState(aisettings.SyncStatePath(home))
	if err != nil || len(peers) == 0 {
		return nil
	}
	out := make([]string, 0, len(peers))
	for peer := range peers {
		out = append(out, peer)
	}
	sort.Strings(out)
	return out
}

// aisettingsProbeRunner is the always-live probe runner for reachability
// checks (ssh probes must run even under --dry-run).
func aisettingsProbeRunner() *exec.Runner {
	return exec.NewProbeRunner()
}

func memorySyncRunner(dryRun bool) *exec.Runner {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return exec.NewRunner(dryRun, logger)
}
