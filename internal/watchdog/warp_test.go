package watchdog

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseWarpStatus(t *testing.T) {
	cases := map[string]WarpConnStatus{
		"Status update: Connected\n":                                         WarpStatusConnected,
		"Status update: Connecting\n":                                        WarpStatusConnecting,
		"Status update: Disconnected\n":                                      WarpStatusDisconnected,
		"Status update: Disconnected\nReason: No network\n":                  WarpStatusDisconnected,
		"Status update: Unable to connect\nReason: authentication expired\n": WarpStatusUnknown,
		"something else entirely\n":                                          WarpStatusUnknown,
		"":                                                                   WarpStatusUnknown,
	}
	for out, want := range cases {
		if got := ParseWarpStatus(out); got != want {
			t.Errorf("ParseWarpStatus(%q) = %v, want %v", out, got, want)
		}
	}
}

const ifconfigWithCGNAT = `utun0: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1280
	inet6 fe80::1%utun0 prefixlen 64 scopeid 0x9
utun4: flags=80d1<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST> mtu 1280
	inet 100.103.27.42 --> 100.103.27.42 netmask 0xffffffff
en0: flags=8863<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet 192.168.1.20 netmask 0xffffff00 broadcast 192.168.1.255
`

const ifconfigNoCGNAT = `en0: flags=8863<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	inet 192.168.1.20 netmask 0xffffff00 broadcast 192.168.1.255
utun1: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST> mtu 1380
	inet 10.8.0.2 --> 10.8.0.1 netmask 0xffffffff
`

func TestHasCGNATAddress(t *testing.T) {
	if !HasCGNATAddress(ifconfigWithCGNAT) {
		t.Error("fixture with 100.103.x must be detected")
	}
	if HasCGNATAddress(ifconfigNoCGNAT) {
		t.Error("fixture with only RFC1918/10.x must not be detected")
	}
	// 100.96.0.0/12 spans 100.96.0.0–100.111.255.255; outside neighbors
	// must not match.
	for _, line := range []string{
		"\tinet 100.95.255.255 --> 100.95.255.255 netmask 0xffffffff",
		"\tinet 100.112.0.1 --> 100.112.0.1 netmask 0xffffffff",
	} {
		if HasCGNATAddress(line) {
			t.Errorf("out-of-range address must not match: %q", line)
		}
	}
	for _, line := range []string{
		"\tinet 100.96.0.1 --> 100.96.0.1 netmask 0xffffffff",
		"\tinet 100.111.255.254 --> 100.111.255.254 netmask 0xffffffff",
	} {
		if !HasCGNATAddress(line) {
			t.Errorf("in-range boundary address must match: %q", line)
		}
	}
}

// TestWarpHealthy_ConnectedWithoutVIPIsUnhealthy is the broken state the
// heal exists for: warp-cli claims Connected but no interface holds the
// CGNAT address, so traffic is not flowing.
func TestWarpHealthy_ConnectedWithoutVIPIsUnhealthy(t *testing.T) {
	if WarpHealthy("Status update: Connected\n", ifconfigNoCGNAT) {
		t.Error("Connected without a 100.96/12 address must be unhealthy")
	}
	if !WarpHealthy("Status update: Connected\n", ifconfigWithCGNAT) {
		t.Error("Connected with a 100.96/12 address must be healthy")
	}
	if WarpHealthy("Status update: Connecting\n", ifconfigWithCGNAT) {
		t.Error("Connecting must be unhealthy even with an address")
	}
}

func TestDecideWarpAction_BelowThresholdIsANoOp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	action, next := DecideWarpAction(false, WarpState{}, 2, now)
	if action != WarpActionNone || next.ConsecutiveFailures != 1 {
		t.Fatalf("first failure = %v %#v, want none with consec 1", action, next)
	}
}

func TestDecideWarpAction_ThresholdTriggersReconnectsThenKickstart(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	state := WarpState{DaemonLabel: "com.cloudflare.1dot1dot1dot1.macos.warp.daemon"}

	// Failure 1: below the threshold, a no-op.
	action, state := DecideWarpAction(false, state, 2, now)
	if action != WarpActionNone || state.ConsecutiveFailures != 1 {
		t.Fatalf("below threshold = %v consec %d, want none with 1", action, state.ConsecutiveFailures)
	}
	// Failure 2 (threshold): first reconnect.
	action, state = DecideWarpAction(false, state, 2, now)
	if action != WarpActionReconnect || state.Attempts != 1 {
		t.Fatalf("at threshold = %v attempts %d, want reconnect 1", action, state.Attempts)
	}
	// Failures 3 and 4: reconnects 2 and 3.
	for want := 2; want <= 3; want++ {
		action, state = DecideWarpAction(false, state, 2, now)
		if action != WarpActionReconnect || state.Attempts != want {
			t.Fatalf("reconnect %d = %v attempts %d", want, action, state.Attempts)
		}
	}
	// Failure 5: reconnects exhausted → kickstart, restart clock stamps.
	action, state = DecideWarpAction(false, state, 2, now)
	if action != WarpActionKickstart {
		t.Fatalf("after %d attempts = %v, want kickstart", MaxReconnectAttempts, action)
	}
	if !state.LastRestart.Equal(now) || state.Attempts != 0 {
		t.Fatalf("kickstart must stamp LastRestart and reset attempts: %#v", state)
	}
	// The sequence restarts: next failure is a reconnect again.
	action, state = DecideWarpAction(false, state, 2, now.Add(time.Minute))
	if action != WarpActionReconnect || state.Attempts != 1 {
		t.Fatalf("post-kickstart sequence = %v attempts %d, want reconnect 1", action, state.Attempts)
	}
}

func TestDecideWarpAction_KickstartRateLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	exhausted := WarpState{ConsecutiveFailures: 10, Attempts: MaxReconnectAttempts, LastRestart: now.Add(-10 * time.Minute)}
	action, next := DecideWarpAction(false, exhausted, 2, now)
	if action != WarpActionRateLimited {
		t.Fatalf("inside the 30-minute window = %v, want rate-limited", action)
	}
	if !next.LastRestart.Equal(exhausted.LastRestart) || next.Attempts != MaxReconnectAttempts {
		t.Fatalf("rate-limited pass must not consume or stamp anything: %#v", next)
	}

	// One restart per 30 minutes: exactly at the window the limit lifts;
	// a second before it, the restart is still held.
	edge := WarpState{ConsecutiveFailures: 10, Attempts: MaxReconnectAttempts, LastRestart: now.Add(-WarpRestartMinInterval)}
	if action, _ := DecideWarpAction(false, edge, 2, now); action != WarpActionKickstart {
		t.Fatalf("at exactly the window edge = %v, want kickstart", action)
	}
	held := WarpState{ConsecutiveFailures: 10, Attempts: MaxReconnectAttempts, LastRestart: now.Add(-WarpRestartMinInterval + time.Second)}
	if action, _ := DecideWarpAction(false, held, 2, now); action != WarpActionRateLimited {
		t.Fatalf("one second inside the window = %v, want rate-limited", action)
	}
}

func TestDecideWarpAction_HealthyResets(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	restart := now.Add(-time.Hour)
	dirty := WarpState{DaemonLabel: "x", ConsecutiveFailures: 9, Attempts: 2, LastRestart: restart}
	action, next := DecideWarpAction(true, dirty, 2, now)
	if action != WarpActionNone {
		t.Fatalf("healthy probe action = %v", action)
	}
	if next.ConsecutiveFailures != 0 || next.Attempts != 0 {
		t.Fatalf("healthy probe must clear counters: %#v", next)
	}
	if next.DaemonLabel != "x" || !next.LastRestart.Equal(restart) {
		t.Fatalf("label and restart clock must survive a reset: %#v", next)
	}
}

func TestResolveWarpDaemonLabel(t *testing.T) {
	listOut := `PID	Status	Label
89173	0	com.cloudflare.1dot1dot1dot1.macos.warp.daemon
-	0	com.apple.WindowServer
89200	0	com.cloudflare.warp.other-helper
`
	label, err := ResolveWarpDaemonLabel(listOut)
	if err != nil {
		t.Fatalf("ResolveWarpDaemonLabel: %v", err)
	}
	if label != "com.cloudflare.1dot1dot1dot1.macos.warp.daemon" {
		t.Fatalf("label = %q", label)
	}
	if _, err := ResolveWarpDaemonLabel("PID\tStatus\tLabel\n- 0 com.apple.WindowServer\n"); err == nil {
		t.Fatal("a list without the WARP daemon must refuse")
	}
	// A helper label that is not the daemon must not be picked.
	if _, err := ResolveWarpDaemonLabel("- 0 com.cloudflare.warp.helper\n"); err == nil {
		t.Fatal("a non-daemon cloudflare label must not satisfy the lookup")
	}
}

func TestWarpState_RoundTrip(t *testing.T) {
	path := t.TempDir() + "/state/warp.json"
	now := time.Unix(1_800_000_000, 0).UTC()
	in := WarpState{DaemonLabel: "com.cloudflare.x.warp.daemon", ConsecutiveFailures: 4, Attempts: 2, LastRestart: now}
	if err := SaveWarpState(path, in); err != nil {
		t.Fatalf("SaveWarpState: %v", err)
	}
	out, err := LoadWarpState(path)
	if err != nil {
		t.Fatalf("LoadWarpState: %v", err)
	}
	if out != in {
		t.Fatalf("round trip = %#v, want %#v", out, in)
	}
}

func TestWarpState_MissingIsZero(t *testing.T) {
	s, err := LoadWarpState(t.TempDir() + "/absent.json")
	if err != nil || s != (WarpState{}) {
		t.Fatalf("missing file = %#v, %v; want zero, nil", s, err)
	}
}

func TestWarpState_CorruptErrors(t *testing.T) {
	path := t.TempDir() + "/warp.json"
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWarpState(path); err == nil || !strings.Contains(err.Error(), "warp state") {
		t.Fatalf("corrupt state error = %v", err)
	}
}
