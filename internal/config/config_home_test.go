package config

import (
	"path/filepath"
	"testing"
)

func TestConfigHomeUsesCanonicalStateAuthority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	xdg := filepath.Join(home, "custom-config")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got := ConfigHome(home, false); got != xdg {
		t.Fatalf("ambient root %q", got)
	}
	if got := ConfigHome(home, true); got != filepath.Join(home, ".config") {
		t.Fatalf("explicit home leaked XDG: %q", got)
	}
	other := t.TempDir()
	if got := ConfigHome(other, false); got != filepath.Join(other, ".config") {
		t.Fatalf("other home leaked XDG: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if got := ConfigHome(home, false); filepath.IsAbs(got) {
		t.Fatalf("relative root was silently corrected: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := ConfigHome(home, false); got != filepath.Join(home, ".config") {
		t.Fatalf("default root %q", got)
	}
}
