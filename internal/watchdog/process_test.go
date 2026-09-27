package watchdog

import (
	"testing"
	"time"
)

const psFixture = `    PID    PPID  %CPU ELAPSED ARGS
      1       0   0.1 90-04:12:33 /sbin/launchd
    501       1   0.0 90-04:12:30 /usr/libexec/UserEventAgent
  88123       1 152.3 2-03:41:07 /var/folders/ab/xyz/T/ahub-recovery-cli-9Xk2/bun run index.ts
  88124   88123  11.0    04:22 /usr/bin/python3 worker.py
  90001       1  49.9    30:05 /opt/homebrew/bin/cloudflared tunnel run
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
	wantEtime := 2*24*time.Hour + 3*time.Hour + 41*time.Minute + 7*time.Second
	if runaway.Etime != wantEtime {
		t.Fatalf("Etime = %v, want %v", runaway.Etime, wantEtime)
	}
}

func TestParseEtime(t *testing.T) {
	cases := map[string]time.Duration{
		"04:22":       4*time.Minute + 22*time.Second,
		"30:05":       30*time.Minute + 5*time.Second,
		"03:41:07":    3*time.Hour + 41*time.Minute + 7*time.Second,
		"2-03:41:07":  2*24*time.Hour + 3*time.Hour + 41*time.Minute + 7*time.Second,
		"90-04:12:33": 90*24*time.Hour + 4*time.Hour + 12*time.Minute + 33*time.Second,
	}
	for in, want := range cases {
		got, err := ParseEtime(in)
		if err != nil {
			t.Fatalf("ParseEtime(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ParseEtime(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "1", "a:b", "1:2:3:4", "x-1:00"} {
		if _, err := ParseEtime(bad); err == nil {
			t.Errorf("ParseEtime(%q) must fail", bad)
		}
	}
}

func TestParsePS_RejectsMalformedRows(t *testing.T) {
	for _, out := range []string{
		"PID PPID %CPU ELAPSED ARGS\nnotanum 1 5.0 00:10 x\n",
		"PID PPID %CPU ELAPSED ARGS\n1 1 5.0 00:10\n",
		"PID PPID %CPU ELAPSED ARGS\n1 1 xx 00:10 x\n",
	} {
		if _, err := ParsePS(out); err == nil {
			t.Errorf("ParsePS(%q) must fail", out)
		}
	}
}

func TestStartKey_SeparatesPIDReuse(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := Process{PID: 4242, Etime: 2 * time.Hour}
	fresh := Process{PID: 4242, Etime: time.Minute}
	if old.StartKey(now) == fresh.StartKey(now) {
		t.Fatalf("PID reuse collapsed into one key: %q", old.StartKey(now))
	}
	// The same live process sampled a minute later must keep its key:
	// now and etime both advanced by the same minute.
	later := Process{PID: 4242, Etime: 3 * time.Hour}
	if old.StartKey(now) != later.StartKey(now.Add(time.Hour)) {
		t.Fatalf("same process drifted keys across runs: %q vs %q", old.StartKey(now), later.StartKey(now.Add(time.Hour)))
	}
}
