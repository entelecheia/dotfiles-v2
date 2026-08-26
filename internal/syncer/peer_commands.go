package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Peer deletions are normally tens of inbox paths. Keep the new-profile cap
// well below the mirror's broader 1000-path default.
const peerDefaultMaxDelete = 100

// peerAllowHeader seeds the peer profile's allow.txt. The cloud mirror keeps
// secrets out; a peer is a second machine the operator already trusts with the
// same work, and without env files and tokens the workspace does not actually
// run there. That difference in intent is exactly why the two profiles have
// separate allow files.
const peerAllowHeader = `# dot peer allow.txt — secrets opt-in for machine-to-machine sync.
#
# Unlike the cloud mirror, a peer is another machine you already trust with
# this workspace, and the workspace does not function there without these.
# The transport is ssh, so these never touch a cloud provider.
#
# Remove a line to stop syncing that class of secret.
/.maru/secrets/**
/.secrets/**
**/.env
**/.env.*
`

// peerHomePathsHeader documents the second rsync pass.
const peerHomePathsHeader = `# dot peer home-paths.txt — host-local paths carried to the peer.
#
# One path per line, relative to $HOME. Comments start with '#'.
# These sit outside the workspace but the workspace does not work without
# them: 16 of this workspace's 19 submodule remotes are SSH, so ~/.ssh is
# load-bearing, and the MCP server list lives in ~/.claude.json.
#
# Deliberately absent, because copying them breaks the target:
#   .maru/env                  venv console scripts bake an absolute
#                              interpreter path — rebuild, never copy
#   .maru/skills/_builtin      re-materialized by the Maru app
#   .maru/skills/_sources      branch-specific checkouts; reproduce, don't copy
#   .claude/plugins/cache      cache
#   .codex/sessions            history
#   .codex/auth.json           credential; re-auth instead
#   .codex/config.toml         machine-local: Codex rewrites it continuously
#                              (per-project trust, hook state, plugin flags),
#                              and its MCP server definitions hash-key the
#                              Keychain-stored MCP OAuth credentials — copying
#                              a peer's config orphans them on both machines
#
# Also unreachable by any file copy: tokens in the macOS keychain (gh, for
# one). They cannot be transferred and cannot even be verified over ssh.
#
# ~/.ssh is listed as a directory but known_hosts is excluded in code: it is
# per-machine trust state, merging it is meaningless, and overwriting it with the
# peer's copy deletes the host key for the channel this sync runs over. That is
# not hypothetical - it happened on the first real run and broke the next ssh.
.ssh
.gitconfig
.config/git
.config/dotfiles
.config/shell/90-secrets.sh
.claude.json
.claude/settings.json
.claude/settings.local.json
.claude/skills
.claude/plans
.agents
.codex/hooks.json
.codex/memories
.maru/settings.json
.maru/sites.json
.maru/agents.json
.maru/state
.maru/telegram
`

// peerPlistTmpl runs `dot peer sync` on an interval.
//
// The PATH is explicit for the same reason the mirror unit's is: launchd hands a
// job a minimal PATH that finds Apple's /usr/bin/rsync (openrsync) before
// Homebrew's 3.x. A peer transfer with the wrong rsync fails at data time, not
// at probe time.
//
// DOT_SCHEDULED_RUN is how the run tells cli it is scheduled: launchd sets no
// distinguishing variable of its own, a TTY check misfires under CI and under
// a pipe, and a persistent flag would advertise a mode no human invokes. The
// dict was already here, so the marker costs one entry. It is read in exactly
// one place, internal/cli/peer_cmd.go.
const peerPlistTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.dotfiles.peer</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>peer</string>
    <string>sync</string>%s
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>DOT_SCHEDULED_RUN</key>
    <string>1</string>
  </dict>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`

var peerExecutable = os.Executable

// peerClockTolerance is how far the two clocks may drift before "newer wins"
// stops being a meaningful comparison.
const peerClockTolerance = 2 * time.Second

// PeerEventKind names one observable step of a peer run. The engine emits
// kinds; cli owns the wording (D-06/D-10).
type PeerEventKind int

const (
	PeerEventPullStart             PeerEventKind = iota // the pull-from-peer pass begins
	PeerEventRemoteDeletesHeld                          // remote-only deletes withheld for want of provenance
	PeerEventPropagateDeletesStart                      // the delete-propagation pass begins
	PeerEventLocalDeletesHeld                           // local deletes withheld for want of provenance
	PeerEventPushStart                                  // the push-to-peer pass begins
	PeerEventHostPathsStart                             // the host-path pass begins
	PeerEventHostPathsMissing                           // no host-path list exists; Path names where one would live
	PeerEventPartialTransfer                            // rsync moved some but not all of a pass; Err carries it
)

// PeerEvent is one step outcome. Only the fields its kind documents are set.
type PeerEvent struct {
	Kind PeerEventKind
	Path string
	Err  error
}

func emitPeer(progress func(PeerEvent), e PeerEvent) {
	if progress != nil {
		progress(e)
	}
}

// PeerHomePathsFile is the host-path allowlist inside a peer store.
func PeerHomePathsFile(paths *LocalPaths) string {
	return filepath.Join(paths.StoreDir, "home-paths.txt")
}

// peerHomeArg renders the ProgramArguments entry that re-supplies --home on
// every scheduled run, or nothing when the job was installed without an
// override. The value is interpolated raw, exactly as the executable path and
// the log path already are.
func peerHomeArg(cfg *Config) (string, error) {
	if cfg == nil || cfg.Home == "" {
		return "", nil
	}
	arg, err := plistHomeArgument(cfg.Home)
	if err != nil {
		return "", err
	}
	return "\n    <string>" + arg + "</string>", nil
}

func seedFileIfAbsent(path, body string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// PeerInitOptions controls PeerInit. Host is already validated by the caller;
// an empty RemotePath means "same absolute layout on both machines".
type PeerInitOptions struct {
	Config     *Config
	Host       string
	RemotePath string

	// Runner is read for its own DryRun flag only — PeerInit writes files
	// directly and routes nothing through it. It is here so a caller that set
	// the flag on the runner and not on the option still gets a preview, the
	// same reconciliation the three aisettings managers make.
	Runner *exec.Runner

	// DryRun computes the profile and reports the files without writing any.
	DryRun bool
}

// PeerInitResult is what the operator is told about the profile just written,
// or — when DryRun is set — about the one that would have been.
type PeerInitResult struct {
	// DryRun reports that none of the three files below were written. The
	// engine returns the flag and the paths; cli owns every string.
	DryRun        bool
	Target        string
	StoreDir      string
	ConfigFile    string
	AllowFile     string
	HomePathsFile string
	Propagation   PropagationPolicy
	MaxDelete     int
}

// PeerInit writes the peer store: target, secrets opt-in and host-path list.
func PeerInit(opts PeerInitOptions) (*PeerInitResult, error) {
	cfg := opts.Config
	paths := cfg.LocalPaths
	if paths == nil {
		return nil, fmt.Errorf("peer store unresolved")
	}
	remotePath := opts.RemotePath
	if remotePath == "" {
		// Same absolute layout on both machines is the norm here; the
		// workspace path is part of the environment being replicated.
		remotePath = strings.TrimRight(cfg.LocalPath, "/")
	}

	local, ok, err := LoadLocalConfig(paths)
	if err != nil {
		return nil, err
	}
	newProfile := !ok || local == nil
	if newProfile {
		local = &LocalConfig{}
	}
	local.Target = "ssh:" + opts.Host + ":" + remotePath
	// exclude-mode, not include-mode. The mirror uses an allowlist
	// (tracked files plus a binary-extension list) because untracked text
	// round-trips through Git anyway. A peer is different: "either machine
	// can continue the same work" has to cover an untracked scratch note,
	// which is neither tracked nor a binary extension and would otherwise
	// sync by neither channel. Measured: a new .md file never arrived.
	//
	// Safe because the junk layer already excludes the caches that make
	// this expensive - node_modules, target, .next, .venv, __pycache__ -
	// and secrets remain governed by allow.txt.
	local.FilterMode = FilterModeExclude
	if newProfile {
		// Deletions propagate for a new peer profile. Without this a file
		// removed on one machine returns on the next pull. Re-running init
		// must preserve an operator's later opt-out.
		local.Propagation = PropagationPolicy{Create: true, Update: true, Delete: true}
		local.MaxDelete = peerDefaultMaxDelete
	}
	local.IncludeSubmodules = true
	// Peer profiles are per-machine by construction (the store is
	// gitignored), so the owner is simply this machine. Use the DNS-safe
	// name rather than os.Hostname(), which can be the generic "Mac".
	if name := PreferredMachineName(); name != "" {
		local.Owner = name
	}

	result := &PeerInitResult{
		DryRun:        opts.DryRun || (opts.Runner != nil && opts.Runner.DryRun),
		Target:        local.Target,
		StoreDir:      paths.StoreDir,
		ConfigFile:    paths.ConfigFile,
		AllowFile:     paths.AllowFile,
		HomePathsFile: PeerHomePathsFile(paths),
		Propagation:   local.Propagation,
		MaxDelete:     local.MaxDelete,
	}
	// The three writes below bypass the runner entirely, so the flag has to be
	// honored here or a preview writes twelve files (BUG-13, D-03). Everything
	// the operator is shown was computed above and is identical either way.
	if result.DryRun {
		return result, nil
	}

	if err := SaveLocalConfig(paths, local); err != nil {
		return nil, err
	}
	if err := seedFileIfAbsent(paths.AllowFile, peerAllowHeader); err != nil {
		return nil, err
	}
	if err := seedFileIfAbsent(PeerHomePathsFile(paths), peerHomePathsHeader); err != nil {
		return nil, err
	}
	return result, nil
}

// PeerDiffOptions controls PeerDiff. Probe is the always-live runner: a
// divergence report is read-only, so it must work under --dry-run.
type PeerDiffOptions struct {
	Config *Config
	Probe  *exec.Runner
}

// PeerDiffResult carries the divergence plan, or the fact that the peer was
// away. An unreachable peer is not an error.
type PeerDiffResult struct {
	Unreachable bool
	Plan        *PeerPlan
}

// PeerDiff reports paths where the two machines disagree.
func PeerDiff(ctx context.Context, opts PeerDiffOptions) (*PeerDiffResult, error) {
	cfg := opts.Config
	if !cfg.Target.IsSSH() {
		return nil, fmt.Errorf("peer target is not configured; run dot peer init first")
	}
	if err := CheckSSH(ctx, opts.Probe, cfg.Target.Host); err != nil {
		return &PeerDiffResult{Unreachable: true}, nil
	}
	rp, err := RemoteRsyncPath(ctx, opts.Probe, cfg.Target.Host)
	if err != nil {
		return nil, err
	}
	cfg.RemoteRsyncPath = rp
	release, err := AcquireLock(cfg.LockDir)
	if err != nil {
		return nil, fmt.Errorf("another sync is already running: %w", err)
	}
	defer release()

	plan, err := peerPlanForRun(ctx, opts.Probe, cfg)
	if err != nil {
		return nil, err
	}
	if err := ValidatePeerPlanSafety(cfg, plan); err != nil {
		return nil, err
	}
	return &PeerDiffResult{Plan: plan}, nil
}

// PeerSyncOptions controls PeerSync. Runner performs the transfers and honors
// --dry-run; Probe is always live so reachability, the remote rsync version
// and the clock can still be read during a preview.
type PeerSyncOptions struct {
	Config   *Config
	Runner   *exec.Runner
	Probe    *exec.Runner
	PushOnly bool
	PullOnly bool
	SkipHome bool
	DryRun   bool
	Progress func(PeerEvent)
}

// PeerSyncResult reports how the transaction ended. Complete=false means
// destructive transitions were held back and the baseline was left alone.
type PeerSyncResult struct {
	Unreachable bool
	Complete    bool
}

// PeerSync exchanges the workspace and the host paths with the peer.
func PeerSync(ctx context.Context, opts PeerSyncOptions) (*PeerSyncResult, error) {
	cfg, runner, probe := opts.Config, opts.Runner, opts.Probe
	dryRun := opts.DryRun
	if !cfg.Target.IsSSH() {
		return nil, fmt.Errorf("peer target is not an ssh target; run: dot peer init --host <user@host>")
	}
	// The profile owner is the coordinator. This guard is intentionally
	// before any probe or transfer: a second machine must not perform a
	// half-run and then leave a different baseline behind.
	if err := CheckOwner(cfg); err != nil {
		return nil, err
	}

	// One peer run at a time. The scheduled job and a manual run would
	// otherwise overlap and drive concurrent rsync writes into the same
	// tree and the same conflict directory.
	release, err := AcquireLock(cfg.LockDir)
	if err != nil {
		return nil, fmt.Errorf("another peer sync is already running: %w", err)
	}
	defer release()

	if err := CheckSSH(ctx, probe, cfg.Target.Host); err != nil {
		return &PeerSyncResult{Unreachable: true}, nil
	}
	rp, err := RemoteRsyncPath(ctx, probe, cfg.Target.Host)
	if err != nil {
		return nil, err
	}
	cfg.RemoteRsyncPath = rp
	if err := checkRemotePeerOwner(ctx, probe, cfg); err != nil {
		return nil, err
	}

	if !dryRun {
		// Normalize opted-in NFD names before tombstones, inventory, plans,
		// and rsync filters materialize. The helper is marker-gated, so an
		// unmarked workspace remains unchanged until its explicit migration.
		if err := NormalizeWorkspaceNamesBeforePush(cfg); err != nil {
			return nil, fmt.Errorf("normalizing workspace names: %w", err)
		}
		if NFDMigrationMarked(cfg.LocalPaths.WorkspaceRoot) {
			if err := normalizeRemotePeerNames(ctx, runner, cfg); err != nil {
				return nil, err
			}
		}
	}

	// Compute deletion evidence before any pull mutates the coordinator
	// tree, then build the same three-way plan used by `peer diff`.
	tombstones, err := ComputeTombstones(cfg)
	if err != nil {
		return nil, err
	}
	cfg.Tombstones = tombstones
	plan, err := peerPlanForRun(ctx, probe, cfg)
	if err != nil {
		return nil, err
	}
	if err := ValidatePeerPlanSafety(cfg, plan); err != nil {
		return nil, err
	}
	conflict := NewConflictDir()
	complete := true
	baselineReady, err := PeerBaselineReady(cfg)
	if err != nil {
		return nil, err
	}
	// A target marker authorizes destructive transitions. An initial
	// additive bootstrap remains allowed, but an unproven local/remote
	// deletion must stay pending and must not retire the baseline.
	deletesAuthorized := baselineReady

	if !opts.PushOnly {
		emitPeer(opts.Progress, PeerEvent{Kind: PeerEventPullStart})
		pullErr := PullPeerPlan(ctx, runner, cfg, plan, dryRun)
		if err := failOnPartial(opts.Progress, pullErr); err != nil {
			return nil, err
		}
		if cfg.Propagation.Delete && deletesAuthorized && len(plan.DeleteLocal) > 0 {
			if err := DeletePeerLocal(cfg, conflict, plan.DeleteLocal, dryRun); err != nil {
				return nil, err
			}
		} else if len(plan.DeleteLocal) > 0 {
			complete = false
			emitPeer(opts.Progress, PeerEvent{Kind: PeerEventRemoteDeletesHeld})
		}
	}
	if !opts.PullOnly {
		deleteSet := intersectPeerPaths(tombstones, plan.DeleteRemote)
		if !cfg.Propagation.Delete {
			deleteSet = nil
		}
		if len(deleteSet) > 0 && deletesAuthorized {
			emitPeer(opts.Progress, PeerEvent{Kind: PeerEventPropagateDeletesStart})
			if err := PropagateDeletes(ctx, runner, cfg, conflict, deleteSet, dryRun); err != nil {
				return nil, err
			}
		} else if len(plan.DeleteRemote) > 0 {
			complete = false
			emitPeer(opts.Progress, PeerEvent{Kind: PeerEventLocalDeletesHeld})
		}
		if len(plan.Push) > len(plan.QuarantineRemote) {
			baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
			if err != nil {
				return nil, fmt.Errorf("peer push revalidation: loading baseline: %w", err)
			}
			// Treat every planned push path as type-sensitive during this
			// inventory, including baseline-unknown creates that became links.
			for _, rel := range plan.Push {
				if _, ok := baseline[rel]; !ok {
					baseline[rel] = Fingerprint{}
				}
			}
			remoteNow, err := peerRemoteInventory(ctx, probe, cfg, baseline)
			if err != nil {
				return nil, err
			}
			if err := ValidatePeerPushRemoteStable(plan, remoteNow); err != nil {
				return nil, err
			}
		}
		emitPeer(opts.Progress, PeerEvent{Kind: PeerEventPushStart})
		pushErr := PushPeerPlan(ctx, runner, cfg, plan, conflict, dryRun)
		if err := failOnPartial(opts.Progress, pushErr); err != nil {
			return nil, err
		}
	}

	// Host paths are a separate pass because they live outside the
	// workspace and are addressed by an explicit list rather than the
	// workspace filter chain. Without them the workspace does not run on
	// the peer: 16 of its 19 submodule remotes are SSH.
	if !opts.SkipHome {
		emitPeer(opts.Progress, PeerEvent{Kind: PeerEventHostPathsStart})
		homeErr := peerHomeSync(ctx, runner, cfg, opts.Progress, dryRun, opts.PushOnly, opts.PullOnly)
		if err := failOnPartial(opts.Progress, homeErr); err != nil {
			return nil, err
		}
	}
	// A first successful additive run establishes provenance. A target
	// marker is not required for that bootstrap, but any held deletion is
	// enough to keep the baseline unchanged until a later verified run.
	canCommitBaseline := baselineReady ||
		(len(plan.DeleteLocal) == 0 && len(plan.DeleteRemote) == 0)
	if complete && !dryRun && !opts.PullOnly {
		if err := AppendPeerConflictAudit(cfg, plan); err != nil {
			return nil, err
		}
	}
	if complete && !dryRun && !opts.PushOnly && !opts.PullOnly && canCommitBaseline {
		if err := CommitPeerBaseline(cfg, plan.NextBaseline); err != nil {
			return nil, err
		}
	}
	return &PeerSyncResult{Complete: complete}, nil
}

// PeerScheduleOptions controls PeerSchedule. Off removes the job; everything
// else is only read on the install path.
type PeerScheduleOptions struct {
	Config   *Config
	Runner   *exec.Runner
	Probe    *exec.Runner
	Interval time.Duration
	Off      bool
	DryRun   bool
}

// PeerScheduleResult describes the job that was installed or removed.
type PeerScheduleResult struct {
	Off      bool
	DryRun   bool
	Plist    string
	LogFile  string
	Interval time.Duration
}

// PeerSchedule installs or removes the periodic `dot peer sync` job.
func PeerSchedule(ctx context.Context, opts PeerScheduleOptions) (*PeerScheduleResult, error) {
	cfg, runner := opts.Config, opts.Runner
	plist := filepath.Join(cfg.HomeDir(), "Library", "LaunchAgents", "com.dotfiles.peer.plist")
	if err := validateSchedulerMutationHome(cfg.Home); err != nil {
		return nil, fmt.Errorf("peer scheduler home %q rejected for plist %s: %w; existing artifact was left untouched; run dot peer setup after fixing the home path", cfg.Home, plist, err)
	}

	if opts.Off {
		_, _ = runner.Run(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/com.dotfiles.peer")
		if opts.DryRun {
			// runner already skipped the bootout; removing the plist here
			// anyway would leave a loaded job with no on-disk definition,
			// which is the opposite of a preview.
			return &PeerScheduleResult{Off: true, DryRun: true, Plist: plist}, nil
		}
		if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return &PeerScheduleResult{Off: true, Plist: plist}, nil
	}
	if err := CheckOwner(cfg); err != nil {
		return nil, fmt.Errorf("refusing peer scheduler on a non-coordinator: %w", err)
	}
	if opts.Interval < time.Minute {
		return nil, fmt.Errorf("--interval must be at least 1m (got %s)", opts.Interval)
	}
	if !cfg.Target.IsSSH() {
		return nil, fmt.Errorf("peer target is not configured; run dot peer init first")
	}
	exe, err := peerExecutable()
	if err != nil {
		return nil, err
	}
	preparedExe, err := plistXMLText(exe, "")
	if err != nil {
		return nil, peerPlistPathError("executable", exe, plist, err)
	}
	preparedLogFile, err := plistXMLText(cfg.LogFile, "")
	if err != nil {
		return nil, peerPlistPathError("log file", cfg.LogFile, plist, err)
	}
	homeArg, err := peerHomeArg(cfg)
	if err != nil {
		return nil, fmt.Errorf("peer scheduler home %q rejected for plist %s: %w; existing artifact was left untouched; run dot peer setup after fixing the home path", cfg.Home, plist, err)
	}
	// All remaining local inputs are now representable in a plist. Check the
	// coordinator immediately before the dry-run/mutation branch so actual
	// scheduler installs still require bilateral owner agreement, while an
	// invalid local artifact can be rejected without a network dependency.
	if err := CheckSSH(ctx, opts.Probe, cfg.Target.Host); err != nil {
		return nil, fmt.Errorf("checking peer coordinator before scheduler setup: %w", err)
	}
	if err := checkRemotePeerOwner(ctx, opts.Probe, cfg); err != nil {
		return nil, err
	}
	// Mirror the off-arm above. The write below bypasses the runner entirely
	// (os.MkdirAll + os.WriteFile), so without this a preview leaves a plist on
	// disk with no loaded job behind it — the same inconsistent state the
	// off-arm refuses to create (BUG-14).
	//
	// The guard sits AFTER the whole validation chain on purpose: a preview that
	// skipped validation would be a different lie from the one being fixed. The
	// off-arm above keeps reading opts.DryRun alone, so its behavior is
	// untouched; only this arm reconciles the option with the runner's own flag,
	// the way the three aisettings managers do.
	if opts.DryRun || (runner != nil && runner.DryRun) {
		return &PeerScheduleResult{DryRun: true, Plist: plist, LogFile: cfg.LogFile, Interval: opts.Interval}, nil
	}
	logFile := cfg.LogFile
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return nil, err
	}
	body := fmt.Sprintf(peerPlistTmpl, preparedExe, homeArg, int(opts.Interval.Seconds()), preparedLogFile, preparedLogFile)
	if err := runner.WriteFileAtomic(plist, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("writing peer plist %s for home %q: %w; existing artifact was left untouched; run dot peer setup after fixing the home path", plist, cfg.Home, err)
	}
	label := "gui/" + strconv.Itoa(os.Getuid()) + "/com.dotfiles.peer"
	_, _ = runner.Run(ctx, "launchctl", "bootout", label)
	if _, err := runner.Run(ctx, "launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), plist); err != nil {
		return nil, fmt.Errorf("loading %s: %w", plist, err)
	}
	return &PeerScheduleResult{Plist: plist, LogFile: logFile, Interval: opts.Interval}, nil
}

func peerPlistPathError(field, value, plist string, err error) error {
	return fmt.Errorf("peer scheduler %s %q rejected for plist %s: %w; existing artifact was left untouched; run dot peer setup after fixing the path", field, value, plist, err)
}

// PeerDoctorOptions controls PeerDoctor. The runner is always live: every
// check is a read-only question about the peer.
type PeerDoctorOptions struct {
	Config *Config
	Probe  *exec.Runner
}

// PeerDoctorReport is the outcome of every precondition probe. Each check
// degrades into a field rather than aborting the report, so one broken probe
// does not hide the rest.
type PeerDoctorReport struct {
	Target          string
	Unreachable     bool
	UnreachableErr  error
	RemoteRsyncPath string
	RemoteRsyncErr  error
	ClockSkew       time.Duration
	ClockSkewErr    error
	ClockSkewOK     bool
	Disk            string
	DiskKnown       bool
	Problems        int
}

// PeerDoctor probes everything that silently breaks a peer transfer.
func PeerDoctor(ctx context.Context, opts PeerDoctorOptions) (*PeerDoctorReport, error) {
	cfg, runner := opts.Config, opts.Probe
	if !cfg.Target.IsSSH() {
		return nil, fmt.Errorf("peer profile target is %q, expected an ssh: target; run dot peer init", cfg.Target.String())
	}
	host := cfg.Target.Host
	report := &PeerDoctorReport{Target: cfg.Target.String()}

	if err := CheckSSH(ctx, runner, host); err != nil {
		report.Unreachable = true
		report.UnreachableErr = err
		return report, nil
	}

	rp, err := RemoteRsyncPath(ctx, runner, host)
	if err != nil {
		report.RemoteRsyncErr = err
		report.Problems++
	} else {
		report.RemoteRsyncPath = rp
	}

	if skew, err := peerClockSkew(ctx, runner, host); err != nil {
		report.ClockSkewErr = err
	} else {
		report.ClockSkew = skew
		abs := skew
		if abs < 0 {
			abs = -abs
		}
		report.ClockSkewOK = abs <= peerClockTolerance
		if !report.ClockSkewOK {
			report.Problems++
		}
	}

	if out, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", host, "df -h / | tail -1"); err == nil {
		report.Disk = strings.Join(strings.Fields(strings.TrimSpace(out.Stdout)), " ")
		report.DiskKnown = true
	}
	return report, nil
}

// peerPlanForRun builds the coordinator plan from one local inventory, one
// read-only remote inventory, and the last committed common baseline. The
// same helper is used by `peer sync` and `peer diff`; a displayed divergence
// therefore cannot disagree with the transaction that follows it.
func peerPlanForRun(ctx context.Context, runner *exec.Runner, cfg *Config) (*PeerPlan, error) {
	if cfg == nil || cfg.LocalPaths == nil {
		return nil, fmt.Errorf("peer plan: local paths unresolved")
	}
	baseline, err := LoadBaselineManifest(cfg.LocalPaths.BaselineFile)
	if err != nil {
		return nil, fmt.Errorf("peer plan: loading baseline: %w", err)
	}
	if err := ValidatePeerBaselineLocalTypes(cfg, baseline); err != nil {
		return nil, err
	}
	if err := PreparePeerPlanFilters(cfg); err != nil {
		return nil, fmt.Errorf("peer plan: preparing filters: %w", err)
	}
	local, err := InventoryPeer(cfg)
	if err != nil {
		return nil, err
	}
	remote, err := peerRemoteInventory(ctx, runner, cfg, baseline)
	if err != nil {
		return nil, err
	}
	return PlanPeerReconcile(baseline, local, remote)
}

// peerHomeSync exchanges the explicitly listed host paths.
//
// --ignore-missing-args matters: the list is shared between machines and a few
// entries legitimately do not exist on every one. A missing path must not abort
// a transfer that has already moved gigabytes.
func peerHomeSync(ctx context.Context, runner *exec.Runner, cfg *Config, progress func(PeerEvent), dryRun, pushOnly, pullOnly bool) error {
	list := PeerHomePathsFile(cfg.LocalPaths)
	if _, err := os.Stat(list); err != nil {
		emitPeer(progress, PeerEvent{Kind: PeerEventHostPathsMissing, Path: list})
		return nil
	}
	home := cfg.HomeDir()
	// --update keeps a peer's newer host config from being overwritten by a
	// stale coordinator copy. Ordinary host-path updates are deliberately not
	// backed up: they are not workspace conflicts, and an unconditional backup
	// on every run made `.dot-peer-conflicts` grow with routine state churn. A
	// future verified host-baseline conflict can opt into a scoped backup.
	// Without --update this pass overwrites host config unconditionally, and the
	// first real run did exactly that: it replaced this machine's
	// ~/.ssh/known_hosts with the peer's copy, deleting the host key entry for
	// the very channel the sync runs over. The next ssh failed with "Host key
	// verification failed".
	//
	// The exclusions are machine-local trust and runtime state. known_hosts is
	// per-machine by nature - merging it is meaningless and losing it is
	// self-inflicted denial of service. Agent sockets are not files worth moving.
	// .codex/config.toml is excluded in code, not just from the seed template:
	// home-paths.txt is seed-once, so lists written before the entry was removed
	// still carry it, and Codex hash-keys its Keychain MCP OAuth credentials to
	// this file's server definitions - copying a peer's copy orphans them.
	base := []string{"-aHAX", "--numeric-ids", "-r", "--human-readable", "--stats",
		"--ignore-missing-args", "--chmod=Du+w",
		"--update",
		"--exclude=known_hosts", "--exclude=known_hosts.old", "--exclude=known_hosts2",
		"--exclude=agent", "--exclude=agent/**", "--exclude=*.sock",
		"--exclude=/.codex/config.toml",
		"--exclude=.DS_Store",
		"--files-from=" + list}
	if cfg.RemoteRsyncPath != "" {
		base = append(base, "--rsync-path="+cfg.RemoteRsyncPath)
	}
	if dryRun {
		base = append(base, "--dry-run")
	}
	base = append(base, "-e", "ssh")
	remote := cfg.Target.Host + ":"

	// Pull first, same reasoning as the workspace pass: the additive direction
	// records a conflict before this machine's version goes out.
	if !pushOnly {
		args := append(append([]string{}, base...), remote, home+"/")
		if err := runPeerRsync(ctx, runner, cfg, args); err != nil {
			return err
		}
	}
	if !pullOnly {
		args := append(append([]string{}, base...), home+"/", remote)
		if err := runPeerRsync(ctx, runner, cfg, args); err != nil {
			return err
		}
	}
	return nil
}

func runPeerRsync(ctx context.Context, runner *exec.Runner, cfg *Config, args []string) error {
	if cfg.Verbose {
		return ClassifyRsyncError(runner.RunAttached(ctx, "rsync", args...))
	}
	_, err := runner.Run(ctx, "rsync", args...)
	return ClassifyRsyncError(err)
}

// failOnPartial announces rsync's partial-transfer outcome, then returns it as
// a hard transaction failure. Exit 23/24 can mean a scoped conflict or delete
// did not finish; later passes and baseline publication must stop immediately.
// The wording of the announcement belongs to cli (D-06); only the
// classification and the abort decision are here.
func failOnPartial(progress func(PeerEvent), err error) error {
	if err == nil {
		return nil
	}
	if IsPartialTransfer(err) {
		emitPeer(progress, PeerEvent{Kind: PeerEventPartialTransfer, Err: err})
		return fmt.Errorf("peer sync incomplete: %w", err)
	}
	return err
}
