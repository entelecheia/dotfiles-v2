package aisettings

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// fakeTransport serves SyncTransport from an in-process SyncDB — the two
// ends of a sync as two temp databases.
type fakeTransport struct {
	db *SyncDB
}

func (f *fakeTransport) Max(context.Context, SyncPeer) (map[string]string, error) {
	return f.db.MaxCreatedAt()
}
func (f *fakeTransport) Export(_ context.Context, _ SyncPeer, cutoff string, onlySessions bool) (*Bundle, error) {
	return f.db.Export(cutoff, onlySessions)
}
func (f *fakeTransport) Import(_ context.Context, _ SyncPeer, b *Bundle) (*ImportResult, error) {
	return f.db.Import(b)
}
func (f *fakeTransport) Counts(context.Context, SyncPeer) (*TableCounts, error) {
	return f.db.Counts()
}

func seedPair(t *testing.T) (local, remote *SyncDB) {
	t.Helper()
	local = newTestSyncDB(t)
	remote = newTestSyncDB(t)
	seedSession(t, local, "c1", "m1", "2026-09-20T10:00:00.000Z")
	seedObs(t, local, "m1", "2026-09-20T10:01:00.000Z", "local-obs")
	seedSummary(t, local, "m1", "2026-09-20T10:02:00.000Z", "local-req")
	seedSession(t, remote, "c2", "m2", "2026-09-21T10:00:00.000Z")
	seedObs(t, remote, "m2", "2026-09-21T10:01:00.000Z", "remote-obs")
	return local, remote
}

func TestRunMemorySync_BothDirections(t *testing.T) {
	local, remote := seedPair(t)
	peer := SyncPeer{Target: "fake"}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	report, err := RunMemorySync(context.Background(), local, &fakeTransport{db: remote}, peer, SyncOptions{Direction: "sync"}, now)
	if err != nil {
		t.Fatalf("RunMemorySync: %v", err)
	}
	if report.Pushed.Obs != 1 || report.Pushed.Sums != 1 || report.Pushed.Sessions != 1 {
		t.Fatalf("pushed = %#v", report.Pushed)
	}
	if report.Pulled.Obs != 1 || report.Pulled.Sessions != 1 {
		t.Fatalf("pulled = %#v", report.Pulled)
	}
	// Both sides now hold each other's rows.
	for _, db := range []*SyncDB{local, remote} {
		counts, err := db.Counts()
		if err != nil {
			t.Fatal(err)
		}
		if counts.Obs != 2 || counts.Sessions != 2 {
			t.Fatalf("counts after sync = %#v", counts)
		}
	}
	// The second run is a no-op: incremental cutoff + dedupe.
	report2, err := RunMemorySync(context.Background(), local, &fakeTransport{db: remote}, peer, SyncOptions{Direction: "sync"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report2.Pushed.Obs != 0 || report2.Pulled.Obs != 0 {
		t.Fatalf("second run must import 0: %#v / %#v", report2.Pushed, report2.Pulled)
	}
}

func TestRunMemorySync_PushPullOnly(t *testing.T) {
	local, remote := seedPair(t)
	peer := SyncPeer{Target: "fake"}
	now := time.Now()

	report, err := RunMemorySync(context.Background(), local, &fakeTransport{db: remote}, peer, SyncOptions{Direction: "push"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Pushed == nil || report.Pulled != nil {
		t.Fatalf("push-only report = %#v", report)
	}
	if counts, _ := local.Counts(); counts.Obs != 1 {
		t.Fatalf("push must not pull: %#v", counts)
	}

	report, err = RunMemorySync(context.Background(), local, &fakeTransport{db: remote}, peer, SyncOptions{Direction: "pull"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Pulled == nil || report.Pushed != nil {
		t.Fatalf("pull-only report = %#v", report)
	}
	if counts, _ := local.Counts(); counts.Obs != 2 {
		t.Fatalf("pull must land remote rows: %#v", counts)
	}

	if _, err := RunMemorySync(context.Background(), local, &fakeTransport{db: remote}, peer, SyncOptions{Direction: "sideways"}, now); err == nil {
		t.Fatal("unknown direction must error")
	}
}

func TestRunMemorySync_SessionsMode(t *testing.T) {
	local, remote := seedPair(t)
	report, err := RunMemorySync(context.Background(), local, &fakeTransport{db: remote}, SyncPeer{Target: "fake"},
		SyncOptions{Direction: "sessions", OnlySessions: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Sessions cross; content rows do not.
	if counts, _ := local.Counts(); counts.Sessions != 2 || counts.Obs != 1 {
		t.Fatalf("local after sessions pass = %#v", counts)
	}
	if counts, _ := remote.Counts(); counts.Sessions != 2 || counts.Obs != 1 {
		t.Fatalf("remote after sessions pass = %#v", counts)
	}
	_ = report
}

// TestSSHTransportOverServe drives the whole wire protocol against a temp
// DB: SSHTransport with a runner that hands the request to ServeOp.
func TestSSHTransportOverServe(t *testing.T) {
	remote := newTestSyncDB(t)
	seedSession(t, remote, "c1", "m1", "2026-09-20T10:00:00.000Z")
	seedObs(t, remote, "m1", "2026-09-20T10:01:00.000Z", "x")

	var lastArgs []string
	transport := &SSHTransport{Run: func(ctx context.Context, target string, serveArgs []string, stdin []byte) ([]byte, error) {
		lastArgs = serveArgs
		// serveArgs: ai memory sync --serve <op> [--remote-db <path>]
		op := serveArgs[4]
		var out bytes.Buffer
		if err := ServeOp(ctx, remote.Path, op, bytes.NewReader(stdin), &out); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}}
	peer := SyncPeer{Target: "user@mac2"}

	maxes, err := transport.Max(context.Background(), peer)
	if err != nil {
		t.Fatalf("Max: %v", err)
	}
	if maxes["observations"] == "" {
		t.Fatalf("maxes = %v", maxes)
	}
	bundle, err := transport.Export(context.Background(), peer, "", false)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(bundle.Observations) != 1 || len(bundle.Sessions) != 1 {
		t.Fatalf("bundle = %d obs %d sessions", len(bundle.Observations), len(bundle.Sessions))
	}
	counts, err := transport.Counts(context.Background(), peer)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Obs != 1 || counts.ObsWithoutSession != 0 {
		t.Fatalf("counts = %#v", counts)
	}

	// Import over the wire into the same DB dedupes to zero.
	res, err := transport.Import(context.Background(), peer, bundle)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Obs != 0 || res.Sessions != 0 {
		t.Fatalf("self-import must dedupe: %#v", res)
	}
	if !strings.Contains(strings.Join(lastArgs, " "), "--serve import") {
		t.Fatalf("serve args = %v", lastArgs)
	}

	// A failing op comes back as an error, not a zero value.
	bad := &SSHTransport{Run: func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte(`{"error":"boom"}`), nil
	}}
	if _, err := bad.Counts(context.Background(), peer); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("serve error must propagate: %v", err)
	}
	if _, err := transport.Counts(context.Background(), SyncPeer{}); err == nil {
		t.Fatal("empty target must error")
	}
}

func TestServeOp_UnknownOpAndBadDB(t *testing.T) {
	var out bytes.Buffer
	if err := ServeOp(context.Background(), filepath_join(t), "frobulate", strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unknown serve op") {
		t.Fatalf("response = %s", out.String())
	}
	out.Reset()
	if err := ServeOp(context.Background(), "/nonexistent/dir/db.sqlite", "count", strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "error") {
		t.Fatalf("response = %s", out.String())
	}
	// Serve writes ONLY the response envelope to stdout.
	if strings.Contains(out.String(), "\n\n") {
		t.Fatalf("serve output must be one JSON document: %q", out.String())
	}
}

func filepath_join(t *testing.T) string {
	t.Helper()
	return newTestSyncDB(t).Path
}

func TestSyncState_RoundTrip(t *testing.T) {
	path := SyncStatePath(t.TempDir())
	entry := SyncStateEntry{LastRun: time.Date(2026, 9, 27, 1, 2, 3, 0, time.UTC), LastResult: "pushed +1 obs"}
	if err := SaveSyncState(path, "mac2", entry); err != nil {
		t.Fatal(err)
	}
	// A second peer preserves the first.
	if err := SaveSyncState(path, "mac3", entry); err != nil {
		t.Fatal(err)
	}
	peers, err := LoadSyncState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 || peers["mac2"].LastResult != "pushed +1 obs" || !peers["mac2"].LastRun.Equal(entry.LastRun) {
		t.Fatalf("peers = %#v", peers)
	}
	if got, err := LoadSyncState(t.TempDir() + "/absent.json"); err != nil || len(got) != 0 {
		t.Fatalf("missing file = %#v, %v", got, err)
	}
}

func TestSyncReportSummarize(t *testing.T) {
	r := &SyncReport{Pushed: &ImportResult{Obs: 1, Sums: 2, Sessions: 3}}
	if got := r.Summarize(); got != "pushed +1 obs +2 sums +3 sessions" {
		t.Errorf("Summarize = %q", got)
	}
	if got := (&SyncReport{}).Summarize(); got != "no rows transferred" {
		t.Errorf("empty Summarize = %q", got)
	}
}
