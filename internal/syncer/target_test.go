package syncer

import "testing"

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
