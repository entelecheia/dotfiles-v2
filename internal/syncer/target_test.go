package syncer

import (
	"slices"
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		spec     string
		wantKind TargetKind
		wantPath string
		wantHost string
		wantErr  bool
	}{
		{spec: "local:~/Dropbox/work", wantKind: TargetLocal, wantPath: "~/Dropbox/work"},
		{spec: "~/Dropbox/work", wantKind: TargetLocal, wantPath: "~/Dropbox/work"},
		{spec: "ssh:me@host:~/workspace/work", wantKind: TargetSSH, wantHost: "me@host", wantPath: "~/workspace/work"},
		{spec: "ssh:me@host", wantErr: true},
		{spec: "local:", wantErr: true},
		{spec: "  ", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseTarget(c.spec)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseTarget(%q) expected error, got %+v", c.spec, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTarget(%q): %v", c.spec, err)
			continue
		}
		if got.Kind != c.wantKind || got.Path != c.wantPath || got.Host != c.wantHost {
			t.Errorf("ParseTarget(%q) = %+v, want kind=%s path=%s host=%s",
				c.spec, got, c.wantKind, c.wantPath, c.wantHost)
		}
	}
}

func TestTarget_RsyncDest(t *testing.T) {
	local := Target{Kind: TargetLocal, Path: "/tmp/mirror"}
	if got := local.RsyncDest(); got != "/tmp/mirror/" {
		t.Errorf("local RsyncDest = %q", got)
	}
	remote := Target{Kind: TargetSSH, Host: "me@host", Path: "~/work"}
	if got := remote.RsyncDest(); got != "me@host:~/work/" {
		t.Errorf("ssh RsyncDest = %q", got)
	}
	if !remote.IsSSH() || local.IsSSH() {
		t.Error("IsSSH misreports")
	}
}

func TestTarget_StringRoundTrip(t *testing.T) {
	for _, spec := range []string{"local:/tmp/mirror", "ssh:me@host:/srv/work"} {
		parsed, err := ParseTarget(spec)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", spec, err)
		}
		if parsed.String() != spec {
			t.Errorf("round trip %q -> %q", spec, parsed.String())
		}
	}
}

func TestPushPullArgs_SSHTransport(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Target = Target{Kind: TargetSSH, Host: "me@host", Path: "~/work"}
	cfg.MirrorPath = ""
	conflict := NewConflictDir()

	push := pushArgs(cfg, conflict, runtimeFilters{}, false)
	joined := strings.Join(push, " ")
	if !strings.Contains(joined, "-e ssh") {
		t.Errorf("ssh push args missing -e ssh: %v", push)
	}
	if push[len(push)-1] != "me@host:~/work/" {
		t.Errorf("ssh push dest = %q, want me@host:~/work/", push[len(push)-1])
	}
	if push[len(push)-2] != cfg.LocalPath {
		t.Errorf("ssh push source = %q, want %q", push[len(push)-2], cfg.LocalPath)
	}

	pull := pullArgs(cfg, conflict, runtimeFilters{}, false)
	if pull[len(pull)-2] != "me@host:~/work/" || pull[len(pull)-1] != cfg.LocalPath {
		t.Errorf("ssh pull src/dest wrong: %v", pull[len(pull)-2:])
	}
	if !slices.Contains(pull, "--update") {
		t.Errorf("ssh pull args missing --update: %v", pull)
	}
}
