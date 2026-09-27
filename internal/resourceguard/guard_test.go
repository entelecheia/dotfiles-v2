package resourceguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testGuard(t *testing.T) (*guard, *time.Time, *Sample) {
	t.Helper()
	now := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	s := Sample{MemoryNormal: true, ThermalKnown: true, CPUIdle: 80, Load1: 1, CPUs: 8, Uptime: time.Hour}
	dir := filepath.Join(t.TempDir(), "private-guard")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	g := &guard{dir: dir, now: func() time.Time { return now }, probe: func(context.Context) (Sample, error) { return s, nil }, identity: func(context.Context, int) (string, error) { return "start-identity", nil }}
	return g, &now, &s
}
func seedHealthy(t *testing.T, g *guard, now *time.Time) {
	t.Helper()
	for i := 0; i <= 20; i++ {
		if i > 0 {
			*now = now.Add(sampleInterval)
		}
		if _, _, err := g.observe(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
func TestAcquireRecoveryAndRepositorySlot(t *testing.T) {
	g, now, _ := testGuard(t)
	ctx := context.Background()
	if _, err := g.acquire(ctx, Options{}); err == nil {
		t.Fatal("first sample admitted without history")
	}
	seedHealthy(t, g, now)
	release, err := g.acquire(ctx, Options{HomeDir: "/profile-a", Purpose: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = g.acquire(ctx, Options{HomeDir: "/profile-b"}); err == nil {
		t.Fatal("separate home bypassed repository slot")
	}
	release()
	release()
	release2, err := g.acquire(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	release2()
}
func TestPressureAndGapsResetRecovery(t *testing.T) {
	for _, which := range []string{"memory", "thermal", "unknown-thermal", "watchdog", "uncovered", "gap", "cpu"} {
		t.Run(which, func(t *testing.T) {
			g, now, s := testGuard(t)
			seedHealthy(t, g, now)
			switch which {
			case "memory":
				s.MemoryNormal = false
			case "thermal":
				s.ThermalHeavy = true
			case "unknown-thermal":
				s.ThermalKnown = false
			case "watchdog":
				s.RecentWatchdog = true
			case "uncovered":
				s.Uncovered = []string{"cargo(pid=123)"}
			case "gap":
				*now = now.Add(time.Minute)
			case "cpu":
				s.CPUIdle = 10
			}
			_, err := g.acquire(context.Background(), Options{})
			var deferred *DeferredError
			if !errors.As(err, &deferred) {
				t.Fatalf("want deferred, got %v", err)
			}
		})
	}
}
func TestCorruptHistoryFailsClosed(t *testing.T) {
	g, _, _ := testGuard(t)
	if err := os.WriteFile(filepath.Join(g.dir, "health.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.acquire(context.Background(), Options{}); err == nil {
		t.Fatal("corrupt history accepted")
	}
}
func TestUnlockedExpiredOwnerRecoverable(t *testing.T) {
	g, now, _ := testGuard(t)
	seedHealthy(t, g, now)
	scope, err := ResolveScope(Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir := scopeDirectory(g.dir, scope)
	if err := secureDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "owner.json"), Owner{PID: 99999, Expires: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	release, err := g.acquire(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestResourceDirRejectsSymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if err := secureDir(dir); err == nil {
		t.Fatal("symlink state accepted")
	}
}
func TestHeavyweightInventoryClassification(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       bool
	}{{"cargo", "cargo test --lib", true}, {"go", "go test ./...", true}, {"cp", "cp -cR /x/target /y/target", true}, {"node", "node /x/playwright test", true}, {"go", "go version", false}, {"ps", "ps -axo pid", false}, {"git", "git status", false}, {"node", "node server.js", false}} {
		if got := heavyweight(tc.name, tc.args); got != tc.want {
			t.Errorf("%s %s: got%t", tc.name, tc.args, got)
		}
	}
}
func TestLimitsReplaceOnlyScopedVariables(t *testing.T) {
	got := limitedEnvironment([]string{"HOME=/h", "GOMAXPROCS=99", "CARGO_BUILD_JOBS=9", "RUST_TEST_THREADS=8"})
	if len(got) != 4 || got[0] != "HOME=/h" {
		t.Fatalf("%v", got)
	}
}

func TestTelemetryFailureBreaksRecovery(t *testing.T) {
	g, now, _ := testGuard(t)
	seedHealthy(t, g, now)
	original := g.probe
	g.probe = func(context.Context) (Sample, error) { return Sample{}, errors.New("unavailable") }
	if _, _, err := g.observe(context.Background()); err == nil {
		t.Fatal("unknown observation accepted")
	}
	g.probe = original
	*now = now.Add(sampleInterval)
	if _, err := g.acquire(context.Background(), Options{}); err == nil {
		t.Fatal("missing telemetry bridged recovery history")
	}
}
