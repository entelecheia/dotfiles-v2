package syncer

import (
	"os"
	"testing"
	"time"
)

func TestRejectGenericPeerProfile(t *testing.T) {
	if err := RejectGenericPeerProfile(&Config{Profile: PeerProfile}); err == nil {
		t.Fatal("generic sync commands must not bypass the peer tombstone transaction")
	}
	if err := RejectGenericPeerProfile(&Config{Profile: DefaultProfile}); err != nil {
		t.Fatalf("default sync profile rejected: %v", err)
	}
}

func TestSetLocalSchedule_DryRunDoesNotPersist(t *testing.T) {
	paths := ResolveLocalPaths(t.TempDir())
	cfg := &Config{LocalPaths: paths}

	if err := SetLocalSchedule(cfg, 600, 900, ModeClean, ModeForce, true); err != nil {
		t.Fatalf("setLocalSchedule dry-run: %v", err)
	}
	if cfg.Interval != 600 || cfg.PullInterval != 900 || cfg.PushMode != ModeClean || cfg.PullMode != ModeForce {
		t.Fatalf("dry-run should still update runtime cfg for planning, got %+v", cfg)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write local config; stat err=%v", err)
	}
}

func TestResolvePruneCutoff(t *testing.T) {
	cases := []struct {
		name         string
		olderDays    int
		all          bool
		olderChanged bool
		wantErr      bool
		wantAgeDays  int // expected approximate distance from now
	}{
		{name: "default 30 days", olderDays: 30, wantAgeDays: 30},
		{name: "explicit 7 days", olderDays: 7, olderChanged: true, wantAgeDays: 7},
		{name: "all prunes everything", all: true, olderDays: 30, wantAgeDays: 0},
		{name: "all with explicit older-than rejected", all: true, olderDays: 7, olderChanged: true, wantErr: true},
		{name: "negative rejected", olderDays: -1, olderChanged: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cutoff, err := ResolvePruneCutoff(tc.olderDays, tc.all, tc.olderChanged)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			got := time.Since(cutoff).Round(time.Minute)
			want := time.Duration(tc.wantAgeDays) * 24 * time.Hour
			if got != want {
				t.Errorf("cutoff age = %v, want %v", got, want)
			}
		})
	}
}
