package watchdog

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Warp self-heal: health detection, the heal state machine, and the
// consecutive-failure state the root LaunchDaemon persists between passes.

// Warp connectivity states parsed from `warp-cli status`.
type WarpConnStatus int

const (
	WarpStatusUnknown WarpConnStatus = iota
	WarpStatusConnected
	WarpStatusConnecting
	WarpStatusDisconnected
)

func (s WarpConnStatus) String() string {
	switch s {
	case WarpStatusConnected:
		return "Connected"
	case WarpStatusConnecting:
		return "Connecting"
	case WarpStatusDisconnected:
		return "Disconnected"
	default:
		return "Unknown"
	}
}

// cgnatPrefix is the WARP-assigned address range the issue pins: an
// interface holding an address in 100.96.0.0/12 proves the tunnel up.
var cgnatPrefix = &net.IPNet{IP: net.IPv4(100, 96, 0, 0), Mask: net.CIDRMask(12, 32)}

// ParseWarpStatus reads `warp-cli status` output, which reports
// "Status update: <state>" possibly followed by a reason paragraph.
func ParseWarpStatus(out string) WarpConnStatus {
	for _, line := range strings.Split(out, "\n") {
		_, rest, found := strings.Cut(line, "Status update:")
		if !found {
			continue
		}
		switch strings.TrimSpace(rest) {
		case "Connected":
			return WarpStatusConnected
		case "Connecting":
			return WarpStatusConnecting
		case "Disconnected":
			return WarpStatusDisconnected
		default:
			return WarpStatusUnknown
		}
	}
	return WarpStatusUnknown
}

// HasCGNATAddress reports whether any `inet` line in `ifconfig` output holds
// an address inside 100.96.0.0/12.
func HasCGNATAddress(ifconfigOut string) bool {
	for _, line := range strings.Split(ifconfigOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "inet" {
			continue
		}
		if ip := net.ParseIP(fields[1]); ip != nil && cgnatPrefix.Contains(ip) {
			return true
		}
	}
	return false
}

// WarpHealthy combines the two health signals: warp-cli must report
// Connected AND an interface must hold a CGNAT address. Connected without
// the address is the broken state this heal exists for.
func WarpHealthy(statusOut, ifconfigOut string) bool {
	return ParseWarpStatus(statusOut) == WarpStatusConnected && HasCGNATAddress(ifconfigOut)
}

// WarpState is the persisted heal bookkeeping. DaemonLabel is resolved once
// at install time (kickstart needs it); the rest mutates every pass. The
// state dir is user-owned while the warp daemon runs as root — root writing
// here is fine on macOS, and the files stay 0644/0755.
type WarpState struct {
	DaemonLabel         string    `json:"daemon_label,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Attempts            int       `json:"attempts"` // reconnects tried in the current heal sequence
	LastRestart         time.Time `json:"last_restart,omitempty"`
}

// Warp actions the state machine can decide on.
type WarpAction int

const (
	WarpActionNone        WarpAction = iota // healthy, or failure threshold not reached yet
	WarpActionReconnect                     // warp-cli disconnect && connect
	WarpActionKickstart                     // launchctl kickstart -k the WARP daemon
	WarpActionRateLimited                   // a kickstart was due but the 30-minute limit held
)

func (a WarpAction) String() string {
	switch a {
	case WarpActionReconnect:
		return "reconnect"
	case WarpActionKickstart:
		return "kickstart"
	case WarpActionRateLimited:
		return "rate-limited"
	default:
		return "none"
	}
}

// MaxReconnectAttempts bounds the reconnect loop before escalation to a
// daemon kickstart.
const MaxReconnectAttempts = 3

// WarpRestartMinInterval rate-limits daemon restarts: one per 30 minutes,
// so a hard-down WARP daemon cannot be kickstarted into a flapping loop.
const WarpRestartMinInterval = 30 * time.Minute

// DecideWarpAction folds one health probe into the persisted state and
// returns the action to take plus the state to persist. Pure: clock and
// history are arguments.
//
// Sequence: a healthy probe resets everything. An unhealthy probe increments
// the consecutive-failure count; below fail_threshold the pass is a no-op
// (transients heal themselves). At the threshold the reaper reconnects, up
// to maxReconnectAttempts times; past that it kickstarts the WARP daemon,
// rate-limited to one restart per WarpRestartMinInterval. A rate-limited
// pass leaves the attempt count where it is, so the next pass after the
// window retries the kickstart rather than another reconnect.
func DecideWarpAction(healthy bool, state WarpState, failThreshold int, now time.Time) (WarpAction, WarpState) {
	next := state
	if healthy {
		next.ConsecutiveFailures = 0
		next.Attempts = 0
		return WarpActionNone, next
	}
	next.ConsecutiveFailures++
	if failThreshold <= 0 {
		failThreshold = DefaultWarpFailThreshold
	}
	if next.ConsecutiveFailures < failThreshold {
		return WarpActionNone, next
	}
	if next.Attempts < MaxReconnectAttempts {
		next.Attempts++
		return WarpActionReconnect, next
	}
	if now.Sub(state.LastRestart) < WarpRestartMinInterval {
		return WarpActionRateLimited, next
	}
	next.LastRestart = now
	next.Attempts = 0
	return WarpActionKickstart, next
}

// ResolveWarpDaemonLabel finds the Cloudflare WARP daemon in `launchctl
// list` output. The label is resolved dynamically rather than hardcoded
// because Cloudflare has shipped it under variant names; refusing to install
// when it is absent beats kickstarting the wrong service later.
func ResolveWarpDaemonLabel(launchctlListOut string) (string, error) {
	for _, line := range strings.Split(launchctlListOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		label := fields[len(fields)-1]
		if ValidWarpDaemonLabel(label) {
			return label, nil
		}
	}
	return "", fmt.Errorf("cloudflare WARP daemon not found in `launchctl list`; install and start WARP, then rerun `dot watchdog setup`")
}

// ValidWarpDaemonLabel is the kickstart-time re-validation of the persisted
// label: warp.json lives in the user-writable state dir, and the root daemon
// must never kickstart an arbitrary service a local edit planted there.
func ValidWarpDaemonLabel(label string) bool {
	return strings.HasPrefix(label, "com.cloudflare.") && strings.Contains(label, "warp") && strings.HasSuffix(label, "daemon")
}

// LoadWarpState reads the warp state file. A missing file is a zero state,
// not an error.
func LoadWarpState(path string) (WarpState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return WarpState{}, nil
	}
	if err != nil {
		return WarpState{}, fmt.Errorf("reading warp state: %w", err)
	}
	var s WarpState
	if err := json.Unmarshal(data, &s); err != nil {
		return WarpState{}, fmt.Errorf("parsing warp state %s: %w", path, err)
	}
	return s, nil
}

// SaveWarpState writes the warp state file atomically (see SaveSamples).
func SaveWarpState(path string, s WarpState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding warp state: %w", err)
	}
	return saveJSONAtomic(path, data)
}

// saveJSONAtomic writes data to path via a same-dir temp file and rename,
// creating the parent directory first (see SaveSamples). The temp file gets
// 0644 explicitly: CreateTemp's 0600 would make a root-written warp.json
// unreadable — and unreloadable — for the user's manual passes.
func saveJSONAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dot-state-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("chmod state temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing state: %w", err)
	}
	return nil
}
