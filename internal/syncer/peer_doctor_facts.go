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
	MachineNames []string          `json:"machineNames"`
	DotPath      string            `json:"dotPath"`
	DotVersion   string            `json:"dotVersion"`
	RsyncPath    string            `json:"rsyncPath,omitempty"`
	RsyncVersion string            `json:"rsyncVersion,omitempty"`
	RsyncError   string            `json:"rsyncError,omitempty"`
	NFDMarked    bool              `json:"nfdMarked"`
	NonNFD       int               `json:"nonNfd"`
	NonNFDSample []string          `json:"nonNfdSample,omitempty"`
	NonNFDError  string            `json:"nonNfdError,omitempty"`
	Owner        string            `json:"owner"`
	OwnerEpoch   int               `json:"ownerEpoch"`
	FencePending bool              `json:"fencePending,omitempty"`
	Coordinator  bool              `json:"coordinator"`
	Scheduler    bool              `json:"scheduler"`
	Replica      *PeerReplicaFacts `json:"replica,omitempty"`
	// ReplicaError is why `dot peer takeover` would refuse the replica.
	ReplicaError string            `json:"replicaError,omitempty"`
	MaxDelete    int               `json:"maxDelete"`
	Propagation  PropagationPolicy `json:"propagation"`
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
		DotVersion:   dotVersion,
		Owner:        cfg.Owner,
		OwnerEpoch:   cfg.OwnerEpoch,
		FencePending: cfg.FencePending,
		Coordinator:  strings.TrimSpace(cfg.Owner) != "" && CheckOwner(cfg) == nil,
		MaxDelete:    cfg.MaxDelete,
		Propagation:  cfg.Propagation,
		Filters:      map[string]string{},
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
	if plan, err := PlanWorkspaceNameNormalization(cfg); err != nil {
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

// remotePeerSideFacts runs `dot peer doctor --self` on the peer.
func remotePeerSideFacts(ctx context.Context, runner *exec.Runner, cfg *Config) (*PeerSideFacts, error) {
	out, err := peerRemoteDot(ctx, runner, cfg, "peer", "doctor", "--self")
	if err != nil {
		return nil, fmt.Errorf("%w (a peer dot without `peer doctor --self` needs an upgrade)", err)
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
	sides := []side{{here, local}, {there, peer}}

	for _, s := range sides {
		if s.f.RsyncError != "" {
			add("rsync", DoctorFail, s.host+": "+s.f.RsyncError, "on "+s.host+": brew install rsync")
		} else {
			add("rsync", DoctorPass, s.host+": "+s.f.RsyncPath+" ("+s.f.RsyncVersion+")", "")
		}
	}

	// The inventory stop is the coordinator's: an NFD-marked coordinator
	// requires NFD names from the other Mac (peerRemoteInventory). Its sync
	// normalizes the peer first; its diff and dry run stop on them.
	for i, s := range sides {
		o := sides[1-i]
		fix := "on " + s.host + ": dot sync names normalize --profile=peer"
		switch {
		case s.f.NonNFDError != "":
			add("nfd", DoctorWarn, s.host+": cannot count non-NFD names: "+s.f.NonNFDError, "")
		case s.f.NonNFD > 0 && o.f.Coordinator && o.f.NFDMarked:
			add("nfd", DoctorFail, fmt.Sprintf("%s: %d name(s) not in NFD (%s); %s, the NFD-marked coordinator, stops its peer diff and dry run on them",
				s.host, s.f.NonNFD, strings.Join(s.f.NonNFDSample, ", "), o.host), fix)
		case s.f.NonNFD > 0:
			add("nfd", DoctorWarn, fmt.Sprintf("%s: %d name(s) not in NFD (%s)", s.host, s.f.NonNFD, strings.Join(s.f.NonNFDSample, ", ")), fix)
		}
	}
	if local.NFDMarked != peer.NFDMarked {
		marked, other := here, there
		if peer.NFDMarked {
			marked, other = there, here
		}
		add("nfd", DoctorWarn, "the NFD marker is set on "+marked+" only", "on "+other+": dot sync names normalize --profile=peer")
	}

	lower, higher := here, there
	if local.OwnerEpoch > peer.OwnerEpoch {
		lower, higher = there, here
	}
	switch {
	case local.Coordinator && peer.Coordinator && local.OwnerEpoch == peer.OwnerEpoch:
		// peerFence refuses on both sides: only an operator can settle it.
		add("roles", DoctorFail, fmt.Sprintf("both machines pass their owner guard at epoch %d: two coordinators", local.OwnerEpoch), "set one owner on both: dot sync owner --profile=peer --set <coordinator>")
	case local.Coordinator && peer.Coordinator:
		// A takeover's pending fence: the lower epoch demotes at its next run.
		add("roles", DoctorWarn, fmt.Sprintf("both machines pass their owner guard; the fence settles it: %s (epoch %d) wins over %s (epoch %d)",
			higher, max(local.OwnerEpoch, peer.OwnerEpoch), lower, min(local.OwnerEpoch, peer.OwnerEpoch)),
			"on "+lower+": dot peer sync (its fence demotes it)")
	case !local.Coordinator && !peer.Coordinator:
		add("roles", DoctorWarn, fmt.Sprintf("neither machine is the coordinator (owner %q here, %q on %s)", local.Owner, peer.Owner, there), "on the Mac in use: dot sync owner --profile=peer --set-self")
	default:
		coord := here
		if peer.Coordinator {
			coord = there
		}
		add("roles", DoctorPass, fmt.Sprintf("coordinator: %s (epoch %d here, %d on %s)", coord, local.OwnerEpoch, peer.OwnerEpoch, there), "")
	}
	if local.OwnerEpoch != peer.OwnerEpoch && (!local.Coordinator || !peer.Coordinator) {
		add("roles", DoctorWarn, fmt.Sprintf("owner epochs differ (%d here, %d on %s); the higher wins at the next sync's fence and the other side demotes", local.OwnerEpoch, peer.OwnerEpoch, there), "")
	}

	for _, s := range sides {
		switch {
		case s.f.Coordinator && !s.f.Scheduler:
			add("scheduler", DoctorWarn, s.host+" is the coordinator but has no peer scheduler", "on "+s.host+": dot peer setup")
		case !s.f.Coordinator && s.f.Scheduler:
			add("scheduler", DoctorWarn, s.host+" is not the coordinator but has a peer scheduler", "on "+s.host+": dot peer setup --off")
		}
	}

	for _, s := range sides {
		if s.f.Coordinator {
			continue
		}
		if s.f.ReplicaError != "" {
			add("replica", DoctorWarn, s.host+": a takeover there would refuse the replica: "+s.f.ReplicaError, "run a complete dot peer sync on the coordinator")
			continue
		}
		if s.f.Replica == nil {
			add("replica", DoctorWarn, "no takeover replica on "+s.host+": a takeover there is not possible", "run a complete dot peer sync on the coordinator")
			continue
		}
		if slices.ContainsFunc(s.f.MachineNames, func(n string) bool {
			return NormalizeHostname(n) == NormalizeHostname(s.f.Replica.Coordinator)
		}) {
			add("replica", DoctorWarn, s.host+": the replica there was staged by "+s.host+" itself, not pushed by the coordinator", "run a complete dot peer sync on the coordinator")
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
	return checks
}
