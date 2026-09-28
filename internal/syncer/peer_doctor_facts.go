package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/entelecheia/dotfiles-v2/internal/exec"
)

// PeerSideFacts are one machine's answers to the pre-reunion checks (#182).
// `dot peer doctor --self` prints this machine's; the doctor on the
// other Mac reads them over ssh and compares both sides.
type PeerSideFacts struct {
	MachineNames []string `json:"machineNames"`
	// PreferredName is what `dot peer adopt --self` records here.
	PreferredName string            `json:"preferredName,omitempty"`
	DotPath       string            `json:"dotPath"`
	DotVersion    string            `json:"dotVersion"`
	RsyncPath     string            `json:"rsyncPath,omitempty"`
	RsyncVersion  string            `json:"rsyncVersion,omitempty"`
	RsyncError    string            `json:"rsyncError,omitempty"`
	NFDMarked     bool              `json:"nfdMarked"`
	NonNFD        int               `json:"nonNfd"`
	NonNFDSample  []string          `json:"nonNfdSample,omitempty"`
	NonNFDError   string            `json:"nonNfdError,omitempty"`
	Owner         string            `json:"owner"`
	OwnerEpoch    int               `json:"ownerEpoch"`
	FencePending  bool              `json:"fencePending,omitempty"`
	Coordinator   bool              `json:"coordinator"`
	Scheduler     bool              `json:"scheduler"`
	Replica       *PeerReplicaFacts `json:"replica,omitempty"`
	// ReplicaError is why `dot peer takeover` would refuse the replica.
	ReplicaError string            `json:"replicaError,omitempty"`
	MaxDelete    int               `json:"maxDelete"`
	Propagation  PropagationPolicy `json:"propagation"`
	// WorkspacePath and TargetPath are the profile's two ends, for the
	// fence's topology check; TargetHost is the target's ssh host.
	WorkspacePath string `json:"workspacePath"`
	TargetPath    string `json:"targetPath"`
	TargetHost    string `json:"targetHost,omitempty"`
	// Filters maps each peer filter file to its sha256, or "absent".
	Filters map[string]string `json:"filters"`
}

// PeerReplicaFacts describe the takeover replica in this machine's store.
type PeerReplicaFacts struct {
	Generation  int       `json:"generation"`
	Coordinator string    `json:"coordinator"`
	Epoch       int       `json:"epoch"`
	WrittenAt   time.Time `json:"writtenAt"`
}

// LocalPeerSideFacts gathers this machine's facts. It only reads; the NFD
// count walks the workspace like `dot sync names normalize --dry-run`.
func LocalPeerSideFacts(ctx context.Context, probe *exec.Runner, cfg *Config, dotVersion string) *PeerSideFacts {
	f := &PeerSideFacts{
		MachineNames: MachineNames(),

		PreferredName: PreferredMachineName(),
		DotVersion:    dotVersion,
		Owner:         cfg.Owner,
		OwnerEpoch:    cfg.OwnerEpoch,
		FencePending:  cfg.FencePending,
		Coordinator:   IsPeerCoordinator(cfg),
		MaxDelete:     cfg.MaxDelete,
		Propagation:   cfg.Propagation,
		Filters:       map[string]string{},

		WorkspacePath: strings.TrimRight(cfg.LocalPath, "/"),
		TargetPath:    cfg.Target.Path,
		TargetHost:    cfg.Target.Host,
	}
	if exe, err := peerExecutable(); err == nil {
		f.DotPath = exe
	}
	if path, ver, err := LocalRsyncPath(ctx, probe); err != nil {
		f.RsyncError = err.Error()
	} else {
		f.RsyncPath, f.RsyncVersion = path, ver
	}
	if cfg.LocalPaths != nil {
		f.NFDMarked = NFDMigrationMarked(cfg.LocalPaths.WorkspaceRoot)
	}
	// Plan as the sync walks this Mac: the coordinator's preflight skips the
	// linked worktrees a peer run excludes; the other Mac is normalized by
	// `dot sync names normalize` over ssh, which walks them.
	planCfg := *cfg
	var wtErr error
	if f.Coordinator {
		// PeerSync stops on this error before it walks: so does the count.
		planCfg.WorktreeExcludes, wtErr = MergePeerWorktrees(cfg, nil, false)
	}
	if wtErr != nil {
		f.NonNFDError = "linked worktrees: " + wtErr.Error()
	} else if plan, err := PlanWorkspaceNameNormalization(&planCfg); err != nil {
		f.NonNFDError = err.Error()
	} else {
		f.NonNFD = len(plan.Renames)
		for i, r := range plan.Renames {
			if i == 3 {
				break
			}
			f.NonNFDSample = append(f.NonNFDSample, r.OldRel)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.HomeDir(), "Library", "LaunchAgents", peerSchedulerLabel+".plist")); err == nil {
		f.Scheduler = true
	}
	if cfg.LocalPaths != nil {
		f.Replica, f.ReplicaError = localReplicaFacts(cfg)
		for name, path := range peerReplicaSources(cfg) {
			if strings.HasPrefix(name, "baseline") {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				f.Filters[name] = "absent"
				continue
			}
			sum := sha256.Sum256(data)
			f.Filters[name] = hex.EncodeToString(sum[:])
		}
	}
	return f
}

// localReplicaFacts applies the takeover's own checks, so the doctor never
// passes a replica that `dot peer takeover` would refuse.
func localReplicaFacts(cfg *Config) (*PeerReplicaFacts, string) {
	info, err := os.Stat(filepath.Join(peerReplicaDir(cfg.LocalPaths), "meta.yaml"))
	if os.IsNotExist(err) {
		return nil, ""
	}
	meta, _, err := loadPeerReplica(cfg)
	if err != nil {
		return nil, err.Error()
	}
	return &PeerReplicaFacts{Generation: meta.Generation, Coordinator: meta.Coordinator, Epoch: meta.Epoch, WrittenAt: info.ModTime().UTC()}, ""
}

func answersTo(names []string, name string) bool {
	want := NormalizeHostname(name)
	for _, n := range names {
		if want != "" && NormalizeHostname(n) == want {
			return true
		}
	}
	return false
}

// nfdVerdict is whether a coordinator's peer syncs, or its diff and dry
// run, stop over one Mac's names not in NFD, by the sync's rules: the push
// preflight (nfdPushRefusal) and its plan on the coordinator; under a
// marked coordinator, the ssh normalize of the other Mac (whose plan must
// succeed) and the inventory gate its diff and dry run apply; under an
// unmarked one, the pull that brings the other Mac's names as they are, for
// its next preflight to refuse. TestDoctorNFDVerdictMatchesTheSync runs the
// real syncs for each case.
func nfdVerdict(s, o *PeerSideFacts, host, other string) (level, detail, fix string) {
	fix = "on " + host + ": dot sync names normalize --profile=peer --yes"
	var quoted []string
	for _, name := range s.NonNFDSample {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	names := fmt.Sprintf("%d name(s) not in NFD", s.NonNFD)
	if len(quoted) > 0 {
		names += " (" + strings.Join(quoted, ", ") + ")"
	}
	switch {
	case s.Coordinator && s.NonNFDError != "":
		return DoctorFail, host + " (coordinator): cannot plan its names, and its sync stops there too: " + s.NonNFDError, ""
	case s.Coordinator && nfdPushRefusal(s.NFDMarked, s.NonNFD, PeerProfile) != nil:
		return DoctorFail, host + " (coordinator, unmarked): " + names + "; its sync refuses before anything moves", fix
	case s.Coordinator && s.NonNFD > 0:
		return DoctorWarn, host + " (coordinator, NFD-marked): " + names + "; its next sync renames them", ""
	case o.Coordinator && o.NFDMarked && s.NonNFDError != "":
		return DoctorFail, host + ": cannot plan its names, and " + other + "'s sync normalizes them here: " + s.NonNFDError, ""
	case o.Coordinator && o.NFDMarked && s.NonNFD > 0:
		return DoctorFail, host + ": " + names + "; " + other + ", the NFD-marked coordinator, stops its diff and dry run on them (its sync renames them first)", fix
	case o.Coordinator && s.NonNFD > 0:
		return DoctorFail, host + ": " + names + "; the next sync pulls them to " + other + ", unmarked, whose following sync refuses them", fix
	case s.NonNFDError != "":
		return DoctorWarn, host + ": cannot count non-NFD names: " + s.NonNFDError, ""
	case s.NonNFD > 0:
		return DoctorWarn, host + ": " + names, fix
	}
	marker := "unmarked"
	if s.NFDMarked {
		marker = "NFD-marked"
	}
	return DoctorPass, host + ": every name in NFD (" + marker + ")", ""
}

// settledCoordinators is who coordinates once the roles row's fix has run,
// the state every other row judges: a sole coordinator stays one (the fix
// aligns the other Mac); with none, this Mac (the fix adopts it here); of
// two at different epochs the higher (the lower one's next sync only
// demotes it, before it normalizes or moves anything). Two at equal epochs
// stay undecided and are both judged as coordinators.
func settledCoordinators(local, peer *PeerSideFacts) (bool, bool) {
	l, p := local.Coordinator, peer.Coordinator
	switch {
	case l && p && local.OwnerEpoch != peer.OwnerEpoch:
		return local.OwnerEpoch > peer.OwnerEpoch, peer.OwnerEpoch > local.OwnerEpoch
	case !l && !p:
		return true, false
	}
	return l, p
}

// topologyFix names the Mac whose peer target does not point at the other
// Mac's workspace and the target that does, set without touching the
// owner or epoch (dot peer init would record this Mac as the owner).
func topologyFix(local, peer *PeerSideFacts, here, there string) string {
	var fixes []string
	for _, s := range []struct {
		host string
		f, o *PeerSideFacts
	}{{here, local, peer}, {there, peer, local}} {
		if filepath.Clean(s.f.TargetPath) == filepath.Clean(s.o.WorkspacePath) {
			continue
		}
		host := s.f.TargetHost
		if host == "" {
			host = "<ssh host>"
		}
		fixes = append(fixes, fmt.Sprintf("on %s: dot sync target --profile=peer ssh:%s:%s", s.host, host, s.o.WorkspacePath))
	}
	return strings.Join(fixes, "; ")
}

// IsPeerCoordinator reports whether this machine is the peer pair's
// coordinator: a set owner that passes the owner guard here.
func IsPeerCoordinator(cfg *Config) bool {
	return strings.TrimSpace(cfg.Owner) != "" && CheckOwner(cfg) == nil
}

// remotePeerSideFacts runs `dot peer doctor --self` on the peer.
func remotePeerSideFacts(ctx context.Context, runner *exec.Runner, cfg *Config) (*PeerSideFacts, error) {
	out, err := peerRemoteDot(ctx, runner, cfg, "peer", "doctor", "--self")
	if err != nil {
		if strings.Contains(err.Error(), "unknown flag") {
			return nil, fmt.Errorf("%w (the peer's dot predates `peer doctor --self`; upgrade it)", err)
		}
		return nil, err
	}
	var f PeerSideFacts
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		return nil, fmt.Errorf("peer doctor facts: %w", err)
	}
	return &f, nil
}

// Doctor check levels.
const (
	DoctorPass = "pass"
	DoctorWarn = "warn"
	DoctorFail = "fail"
)

// DoctorCheck is one comparison of the two machines, with the command that
// fixes it and the host to run it on.
type DoctorCheck struct {
	Name   string `json:"name"`
	Level  string `json:"level"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// evaluatePeerSides compares both machines' facts (#182). here and there
// name the machines in details and fixes.
func evaluatePeerSides(local, peer *PeerSideFacts, here, there string) []DoctorCheck {
	var checks []DoctorCheck
	add := func(name, level, detail, fix string) {
		checks = append(checks, DoctorCheck{Name: name, Level: level, Detail: detail, Fix: fix})
	}
	type side struct {
		host string
		f    *PeerSideFacts
	}
	// Every row but rsync and roles judges the state the roles fix leaves,
	// so no two fix lines contradict each other.
	lc, pc := settledCoordinators(local, peer)
	el, ep := *local, *peer
	el.Coordinator, ep.Coordinator = lc, pc
	sides := []side{{here, &el}, {there, &ep}}

	// This machine's client is the doctor's first line. Every Mac that passes
	// its owner guard runs one: a sync checks rsync before its fence, so a
	// coordinator about to be demoted needs it to get there.
	switch {
	case peer.RsyncError != "" && peer.Coordinator:
		add("rsync", DoctorFail, there+": "+peer.RsyncError, "on "+there+": brew install rsync")
	case peer.RsyncError != "":
		add("rsync", DoctorWarn, there+": "+peer.RsyncError+" (its client once it coordinates; see remote rsync above for the server this Mac uses)", "on "+there+": brew install rsync")
	default:
		add("rsync", DoctorPass, there+": "+peer.RsyncPath+" ("+peer.RsyncVersion+")", "")
	}

	for i, s := range sides {
		o := sides[1-i]
		level, detail, fix := nfdVerdict(s.f, o.f, s.host, o.host)
		add("nfd", level, detail, fix)
	}
	if local.NFDMarked != peer.NFDMarked {
		marked, other := here, there
		if peer.NFDMarked {
			marked, other = there, here
		}
		add("nfd", DoctorWarn, "the NFD marker is set on "+marked+" only", "on "+other+": dot sync names normalize --profile=peer --yes")
	}

	// Roles: what peerFence decides at the next sync, asked through the same
	// fenceDecision from the side that runs it (the coordinator).
	lower, higher := here, there
	if local.OwnerEpoch > peer.OwnerEpoch {
		lower, higher = there, here
	}
	// The fence checks topology first, on whichever Mac syncs; it is
	// symmetric, so one call answers for both. Facts from an older dot lack
	// the paths.
	topo := topologyError(local.WorkspacePath, local.TargetPath, peer.WorkspacePath, peer.TargetPath)
	switch {
	case (local.Coordinator || peer.Coordinator) && topo != nil && local.WorkspacePath != "" && peer.WorkspacePath != "":
		add("roles", DoctorFail, "every sync is refused: "+topo.Error(), topologyFix(local, peer, here, there))
	case local.Coordinator && peer.Coordinator && local.OwnerEpoch == peer.OwnerEpoch:
		// The fence refuses on both sides, or both write: an operator settles it.
		add("roles", DoctorFail, fmt.Sprintf("both machines pass their owner guard at epoch %d: two coordinators", local.OwnerEpoch), "set one owner on both, a name only that Mac answers to: dot sync owner --profile=peer --set <coordinator>")
	case local.Coordinator && peer.Coordinator:
		// A takeover's pending fence: the lower epoch demotes at its next
		// run. Each side's fence is asked, as each side's sync would.
		lo, hi := local, peer
		if local.OwnerEpoch > peer.OwnerEpoch {
			lo, hi = peer, local
		}
		// The lower side always demotes (the higher epoch is at least 1).
		settle := "on " + lower + ": dot peer sync (its fence demotes it)"
		switch _, herr := fenceDecision(hi.Owner, hi.OwnerEpoch, lo.Owner, lo.OwnerEpoch); {
		case answersTo(lo.MachineNames, hi.Owner):
			// It adopts that owner and epoch and still passes its guard: two
			// writers at one epoch, which the fence lets through.
			add("roles", DoctorFail, fmt.Sprintf("both machines pass their owner guard, and %s answers to %s's owner %q too: its demotion leaves two coordinators", lower, higher, hi.Owner),
				"set one owner on both, a name only that Mac answers to: dot sync owner --profile=peer --set <coordinator>")
		case herr != nil:
			add("roles", DoctorWarn, fmt.Sprintf("both machines pass their owner guard; %s (epoch %d) wins, but its syncs are refused until %s's next sync demotes it (%v)",
				higher, hi.OwnerEpoch, lower, herr), settle)
		default:
			add("roles", DoctorWarn, fmt.Sprintf("both machines pass their owner guard; the fence settles it: %s (epoch %d) wins over %s (epoch %d)",
				higher, hi.OwnerEpoch, lower, lo.OwnerEpoch), settle)
		}
	case !local.Coordinator && !peer.Coordinator:
		// Each owner guard refuses, so no peer sync runs. The Mac in use
		// adopts itself above both epochs; the other records the same owner
		// and epoch, which the fence also needs when it has no epoch yet.
		e := max(local.OwnerEpoch, peer.OwnerEpoch) + 1
		name := local.PreferredName
		if name == "" {
			name = "<this Mac's name>"
		}
		add("roles", DoctorFail, fmt.Sprintf("neither machine is the coordinator (owner %q here, %q on %s)", local.Owner, peer.Owner, there),
			fmt.Sprintf("to coordinate from %s: dot peer adopt --self --epoch %d here, then on %s: dot peer adopt --owner %s --epoch %d", here, e, there, name, e))
	default:
		c, n, coord, other := local, peer, here, there
		if peer.Coordinator {
			c, n, coord, other = peer, local, there, here
		}
		// The settled state: the other Mac records the coordinator's owner
		// and epoch, so the fence proceeds.
		align := fmt.Sprintf("on %s: dot peer adopt --owner %s --epoch %d", other, c.Owner, c.OwnerEpoch)
		demote, err := fenceDecision(c.Owner, c.OwnerEpoch, n.Owner, n.OwnerEpoch)
		switch {
		case err != nil:
			add("roles", DoctorFail, fmt.Sprintf("%s's next sync is refused: %v", coord, err), align)
		case demote && answersTo(c.MachineNames, n.Owner):
			add("roles", DoctorWarn, fmt.Sprintf("%s records epoch %d over %s's %d: %s's next sync demotes it, removing its scheduler and running its on_deactivate hooks, though it stays the owner", other, n.OwnerEpoch, coord, c.OwnerEpoch, coord), align)
		case demote:
			add("roles", DoctorFail, fmt.Sprintf("%s records owner %q at epoch %d over %s's %d: %s's next sync demotes it to that owner, which it does not answer to, leaving no coordinator", other, n.Owner, n.OwnerEpoch, coord, c.OwnerEpoch, coord), align)
		case n.OwnerEpoch != c.OwnerEpoch:
			// The coordinator proceeds; the other Mac never syncs to catch up.
			add("roles", DoctorPass, fmt.Sprintf("coordinator: %s, owner %q (epoch %d here, %d on %s)", coord, c.Owner, local.OwnerEpoch, peer.OwnerEpoch, there), "")
			add("roles", DoctorWarn, fmt.Sprintf("%s still records owner %q at epoch %d (the coordinator's: %q at %d); `dot peer setup` there needs both to name the same owner", other, n.Owner, n.OwnerEpoch, c.Owner, c.OwnerEpoch), align)
		default:
			add("roles", DoctorPass, fmt.Sprintf("coordinator: %s, owner %q (epoch %d here, %d on %s)", coord, c.Owner, local.OwnerEpoch, peer.OwnerEpoch, there), "")
		}
	}

	// Their fixes come after the roles fix, when it has one: dot peer setup
	// checks the owner, and the replica comes from the settled coordinator.
	after := ""
	if slices.ContainsFunc(checks, func(c DoctorCheck) bool { return c.Name == "roles" && c.Level != DoctorPass }) {
		after = "after the roles fix, "
	}
	for _, s := range sides {
		switch {
		case s.f.Coordinator && !s.f.Scheduler:
			add("scheduler", DoctorWarn, s.host+" is the coordinator but has no peer scheduler", after+"on "+s.host+": dot peer setup")
		case !s.f.Coordinator && s.f.Scheduler:
			add("scheduler", DoctorWarn, s.host+" is not the coordinator but has a peer scheduler", after+"on "+s.host+": dot peer setup --off")
		case s.f.Scheduler:
			add("scheduler", DoctorPass, s.host+": peer scheduler installed (coordinator)", "")
		default:
			add("scheduler", DoctorPass, s.host+": no peer scheduler (not the coordinator)", "")
		}
	}

	for _, s := range sides {
		if s.f.Coordinator {
			continue
		}
		if s.f.ReplicaError != "" {
			add("replica", DoctorWarn, s.host+": a takeover there would refuse the replica: "+s.f.ReplicaError, after+"run a complete dot peer sync on the coordinator")
			continue
		}
		if s.f.Replica == nil {
			add("replica", DoctorWarn, "no takeover replica on "+s.host+": a takeover there is not possible", after+"run a complete dot peer sync on the coordinator")
			continue
		}
		if slices.ContainsFunc(s.f.MachineNames, func(n string) bool {
			return NormalizeHostname(n) == NormalizeHostname(s.f.Replica.Coordinator)
		}) {
			add("replica", DoctorWarn, s.host+": the replica there was staged by "+s.host+" itself, not pushed by the coordinator", after+"run a complete dot peer sync on the coordinator")
			continue
		}
		age := time.Since(s.f.Replica.WrittenAt).Round(time.Minute)
		add("replica", DoctorPass, fmt.Sprintf("%s: replica generation %d from %s, written %s ago", s.host, s.f.Replica.Generation, s.f.Replica.Coordinator, age), "")
	}

	if local.MaxDelete != peer.MaxDelete {
		add("config", DoctorWarn, fmt.Sprintf("max_delete is %d here and %d on %s; only the coordinator's applies", local.MaxDelete, peer.MaxDelete, there), "align .dotfiles/peer/config.yaml on both")
	}
	if local.Propagation != peer.Propagation {
		add("config", DoctorWarn, fmt.Sprintf("propagation differs (%+v here, %+v on %s)", local.Propagation, peer.Propagation, there), "align .dotfiles/peer/config.yaml on both")
	}
	var drift []string
	for name, sum := range local.Filters {
		if peer.Filters[name] != sum {
			drift = append(drift, name)
		}
	}
	for name := range peer.Filters {
		if _, ok := local.Filters[name]; !ok {
			drift = append(drift, name)
		}
	}
	sort.Strings(drift)
	if len(drift) > 0 {
		add("config", DoctorWarn, "filter files differ: "+strings.Join(drift, ", "), "copy the coordinator's .dotfiles/peer filter files to the other Mac")
	}
	if checks[len(checks)-1].Name != "config" {
		add("config", DoctorPass, "max_delete, propagation and filter files match", "")
	}
	return checks
}
