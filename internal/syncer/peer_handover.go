package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// Handover and takeover move the peer coordinator role between the two Macs.
//
// The model is one active Mac at a time, switchable at any moment. Git state
// never moves machine to machine; the baseline either starts empty (planned
// switch: an additive bootstrap right after a final complete run, so no
// deletes can be planned) or comes from the replica the last coordinator
// pushed after every complete run (unplanned switch). max_delete is never
// raised to make a switch work — the 09-04 coordinator switch with a
// mismatched baseline planned 8,050 deletes and only max_delete=100 stopped
// it, which is why these are the only two ways a new coordinator starts.
//
// ponytail: known ceiling. See docs/CEILINGS.md (unplanned peer switch window).
// ponytail: known ceiling. See docs/CEILINGS.md (replica bootstrap trust).

const (
	peerReplicaDirName       = "replica"
	peerReplicaGenerationKey = "replica-generation"
	peerSchedulerLabel       = "com.dotfiles.peer"
)

// peerReplicaMeta is meta.yaml inside a pushed replica: provenance plus one
// sha256 per replicated file. Only takeover reads the replica.
type peerReplicaMeta struct {
	Generation  int               `yaml:"generation"`
	Coordinator string            `yaml:"coordinator"`
	Epoch       int               `yaml:"epoch"`
	Target      string            `yaml:"target"`
	Files       map[string]string `yaml:"files"`
}

// peerReplicaSources names the files a complete run replicates to the peer
// store: both baselines with their target markers and the five filter files
// the receiving operator must be able to diff before a takeover.
func peerReplicaSources(cfg *Config) map[string]string {
	homeBaseline := peerHomeBaselineFile(cfg.LocalPaths)
	return map[string]string{
		"baseline.manifest":         cfg.LocalPaths.BaselineFile,
		"baseline.peer-target":      baselineTargetFileFor(cfg.LocalPaths.BaselineFile, peerBaselineTargetName),
		"baseline-home.manifest":    homeBaseline,
		"baseline-home.peer-target": baselineTargetFileFor(homeBaseline, homeBaselineTargetName),
		"exclude.txt":               cfg.LocalPaths.ExcludeFile,
		"ignore.txt":                cfg.LocalPaths.IgnoreFile,
		"allow.txt":                 cfg.LocalPaths.AllowFile,
		"home-paths.txt":            PeerHomePathsFile(cfg.LocalPaths),
		"home-paths-tracked.txt":    PeerHomeTrackedFile(cfg.LocalPaths),
	}
}

func peerReplicaDir(paths *LocalPaths) string {
	return filepath.Join(paths.StoreDir, peerReplicaDirName)
}

func peerReplicaGenerationFile(paths *LocalPaths) string {
	return filepath.Join(paths.StoreDir, peerReplicaGenerationKey)
}

// readPeerReplicaGeneration returns the last replica generation this store
// produced or installed (0 when none). The one counter serves both roles:
// the coordinator increments it on every push, and a former coordinator or a
// takeover compares it against the replica it is offered.
func readPeerReplicaGeneration(paths *LocalPaths) (int, error) {
	raw, err := os.ReadFile(peerReplicaGenerationFile(paths))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("peer replica generation file corrupt: %q", strings.TrimSpace(string(raw)))
	}
	return n, nil
}

func writePeerReplicaGeneration(paths *LocalPaths, generation int) error {
	return atomicWrite(peerReplicaGenerationFile(paths), []byte(strconv.Itoa(generation)+"\n"))
}

// PushPeerReplica replicates the just-committed baselines and the filter
// files to <peer store>/replica/ on the remote, with meta.yaml recording
// generation, coordinator, epoch and a sha256 per file. Called after every
// complete two-way run; only takeover ever reads the result.
func PushPeerReplica(ctx context.Context, runner *exec.Runner, cfg *Config) error {
	if cfg == nil || cfg.Profile != PeerProfile || !cfg.Target.IsSSH() || cfg.LocalPaths == nil {
		return fmt.Errorf("peer replica: requires an SSH peer profile")
	}
	generation, err := readPeerReplicaGeneration(cfg.LocalPaths)
	if err != nil {
		return err
	}
	generation++

	replicaDir := peerReplicaDir(cfg.LocalPaths)
	if err := os.RemoveAll(replicaDir); err != nil {
		return fmt.Errorf("peer replica: resetting staging: %w", err)
	}
	if err := os.MkdirAll(replicaDir, 0o755); err != nil {
		return fmt.Errorf("peer replica: staging: %w", err)
	}

	meta := peerReplicaMeta{
		Generation:  generation,
		Coordinator: cfg.Owner,
		Epoch:       cfg.OwnerEpoch,
		Target:      cfg.Target.String(),
		Files:       map[string]string{},
	}
	for name, src := range peerReplicaSources(cfg) {
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			continue // optional layers (home baselines, home-path lists) may not exist yet
		}
		if err != nil {
			return fmt.Errorf("peer replica: reading %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		if err := os.WriteFile(filepath.Join(replicaDir, name), data, 0o644); err != nil {
			return fmt.Errorf("peer replica: staging %s: %w", name, err)
		}
		meta.Files[name] = hex.EncodeToString(sum[:])
	}
	metaBody, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("peer replica: encoding meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(replicaDir, "meta.yaml"), metaBody, 0o644); err != nil {
		return fmt.Errorf("peer replica: staging meta.yaml: %w", err)
	}

	remoteDir := strings.TrimRight(cfg.Target.Path, "/") + "/.dotfiles/peer/replica/"
	// mkdir -p over ssh instead of rsync --mkpath: macOS peers may only offer
	// openrsync, which predates --mkpath (rsync 3.2.3) and rejects the flag.
	if _, err := runner.Run(ctx, "ssh",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		cfg.Target.Host, "mkdir -p "+shellQuote(remoteDir)); err != nil {
		return fmt.Errorf("peer replica: creating %s on %s: %w", remoteDir, cfg.Target.Host, err)
	}
	// --checksum, not the default size+mtime quick check: the staging files
	// are rewritten every run, and a same-size meta.yaml written in the same
	// second as the previous one would otherwise be skipped, stalling the
	// very generation a takeover compares. The payload is a few small files
	// plus the baselines, so hashing both sides is cheap.
	args := []string{"-a", "--checksum", "--delete"}
	if cfg.RemoteRsyncPath != "" {
		// A peer whose default rsync is openrsync/2.x can only be served by
		// the alternate the probe selected (rsyncbin.go); bare `rsync` on the
		// remote side would fail every push.
		args = append(args, "--rsync-path="+cfg.RemoteRsyncPath)
	}
	args = append(args, "-e", "ssh -o BatchMode=yes -o ConnectTimeout=5", replicaDir+"/", cfg.Target.Host+":"+remoteDir)
	if _, err := runner.Run(ctx, cfg.rsyncBin(), args...); err != nil {
		return fmt.Errorf("peer replica: pushing to %s: %w", cfg.Target.Host, err)
	}
	if err := writePeerReplicaGeneration(cfg.LocalPaths, generation); err != nil {
		return fmt.Errorf("peer replica: recording generation: %w", err)
	}
	return nil
}

// peerFence compares coordinator epochs on first contact, before any
// transfer, and decides what this machine may do:
//
//   - remote epoch higher: the remote won. demote=true; the caller adopts the
//     remote's owner and epoch, removes its scheduler and stops (bootout last,
//     because from inside the scheduled job bootout ends the job itself).
//   - local epoch higher: proceed; the remote demotes itself on its own run.
//   - equal epochs, different owners: both sides refuse.
//   - remote without epoch support (a previous release): the pre-epoch
//     owner-mismatch check decides, and legacy=true asks the caller to say
//     which features the peer's version lacks.
func peerFence(cfg *Config, remote *remotePeerStatus) (demote, legacy bool, err error) {
	if err := checkRemotePeerTopology(cfg, remote); err != nil {
		return false, false, err
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return false, false, fmt.Errorf("peer coordinator check: local peer owner is empty; set one with `dot sync owner --profile=peer --set <coordinator>`")
	}
	legacy = remote.DotVersion == ""
	if remote.OwnerEpoch == 0 {
		// A remote without epoch support refuses (or proceeds) through the
		// existing owner-mismatch check, exactly as before epochs existed.
		return false, legacy, checkRemotePeerOwnerMatch(cfg, remote)
	}
	switch {
	case remote.OwnerEpoch > cfg.OwnerEpoch:
		return true, false, nil
	case cfg.OwnerEpoch > remote.OwnerEpoch:
		return false, false, nil
	}
	// At equal epochs only one machine may pass its owner guard. The peer's
	// canPush is its own verdict, so names and aliases that happen to match
	// cannot admit a second coordinator.
	if remote.Profile.CanPush {
		return false, false, fmt.Errorf(
			"peer fence: equal owner epochs (%d) and the peer also passes its own owner guard (its owner %q, local %q); both sides refuse — set one coordinator with `dot sync owner --profile=peer --set <machine>` on both machines",
			cfg.OwnerEpoch, remote.Profile.Owner, cfg.Owner)
	}
	if !sameOwner(cfg.Owner, cfg.OwnerAliases, remote.Profile.Owner, remote.Profile.OwnerAliases) {
		return false, false, fmt.Errorf(
			"peer fence: equal owner epochs (%d) with different owners (local %q, remote %q); both sides refuse — pick one coordinator and set it with `dot sync owner --profile=peer --set <machine>`",
			cfg.OwnerEpoch, cfg.Owner, remote.Profile.Owner)
	}
	return false, false, nil
}

// recheckPeerOwnerBeforeMutation re-reads the remote owner immediately before
// the first remote mutation after the pull pass. A coordinator change during
// the pull must stop the run before PropagateDeletes or PushPeerPlan moves
// anything; the fence outcome is re-evaluated, and a loss here is an abort,
// not a demotion (demotion happens at the next run's fence, before transfer).
func recheckPeerOwnerBeforeMutation(ctx context.Context, probe *exec.Runner, cfg *Config) error {
	remote, err := fetchRemotePeerStatus(ctx, probe, cfg)
	if err != nil {
		return fmt.Errorf("peer owner re-check before remote mutation: %w", err)
	}
	demote, _, err := peerFence(cfg, remote)
	if err != nil {
		return fmt.Errorf("peer owner re-check before remote mutation: %w", err)
	}
	if demote {
		return fmt.Errorf(
			"peer owner re-check before remote mutation: the peer now holds the higher epoch (%d > %d); aborting before any remote mutation — this machine will demote itself at the next run's fence",
			remote.OwnerEpoch, cfg.OwnerEpoch)
	}
	return nil
}

// PeerAdoptOptions drives one exact write of the coordinator fields. Unlike
// SetOwner it never derives or bumps anything: handover, takeover and the
// fence all need the values recorded verbatim.
type PeerAdoptOptions struct {
	Self              bool   // owner = this machine's preferred name
	Owner             string // explicit owner (ignored when Self is set)
	Epoch             int
	ReplicaGeneration int // <0 leaves the replica counter alone
	FencePending      bool
}

// PeerAdopt records owner, epoch and fence state in the local peer store.
func PeerAdopt(cfg *Config, opts PeerAdoptOptions) (string, error) {
	if cfg == nil || cfg.LocalPaths == nil {
		return "", fmt.Errorf("peer adopt: local paths unresolved")
	}
	owner := opts.Owner
	if opts.Self {
		owner = PreferredMachineName()
		if owner == "" {
			return "", fmt.Errorf("peer adopt: cannot determine this machine's name")
		}
	}
	if strings.TrimSpace(owner) == "" {
		return "", fmt.Errorf("peer adopt: owner is empty")
	}
	if opts.Epoch < 0 {
		return "", fmt.Errorf("peer adopt: negative epoch %d", opts.Epoch)
	}
	local, ok, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return "", err
	}
	if !ok || local == nil {
		return "", fmt.Errorf("peer adopt: profile %q has no config yet; run dot peer init first", cfg.Profile)
	}
	AssignOwner(local, owner)
	local.OwnerEpoch = opts.Epoch
	local.FencePending = opts.FencePending
	if err := SaveLocalConfig(cfg.LocalPaths, local); err != nil {
		return "", err
	}
	if opts.ReplicaGeneration >= 0 {
		if err := writePeerReplicaGeneration(cfg.LocalPaths, opts.ReplicaGeneration); err != nil {
			return "", err
		}
	}
	return owner, nil
}

// removePeerSchedulerArtifacts deletes the peer plist and only then boots the
// job out: from inside the scheduled job, bootout ends the job itself, so it
// must be the last action of a demotion.
func removePeerSchedulerArtifacts(ctx context.Context, runner *exec.Runner, cfg *Config) error {
	plist := filepath.Join(cfg.HomeDir(), "Library", "LaunchAgents", peerSchedulerLabel+".plist")
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("peer demotion: removing plist: %w", err)
	}
	_, _ = runner.Run(ctx, "launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+peerSchedulerLabel)
	return nil
}

// demotePeer loses the fence: adopt the winner's owner and epoch, drop any
// pending fence, and leave no plist or loaded job behind. A preview writes
// nothing.
func demotePeer(ctx context.Context, runner *exec.Runner, cfg *Config, adoptedOwner string, epoch int, dryRun bool) error {
	if dryRun {
		return nil
	}
	if _, err := PeerAdopt(cfg, PeerAdoptOptions{Owner: adoptedOwner, Epoch: epoch}); err != nil {
		return fmt.Errorf("peer demotion: %w", err)
	}
	return removePeerSchedulerArtifacts(ctx, runner, cfg)
}

// clearPeerFencePending records that the first complete run after contact
// happened: the fence has done its job and normal reachability and
// remote-owner requirements apply again.
func clearPeerFencePending(cfg *Config) error {
	local, ok, err := LoadLocalConfig(cfg.LocalPaths)
	if err != nil {
		return err
	}
	if !ok || local == nil || !local.FencePending {
		return nil
	}
	local.FencePending = false
	return SaveLocalConfig(cfg.LocalPaths, local)
}

// peerReplicaFilterDiff describes one filter file's state relative to the
// replica, for the takeover confirmation.
type peerReplicaFilterDiff struct {
	Name   string
	Status string // "same", "differs", "only local", "only replica"
}

// loadPeerReplica reads and validates the replica in this store: meta.yaml
// parses, its target is the reverse of this profile, every listed file is
// present with a matching sha256, and the generation is not older than the
// last one this store saw.
func loadPeerReplica(cfg *Config) (*peerReplicaMeta, string, error) {
	replicaDir := peerReplicaDir(cfg.LocalPaths)
	raw, err := os.ReadFile(filepath.Join(replicaDir, "meta.yaml"))
	if os.IsNotExist(err) {
		return nil, "", fmt.Errorf("no replica in the peer store: the coordinator pushes one after every complete run; without it a takeover would repeat the 09-04 mismatched-baseline switch")
	}
	if err != nil {
		return nil, "", fmt.Errorf("peer replica: %w", err)
	}
	var meta peerReplicaMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return nil, "", fmt.Errorf("peer replica: invalid meta.yaml: %w", err)
	}
	target, err := ParseTarget(meta.Target)
	if err != nil || target.Kind != TargetSSH {
		return nil, "", fmt.Errorf("peer replica: meta target %q is not an ssh destination", meta.Target)
	}
	if filepath.Clean(target.Path) != filepath.Clean(strings.TrimRight(cfg.LocalPath, "/")) {
		return nil, "", fmt.Errorf(
			"peer replica: meta target %q is not the reverse of this profile (this workspace is %q); refusing to install another pair's baseline",
			meta.Target, strings.TrimRight(cfg.LocalPath, "/"))
	}
	if len(meta.Files) == 0 {
		return nil, "", fmt.Errorf("peer replica: meta.yaml lists no files")
	}
	names := make([]string, 0, len(meta.Files))
	for name := range meta.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if filepath.Base(name) != name {
			return nil, "", fmt.Errorf("peer replica: unsafe file name %q", name)
		}
		data, err := os.ReadFile(filepath.Join(replicaDir, name))
		if err != nil {
			return nil, "", fmt.Errorf("peer replica: %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != meta.Files[name] {
			return nil, "", fmt.Errorf("peer replica: %s sha256 mismatch (meta %s, file %s); the replica is corrupt or half-written", name, meta.Files[name], got)
		}
	}
	lastSeen, err := readPeerReplicaGeneration(cfg.LocalPaths)
	if err != nil {
		return nil, "", err
	}
	if meta.Generation < lastSeen {
		return nil, "", fmt.Errorf(
			"peer replica: generation %d is older than the last one this store saw (%d); refusing to install a stale baseline",
			meta.Generation, lastSeen)
	}
	return &meta, replicaDir, nil
}

// diffPeerReplicaFilters compares the replica's filter files against the
// local store's, for the confirmation a takeover requires.
func diffPeerReplicaFilters(cfg *Config, replicaDir string, meta *peerReplicaMeta) []peerReplicaFilterDiff {
	filterNames := []string{"exclude.txt", "ignore.txt", "allow.txt", "home-paths.txt", "home-paths-tracked.txt"}
	sources := peerReplicaSources(cfg)
	var diffs []peerReplicaFilterDiff
	for _, name := range filterNames {
		localData, localErr := os.ReadFile(sources[name])
		_, inReplica := meta.Files[name]
		replicaData, replicaErr := os.ReadFile(filepath.Join(replicaDir, name))
		switch {
		case os.IsNotExist(localErr) && (!inReplica || os.IsNotExist(replicaErr)):
			continue
		case os.IsNotExist(localErr):
			diffs = append(diffs, peerReplicaFilterDiff{name, "only replica"})
		case !inReplica || os.IsNotExist(replicaErr):
			diffs = append(diffs, peerReplicaFilterDiff{name, "only local"})
		case string(localData) == string(replicaData):
			diffs = append(diffs, peerReplicaFilterDiff{name, "same"})
		default:
			diffs = append(diffs, peerReplicaFilterDiff{name, "differs"})
		}
	}
	return diffs
}

// PeerTakeoverOptions controls the unplanned switch. Confirm gates the
// install and receives the validated preview (replica generation and filter
// differences) so the caller can show it before the operator answers; a nil
// Confirm means the caller already consented (tests pass nil with Yes).
type PeerTakeoverOptions struct {
	Yes     bool
	DryRun  bool
	Confirm func(prompt string, preview *PeerTakeoverResult) (bool, error)
}

// PeerTakeoverResult reports what a takeover installed or would install.
type PeerTakeoverResult struct {
	DryRun      bool
	Owner       string
	Epoch       int
	Generation  int
	FilterDiffs []peerReplicaFilterDiff
}

// PeerTakeover makes this machine the coordinator while the old one is away:
// validate the replica, show the filter differences for confirmation, install
// the replica baselines with the target markers rewritten for this profile,
// then adopt self with a higher epoch and a pending fence. max_delete is
// never touched.
func PeerTakeover(cfg *Config, opts PeerTakeoverOptions) (*PeerTakeoverResult, error) {
	if cfg == nil || cfg.Profile != PeerProfile || !cfg.Target.IsSSH() || cfg.LocalPaths == nil {
		return nil, fmt.Errorf("peer takeover: requires an SSH peer profile")
	}
	meta, replicaDir, err := loadPeerReplica(cfg)
	if err != nil {
		return nil, err
	}
	diffs := diffPeerReplicaFilters(cfg, replicaDir, meta)
	result := &PeerTakeoverResult{FilterDiffs: diffs, Generation: meta.Generation}
	if opts.DryRun {
		// A preview validates and shows the differences; it installs nothing
		// and therefore asks nothing.
		result.DryRun = true
		return result, nil
	}
	if !opts.Yes {
		if opts.Confirm == nil {
			return nil, fmt.Errorf("peer takeover: confirmation required (pass --yes to skip)")
		}
		ok, err := opts.Confirm("Install the replica baselines and take over coordination?", result)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("peer takeover: declined")
		}
	}

	// Install the replica baselines with the target markers rewritten for
	// this profile's direction. The markers prove the baseline describes the
	// peer this machine actually syncs with, so delete provenance carries
	// over from the last complete run.
	markers := map[string]string{
		"baseline.peer-target":      cfg.Target.RsyncDest() + "\n",
		"baseline-home.peer-target": cfg.Target.RsyncDest() + "\n",
	}
	baselines := []string{"baseline.manifest", "baseline.peer-target", "baseline-home.manifest", "baseline-home.peer-target"}
	sources := peerReplicaSources(cfg)
	for _, name := range baselines {
		if _, ok := meta.Files[name]; !ok {
			continue
		}
		body, err := os.ReadFile(filepath.Join(replicaDir, name))
		if err != nil {
			return nil, fmt.Errorf("peer takeover: reading replica %s: %w", name, err)
		}
		if marker, isMarker := markers[name]; isMarker {
			body = []byte(marker)
		}
		if err := atomicWrite(sources[name], body); err != nil {
			return nil, fmt.Errorf("peer takeover: installing %s: %w", name, err)
		}
	}
	epoch := cfg.OwnerEpoch
	if meta.Epoch > epoch {
		epoch = meta.Epoch
	}
	owner, err := PeerAdopt(cfg, PeerAdoptOptions{Self: true, Epoch: epoch + 1, ReplicaGeneration: meta.Generation, FencePending: true})
	if err != nil {
		return nil, err
	}
	result.Owner = owner
	result.Epoch = epoch + 1
	return result, nil
}

// PeerHandoverOptions controls the planned switch. Peer, when set, must name
// the configured target host so a handover to the wrong machine cannot start.
type PeerHandoverOptions struct {
	Config *Config
	Runner *exec.Runner
	Probe  *exec.Runner
	Peer   string
	DryRun bool
}

// PeerHandoverResult lists the steps a handover took or would take.
type PeerHandoverResult struct {
	DryRun   bool
	NewOwner string
	Epoch    int
	Steps    []string
}

// PeerHandover moves coordination to the peer, planned, with both machines
// reachable: one complete sync, adopt on the peer then locally, remove the
// local scheduler, set the peer's baselines aside for an additive bootstrap,
// then let the peer sync once and install its scheduler.
func PeerHandover(ctx context.Context, opts PeerHandoverOptions) (*PeerHandoverResult, error) {
	cfg := opts.Config
	if cfg == nil || cfg.Profile != PeerProfile || !cfg.Target.IsSSH() || cfg.LocalPaths == nil {
		return nil, fmt.Errorf("peer handover: requires an SSH peer profile")
	}
	if opts.Peer != "" && opts.Peer != cfg.Target.Host {
		return nil, fmt.Errorf("peer handover: this profile targets %q, not %q; refusing to hand over to the wrong machine", cfg.Target.Host, opts.Peer)
	}
	if err := CheckOwner(cfg); err != nil {
		return nil, fmt.Errorf("peer handover must run on the current coordinator: %w", err)
	}

	result := &PeerHandoverResult{DryRun: opts.DryRun}
	step := func(format string, args ...any) { result.Steps = append(result.Steps, fmt.Sprintf(format, args...)) }

	// 1. One complete sync; a held or partial run leaves the peer without the
	// current state, and a handover on top of it would strand real changes.
	syncRes, err := PeerSync(ctx, PeerSyncOptions{
		Config:   cfg,
		Runner:   opts.Runner,
		Probe:    opts.Probe,
		DryRun:   opts.DryRun,
		Progress: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("peer handover: the required complete sync failed: %w", err)
	}
	if syncRes.Unreachable {
		return nil, fmt.Errorf("peer handover: peer %s is unreachable; a planned switch needs both machines", cfg.Target.Host)
	}
	if !syncRes.Complete {
		return nil, fmt.Errorf("peer handover: the sync held destructive transitions; resolve them and re-run `dot peer sync` cleanly before handing over")
	}
	step("complete sync verified")

	// The peer adopts itself, so it must be able to say its own name. A
	// previous release cannot: adopt does not exist there.
	remote, err := fetchRemotePeerStatus(ctx, opts.Probe, cfg)
	if err != nil {
		return nil, err
	}
	if remote.DotVersion == "" {
		return nil, fmt.Errorf(
			"peer handover: the peer's dot %s predates handover support (its status document carries no dotVersion); upgrade dot on %s first",
			cfg.remoteDot.String(), cfg.Target.Host)
	}
	epoch := cfg.OwnerEpoch + 1
	generation, err := readPeerReplicaGeneration(cfg.LocalPaths)
	if err != nil {
		return nil, err
	}
	result.Epoch = epoch

	if opts.DryRun {
		step("would set owner and epoch %d on the peer, then locally", epoch)
		step("would remove the local scheduler")
		step("would set the peer's baselines aside and run its first sync as an additive bootstrap")
		step("would install the peer's scheduler")
		return result, nil
	}

	// 2. Owner and epoch on the peer first: if the local write then fails,
	// this machine simply stops being allowed to sync, which is the safe
	// direction. The peer prints the exact owner it adopted.
	remoteOwner, err := peerRemoteAdopt(ctx, opts.Runner, cfg, epoch, generation)
	if err != nil {
		return nil, fmt.Errorf("peer handover: the peer's adopt failed or its answer was unusable; nothing changed here, check `dot peer status` on %s (the next sync's fence settles a half-done adopt): %w", cfg.Target.Host, err)
	}
	result.NewOwner = remoteOwner
	if _, err := PeerAdopt(cfg, PeerAdoptOptions{Owner: remoteOwner, Epoch: epoch}); err != nil {
		return nil, fmt.Errorf(
			"peer handover: the peer adopted %q (epoch %d) but the local write failed: %w. The peer is now coordinator; set this machine's owner with `dot sync owner --profile=peer --set %s`",
			remoteOwner, epoch, err, remoteOwner)
	}
	step("owner is %q (epoch %d) on both machines", remoteOwner, epoch)

	// 3. Local scheduler off.
	if err := removePeerSchedulerArtifacts(ctx, opts.Runner, cfg); err != nil {
		return nil, fmt.Errorf("peer handover: removing the local scheduler (owner already moved): %w", err)
	}
	step("local scheduler removed")

	// 4. On the peer: old baselines aside, then the first sync as an additive
	// bootstrap. No baseline means no deletes can be planned, and right after
	// step 1 almost nothing transfers.
	if err := peerRemoteBaselinesAside(ctx, opts.Runner, cfg); err != nil {
		return nil, err
	}
	if _, err := peerRemoteDot(ctx, opts.Runner, cfg, "peer", "sync"); err != nil {
		return nil, fmt.Errorf(
			"peer handover: the peer's bootstrap sync failed: %w. Ownership already moved; re-run `dot peer sync` on %s, then `dot peer setup` there",
			err, cfg.Target.Host)
	}
	step("peer baseline set aside; bootstrap sync complete")

	// 5. The peer's scheduler on.
	if _, err := peerRemoteDot(ctx, opts.Runner, cfg, "peer", "setup"); err != nil {
		return nil, fmt.Errorf(
			"peer handover: installing the peer's scheduler failed: %w. Ownership already moved; run `dot peer setup` on %s to finish",
			err, cfg.Target.Host)
	}
	step("peer scheduler installed")
	return result, nil
}

// peerRemoteDotCommand builds an exact argv for a dot subcommand on the
// resolved peer binary, each argument single-quoted. The binary is the one the
// status probe uses, so a peer reachable by `peer status` is reachable here.
func peerRemoteDotCommand(dot string, args ...string) (string, error) {
	var b strings.Builder
	b.WriteString("exec " + shellQuote(dot))
	for _, arg := range args {
		if strings.ContainsAny(arg, "'\n") {
			return "", fmt.Errorf("unsafe remote dot argument %q", arg)
		}
		b.WriteString(" '" + arg + "'")
	}
	return b.String(), nil
}

// peerRemoteDot runs a dot subcommand on the peer and returns its stdout.
func peerRemoteDot(ctx context.Context, runner *exec.Runner, cfg *Config, args ...string) (string, error) {
	dot, err := resolveRemoteDot(ctx, runner, cfg)
	if err != nil {
		return "", err
	}
	cmd, err := peerRemoteDotCommand(dot.Path, args...)
	if err != nil {
		return "", err
	}
	res, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", cfg.Target.Host, cmd)
	if err != nil {
		return "", fmt.Errorf("remote `dot %s` on %s failed: %w", strings.Join(args, " "), cfg.Target.Host, err)
	}
	return res.Stdout, nil
}

// PeerView is what an owner rename asks the other Mac: the names it answers
// to and its peer scheduler's state.
type PeerView struct {
	MachineNames []string
	Scheduler    string // "not installed" when it has none; "" when unknown
}

// PeerOwnerView reads the peer's status document over ssh.
func PeerOwnerView(ctx context.Context, runner *exec.Runner, cfg *Config) (*PeerView, error) {
	if err := CheckSSH(ctx, runner, cfg.Target.Host); err != nil {
		return nil, err
	}
	status, err := fetchRemotePeerStatus(ctx, runner, cfg)
	if err != nil {
		return nil, err
	}
	return &PeerView{MachineNames: status.Profile.MachineNames, Scheduler: status.Job.State}, nil
}

// RenamePeerOwner applies `dot sync owner --rename <old> <new> --local-only`
// on the peer, through the same resolved dot binary the status probe uses.
func RenamePeerOwner(ctx context.Context, runner *exec.Runner, cfg *Config, oldName, newName string) error {
	if err := CheckSSH(ctx, runner, cfg.Target.Host); err != nil {
		return err
	}
	_, err := peerRemoteDot(ctx, runner, cfg, "sync", "owner", "--rename", "--local-only", oldName, newName)
	return err
}

// peerRemoteAdopt asks the peer to adopt itself as coordinator and returns
// the exact owner name it recorded (the last stdout line).
func peerRemoteAdopt(ctx context.Context, runner *exec.Runner, cfg *Config, epoch, generation int) (string, error) {
	out, err := peerRemoteDot(ctx, runner, cfg,
		"peer", "adopt", "--self",
		"--epoch", strconv.Itoa(epoch),
		"--replica-generation", strconv.Itoa(generation))
	if err != nil {
		return "", err
	}
	owner := strings.TrimSpace(out)
	if i := strings.LastIndex(owner, "\n"); i >= 0 {
		owner = strings.TrimSpace(owner[i+1:])
	}
	if owner == "" || strings.ContainsAny(owner, " \t'\"/") {
		return "", fmt.Errorf("the peer reported an unusable owner name %q", owner)
	}
	return owner, nil
}

// peerRemoteBaselinesAside moves the peer's current baselines and target
// markers aside so the peer's first sync as coordinator is an additive
// bootstrap: with no baseline, no deletes can be planned, and right after the
// handover's complete sync almost nothing transfers. The files stay in the
// store with a timestamp suffix; nothing is deleted.
func peerRemoteBaselinesAside(ctx context.Context, runner *exec.Runner, cfg *Config) error {
	store := strings.TrimRight(cfg.Target.Path, "/") + "/.dotfiles/peer"
	if strings.ContainsAny(store, "'\n") {
		return fmt.Errorf("peer handover: unsafe peer store path %q", store)
	}
	cmd := `set -eu
store='` + store + `'
stamp=$(date -u +%Y%m%dT%H%M%SZ)
for f in baseline.manifest baseline.peer-target baseline-home.manifest baseline-home.peer-target; do
  if [ -e "$store/$f" ]; then
    mv "$store/$f" "$store/$f.set-aside-$stamp"
  fi
done`
	if _, err := runner.Run(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", cfg.Target.Host, cmd); err != nil {
		return fmt.Errorf("peer handover: setting the peer's baselines aside: %w", err)
	}
	return nil
}
