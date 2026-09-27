package watchdog

import (
	"testing"
)

const psFixture = `    PID    PPID  %CPU STARTED                        ARGS
      1       0   0.1 Sat Jun 27 08:00:00 2026 /sbin/launchd
    501       1   0.0 Sat Jun 27 08:00:03 2026 /usr/libexec/UserEventAgent
  88123       1 152.3 Thu Sep 24 10:15:30 2026 /var/folders/ab/xyz/T/ahub-recovery-cli-9Xk2/bun run index.ts
  88124   88123  11.0 Sun Sep 27 07:55:00 2026 /usr/bin/python3 worker.py
  90001       1  49.9 Sun Sep 27 07:30:00 2026 /opt/homebrew/bin/cloudflared tunnel run
`

func TestParsePS_Fixture(t *testing.T) {
	procs, err := ParsePS(psFixture)
	if err != nil {
		t.Fatalf("ParsePS: %v", err)
	}
	if len(procs) != 5 {
		t.Fatalf("parsed %d processes, want 5: %#v", len(procs), procs)
	}
	runaway := procs[2]
	if runaway.PID != 88123 || runaway.PPID != 1 || runaway.CPU != 152.3 {
		t.Fatalf("runaway row = %#v", runaway)
	}
	if runaway.Executable() != "/var/folders/ab/xyz/T/ahub-recovery-cli-9Xk2/bun" {
		t.Fatalf("Executable = %q", runaway.Executable())
	}
	if runaway.Args != "/var/folders/ab/xyz/T/ahub-recovery-cli-9Xk2/bun run index.ts" {
		t.Fatalf("Args lost the argument tail: %q", runaway.Args)
	}
	if runaway.Started != "Thu Sep 24 10:15:30 2026" {
		t.Fatalf("Started = %q", runaway.Started)
	}
}

func TestParsePS_RejectsMalformedRows(t *testing.T) {
	for _, out := range []string{
		"PID PPID %CPU STARTED ARGS\nnotanum 1 5.0 Sun Sep 27 08:00:00 2026 x\n",
		"PID PPID %CPU STARTED ARGS\n1 1 5.0 Sun Sep 27 08:00:00 2026\n",
		"PID PPID %CPU STARTED ARGS\n1 1 xx Sun Sep 27 08:00:00 2026 x\n",
		"PID PPID %CPU STARTED ARGS\n1 1 5.0 99 Bottles Of Beer 2026 x\n",
	} {
		if _, err := ParsePS(out); err == nil {
			t.Errorf("ParsePS(%q) must fail", out)
		}
	}
}

func TestStartKey_SeparatesPIDReuse(t *testing.T) {
	old := Process{PID: 4242, Started: "Sat Sep 26 08:00:00 2026"}
	fresh := Process{PID: 4242, Started: "Sun Sep 27 08:00:00 2026"}
	if old.StartKey() == fresh.StartKey() {
		t.Fatalf("PID reuse collapsed into one key: %q", old.StartKey())
	}
	// lstart is exact, so the same live process keeps one key forever —
	// no derived-start jitter can split its sustain streak across runs.
	again := Process{PID: 4242, Started: "Sat Sep 26 08:00:00 2026"}
	if old.StartKey() != again.StartKey() {
		t.Fatalf("same process drifted keys across runs: %q vs %q", old.StartKey(), again.StartKey())
	}
}
