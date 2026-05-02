package module

import (
	"testing"

	"github.com/entelecheia/dotfiles-v2/internal/config"
)

func TestMacAppsResolveCasks_AppendsExtrasToDefaults(t *testing.T) {
	m := &MacAppsModule{}
	rc := &RunContext{
		Config: &config.Config{
			CasksExtra: []string{"iterm2"},
		},
	}

	got := m.resolveCasks(rc)
	if !contains(got, "arc") {
		t.Fatalf("default casks were not preserved: %v", got)
	}
	if !contains(got, "iterm2") {
		t.Fatalf("extra cask missing: %v", got)
	}
}

func TestMacAppsResolveCasks_ConfiguredCasksWin(t *testing.T) {
	m := &MacAppsModule{}
	rc := &RunContext{
		Config: &config.Config{
			Casks:      []string{"raycast"},
			CasksExtra: []string{"raycast", "iterm2"},
		},
	}

	got := m.resolveCasks(rc)
	want := []string{"raycast", "iterm2"}
	if len(got) != len(want) {
		t.Fatalf("resolveCasks length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolveCasks[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
