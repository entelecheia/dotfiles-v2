package syncer

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
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
		{spec: "ssh:me@host:-relative-remote-path", wantKind: TargetSSH, wantHost: "me@host", wantPath: "-relative-remote-path"},
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

func TestParseTarget_RejectsUnsafeRawFields(t *testing.T) {
	invalidUTF8 := string([]byte{'l', 'o', 'c', 'a', 'l', ':', 0xff})
	cases := []struct {
		name      string
		spec      string
		wantClass string
	}{
		{name: "empty spec", spec: "", wantClass: "empty"},
		{name: "empty local path", spec: "local:", wantClass: "empty"},
		{name: "empty ssh host", spec: "ssh::/srv/work", wantClass: "empty"},
		{name: "empty ssh path", spec: "ssh:me@host:", wantClass: "empty"},
		{name: "invalid UTF-8", spec: invalidUTF8, wantClass: "invalid UTF-8"},
		{name: "unicode whitespace", spec: "local:/tmp\u00a0mirror", wantClass: "Unicode whitespace"},
		{name: "local leading ASCII space", spec: "local: /tmp/mirror", wantClass: "Unicode whitespace"},
		{name: "local internal ASCII space", spec: "local:/tmp/mirror copy", wantClass: "Unicode whitespace"},
		{name: "local trailing ASCII space", spec: "local:/tmp/mirror ", wantClass: "Unicode whitespace"},
		{name: "bare leading ASCII space", spec: " /tmp/mirror", wantClass: "Unicode whitespace"},
		{name: "bare internal ASCII space", spec: "/tmp/mirror copy", wantClass: "Unicode whitespace"},
		{name: "bare trailing ASCII space", spec: "/tmp/mirror ", wantClass: "Unicode whitespace"},
		{name: "ssh host ASCII space", spec: "ssh:me @host:/srv/work", wantClass: "Unicode whitespace"},
		{name: "ssh path ASCII space", spec: "ssh:me@host:/srv/work copy", wantClass: "Unicode whitespace"},
		{name: "ssh trailing ASCII space", spec: "ssh:me@host:/srv/work ", wantClass: "Unicode whitespace"},
		{name: "tab", spec: "ssh:me\t@host:/srv/work", wantClass: "control"},
		{name: "newline", spec: "local:/tmp\nmirror", wantClass: "control"},
		{name: "c1 control", spec: "ssh:me@host:/srv/\u0085work", wantClass: "control"},
		{name: "dash local path", spec: "local:-/tmp/mirror", wantClass: "leading option marker"},
		{name: "dash bare path", spec: "-mirror", wantClass: "leading option marker"},
		{name: "space ssh host", spec: "ssh:me @host:/srv/work", wantClass: "Unicode whitespace"},
		{name: "dash ssh host", spec: "ssh:-oProxyCommand=bad:/srv/work", wantClass: "leading option marker"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTarget(tc.spec)
			if err == nil {
				t.Fatalf("ParseTarget(%q) unexpectedly accepted %+v", tc.spec, got)
			}
			if got != (Target{}) {
				t.Fatalf("ParseTarget(%q) returned target %+v with error %v", tc.spec, got, err)
			}
			if !strings.Contains(err.Error(), tc.wantClass) {
				t.Errorf("ParseTarget(%q) error %q does not name %q", tc.spec, err, tc.wantClass)
			}
			if !utf8.ValidString(err.Error()) {
				t.Errorf("ParseTarget(%q) error contains invalid UTF-8: %q", tc.spec, err)
			}
			if strings.ContainsAny(err.Error(), "\t\n\r\x00\u0085") {
				t.Errorf("ParseTarget(%q) error leaks a raw control: %q", tc.spec, err)
			}
		})
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
