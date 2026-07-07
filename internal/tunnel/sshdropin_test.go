package tunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHDropInAddListRemove(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SSHConfigPath(home), []byte("Include ~/.ssh/config.d/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	added, _, err := AddHost(home, "mac.example.com")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	if !added {
		t.Fatal("first AddHost should report added=true")
	}
	added, _, err = AddHost(home, "mac.example.com")
	if err != nil {
		t.Fatalf("AddHost idempotent: %v", err)
	}
	if added {
		t.Fatal("duplicate AddHost should report added=false")
	}
	hosts, err := ListHosts(home)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "mac.example.com" {
		t.Fatalf("hosts = %v", hosts)
	}

	data, err := os.ReadFile(DropInPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), "Host mac.example.com"); count != 1 {
		t.Fatalf("Host block count = %d\n%s", count, data)
	}
	if !strings.Contains(string(data), "ProxyCommand cloudflared access ssh --hostname %h") {
		t.Fatalf("drop-in missing ProxyCommand:\n%s", data)
	}

	removed, err := RemoveHost(home, "mac.example.com")
	if err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	if !removed {
		t.Fatal("expected host to be removed")
	}
	hosts, err = ListHosts(home)
	if err != nil {
		t.Fatalf("ListHosts after remove: %v", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("hosts after remove = %v", hosts)
	}
}

func TestSSHDropInPreservesHeaderAndOtherBlocks(t *testing.T) {
	home := t.TempDir()
	existing := "# custom header\n# keep me\n\nHost old.example.com\n    ProxyCommand cloudflared access ssh --hostname %h\n"
	if err := os.MkdirAll(filepath.Dir(DropInPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DropInPath(home), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := AddHost(home, "new.example.com"); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	data, err := os.ReadFile(DropInPath(home))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"# custom header",
		"# keep me",
		"Host old.example.com",
		"Host new.example.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("drop-in missing %q:\n%s", want, text)
		}
	}
}

func TestSSHDropInWarnings(t *testing.T) {
	home := t.TempDir()
	_, warnings, err := AddHost(home, "mac.example.com")
	if err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "ssh config does not include") {
		t.Fatalf("expected Include warning, got %v", warnings)
	}
}

// Hand-edited drop-ins use every valid ssh_config spelling of Host — the
// parser must still see them so list/remove don't corrupt neighboring blocks.
func TestSSHDropInTolerantHostParsing(t *testing.T) {
	home := t.TempDir()
	existing := strings.Join([]string{
		"# header",
		"",
		"host lower.example.com",
		"    ProxyCommand cloudflared access ssh --hostname %h",
		"",
		"Host\ttabbed.example.com",
		"    ProxyCommand cloudflared access ssh --hostname %h",
		"",
		"  Host indented.example.com",
		"    ProxyCommand cloudflared access ssh --hostname %h",
		"",
	}, "\n")
	if err := os.MkdirAll(filepath.Dir(DropInPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DropInPath(home), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	hosts, err := ListHosts(home)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	want := []string{"indented.example.com", "lower.example.com", "tabbed.example.com"}
	if strings.Join(hosts, ",") != strings.Join(want, ",") {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}

	// A duplicate add against a hand-edited spelling must be detected.
	if added, _, err := AddHost(home, "lower.example.com"); err != nil || added {
		t.Fatalf("duplicate against hand-edited block: added=%v err=%v", added, err)
	}

	removed, err := RemoveHost(home, "tabbed.example.com")
	if err != nil || !removed {
		t.Fatalf("RemoveHost tabbed: removed=%v err=%v", removed, err)
	}
	data, _ := os.ReadFile(DropInPath(home))
	text := string(data)
	if strings.Contains(text, "tabbed.example.com") {
		t.Fatalf("tabbed block not removed:\n%s", text)
	}
	for _, keep := range []string{"lower.example.com", "indented.example.com"} {
		if !strings.Contains(text, keep) {
			t.Fatalf("block %q lost during remove:\n%s", keep, text)
		}
	}
}

func TestIncludesConfigDVariants(t *testing.T) {
	for _, ok := range []string{
		"Include ~/.ssh/config.d/*",
		"include config.d/*",
		"Include\tconfig.d/dot-tunnel",
		"Include other.conf config.d/*",
	} {
		if !includesConfigD(ok + "\n") {
			t.Fatalf("includesConfigD(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"# Include config.d/*",
		"IdentityFile ~/.ssh/config.d-key",
		"",
	} {
		if includesConfigD(bad + "\n") {
			t.Fatalf("includesConfigD(%q) = true, want false", bad)
		}
	}
}
